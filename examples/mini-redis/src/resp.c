#include "resp.h"
#include <stdlib.h>
#include <string.h>
#include <stdio.h>
#include <inttypes.h>

/* Find \r\n in buf[0..len). Returns offset of \r, or -1 if not found. */
static int parse_line(const char *buf, size_t len, size_t *line_len) {
    for (size_t i = 0; i + 1 < len; i++) {
        if (buf[i] == '\r' && buf[i + 1] == '\n') {
            *line_len = i;
            return 0;
        }
    }
    return -1; /* not found */
}

static int parse_integer_value(const char *buf, size_t len, int64_t *out) {
    if (len == 0) {
        return -1;
    }

    int64_t result = 0;
    int sign = 1;
    size_t i = 0;

    if (buf[0] == '-') {
        sign = -1;
        i = 1;
        if (len == 1) {
            return -1;
        }
    }

    for (; i < len; i++) {
        if (buf[i] < '0' || buf[i] > '9') {
            return -1;
        }
        result = result * 10 + (buf[i] - '0');
    }

    *out = result * sign;
    return 0;
}

static int parse_bulk_string(const char *buf, size_t len,
                             resp_value_t *out) {
    /* buf starts after '$' */
    size_t line_len;
    if (parse_line(buf, len, &line_len) < 0) {
        return 0; /* need more data */
    }

    int64_t slen;
    if (parse_integer_value(buf, line_len, &slen) < 0) {
        return -1;
    }

    /* null bulk string */
    if (slen == -1) {
        out->type = RESP_NULL_BULK_STRING;
        return (int)(1 + line_len + 2); /* $-1\r\n */
    }

    if (slen < 0) {
        return -1; /* invalid */
    }

    /* bytes consumed so far: '$' + line + \r\n */
    size_t header_len = 1 + line_len + 2;
    /* total needed: header + slen bytes + \r\n */
    size_t total = header_len + (size_t)slen + 2;

    if (1 + len < total) {
        /* we passed buf starting after '$', so check (len+1) vs total */
        /* Actually let's recompute: original buf starts at '$',
           len passed is from after '$'. Need line_len+2+slen+2 bytes
           from position after '$' */
        if (len < line_len + 2 + (size_t)slen + 2) {
            return 0; /* need more data */
        }
    }

    out->type = RESP_BULK_STRING;
    out->str = rstr_create(buf + line_len + 2, (size_t)slen);

    return (int)(1 + line_len + 2 + (size_t)slen + 2);
}

static int parse_array(const char *buf, size_t len, resp_value_t *out);

int resp_parse(const char *buf, size_t len, resp_value_t *out) {
    if (len == 0) {
        return 0;
    }

    switch (buf[0]) {
    case '+':
    case '-': {
        size_t line_len;
        if (parse_line(buf + 1, len - 1, &line_len) < 0) {
            return 0; /* need more data */
        }
        out->type = (buf[0] == '+') ? RESP_SIMPLE_STRING : RESP_ERROR;
        out->str = rstr_create(buf + 1, line_len);
        return (int)(1 + line_len + 2);
    }

    case ':': {
        size_t line_len;
        if (parse_line(buf + 1, len - 1, &line_len) < 0) {
            return 0;
        }
        int64_t val;
        if (parse_integer_value(buf + 1, line_len, &val) < 0) {
            return -1;
        }
        out->type = RESP_INTEGER;
        out->integer = val;
        return (int)(1 + line_len + 2);
    }

    case '$': {
        return parse_bulk_string(buf + 1, len - 1, out);
    }

    case '*': {
        return parse_array(buf, len, out);
    }

    default:
        return -1; /* malformed */
    }
}

static int parse_array(const char *buf, size_t len, resp_value_t *out) {
    /* buf[0] == '*' */
    size_t line_len;
    if (parse_line(buf + 1, len - 1, &line_len) < 0) {
        return 0;
    }

    int64_t count;
    if (parse_integer_value(buf + 1, line_len, &count) < 0) {
        return -1;
    }

    if (count < 0) {
        return -1;
    }

    size_t consumed = 1 + line_len + 2; /* *<count>\r\n */

    if (count == 0) {
        out->type = RESP_ARRAY;
        out->array.elements = NULL;
        out->array.count = 0;
        return (int)consumed;
    }

    resp_value_t *elements = malloc((size_t)count * sizeof(resp_value_t));
    if (!elements) {
        perror("malloc");
        exit(1);
    }

    for (int64_t i = 0; i < count; i++) {
        if (consumed >= len) {
            /* need more data */
            for (int64_t j = 0; j < i; j++) {
                resp_value_free(&elements[j]);
            }
            free(elements);
            return 0;
        }

        int r = resp_parse(buf + consumed, len - consumed, &elements[i]);
        if (r <= 0) {
            /* incomplete or error */
            for (int64_t j = 0; j < i; j++) {
                resp_value_free(&elements[j]);
            }
            free(elements);
            return r;
        }
        consumed += (size_t)r;
    }

    out->type = RESP_ARRAY;
    out->array.elements = elements;
    out->array.count = (int)count;
    return (int)consumed;
}

void resp_value_free(resp_value_t *val) {
    if (!val) {
        return;
    }

    switch (val->type) {
    case RESP_SIMPLE_STRING:
    case RESP_ERROR:
    case RESP_BULK_STRING:
        rstr_free(&val->str);
        break;
    case RESP_ARRAY:
        for (int i = 0; i < val->array.count; i++) {
            resp_value_free(&val->array.elements[i]);
        }
        free(val->array.elements);
        val->array.elements = NULL;
        val->array.count = 0;
        break;
    case RESP_INTEGER:
    case RESP_NULL_BULK_STRING:
        break;
    }
}

void resp_write_simple_string(client_t *c, const char *str) {
    client_write_append(c, "+", 1);
    client_write_append(c, str, strlen(str));
    client_write_append(c, "\r\n", 2);
}

void resp_write_error(client_t *c, const char *msg) {
    client_write_append(c, "-", 1);
    client_write_append(c, msg, strlen(msg));
    client_write_append(c, "\r\n", 2);
}

void resp_write_integer(client_t *c, int64_t val) {
    char buf[32];
    int n = snprintf(buf, sizeof(buf), ":%" PRId64 "\r\n", val);
    client_write_append(c, buf, (size_t)n);
}

void resp_write_bulk_string(client_t *c, const char *data, size_t len) {
    char header[32];
    int n = snprintf(header, sizeof(header), "$%zu\r\n", len);
    client_write_append(c, header, (size_t)n);
    client_write_append(c, data, len);
    client_write_append(c, "\r\n", 2);
}

void resp_write_null_bulk_string(client_t *c) {
    client_write_append(c, "$-1\r\n", 5);
}

void resp_write_array_header(client_t *c, int count) {
    char buf[32];
    int n = snprintf(buf, sizeof(buf), "*%d\r\n", count);
    client_write_append(c, buf, (size_t)n);
}
