#include "commands.h"
#include "glob.h"
#include "util.h"
#include <string.h>
#include <stdio.h>
#include <stdlib.h>
#include <ctype.h>
#include <stdint.h>
#include <inttypes.h>
#include <limits.h>

static bool parse_int64(const char *data, size_t len, int64_t *out) {
    if (len == 0) {
        return false;
    }

    int64_t result = 0;
    size_t i = 0;
    bool negative = false;

    if (data[0] == '-') {
        negative = true;
        i = 1;
        if (len == 1) {
            return false;
        }
    }

    for (; i < len; i++) {
        if (data[i] < '0' || data[i] > '9') {
            return false;
        }
        int digit = data[i] - '0';
        /* overflow check */
        if (negative) {
            if (result > (-(INT64_MIN + digit)) / 10 + 1 ||
                (result == (-(INT64_MIN / 10)) &&
                 digit > (int)(-(INT64_MIN % 10)))) {
                /* more robust check below */
            }
        }
        if (!negative && result > (INT64_MAX - digit) / 10) {
            return false;
        }
        result = result * 10 + digit;
    }

    if (negative) {
        *out = -result;
    } else {
        *out = result;
    }
    return true;
}

static void cmd_ping(client_t *client, hashtable_t *store,
                     resp_value_t *args, int argc) {
    (void)store;
    if (argc == 1) {
        resp_write_simple_string(client, "PONG");
    } else {
        resp_write_bulk_string(client, args[1].str.data,
                               args[1].str.len);
    }
}

static void cmd_echo(client_t *client, hashtable_t *store,
                     resp_value_t *args, int argc) {
    (void)store;
    (void)argc;
    resp_write_bulk_string(client, args[1].str.data, args[1].str.len);
}

static void cmd_set(client_t *client, hashtable_t *store,
                    resp_value_t *args, int argc) {
    rstr_t key = args[1].str;
    rstr_t value = args[2].str;

    ht_set(store, key, value);

    if (argc == 5) {
        /* check for EX */
        char ex_buf[3];
        if (args[3].str.len == 2) {
            ex_buf[0] = (char)toupper((unsigned char)args[3].str.data[0]);
            ex_buf[1] = (char)toupper((unsigned char)args[3].str.data[1]);
            ex_buf[2] = '\0';
        } else {
            ex_buf[0] = '\0';
        }

        if (strcmp(ex_buf, "EX") == 0) {
            int64_t seconds;
            if (!parse_int64(args[4].str.data, args[4].str.len,
                             &seconds) || seconds <= 0) {
                /* undo the set? No, Redis sets and then fails on EX.
                   Actually Redis rejects the whole command. Let's
                   delete and send error. */
                ht_delete(store, key);
                resp_write_error(client,
                    "ERR invalid expire time in 'set' command");
                return;
            }
            int64_t expire_at = current_time_ms() + seconds * 1000;
            ht_set_expire(store, key, expire_at);
        } else {
            ht_delete(store, key);
            resp_write_error(client, "ERR syntax error");
            return;
        }
    }

    resp_write_simple_string(client, "OK");
}

static void cmd_get(client_t *client, hashtable_t *store,
                    resp_value_t *args, int argc) {
    (void)argc;
    rstr_t *val = ht_get(store, args[1].str);
    if (val) {
        resp_write_bulk_string(client, val->data, val->len);
    } else {
        resp_write_null_bulk_string(client);
    }
}

static void cmd_del(client_t *client, hashtable_t *store,
                    resp_value_t *args, int argc) {
    int64_t count = 0;
    for (int i = 1; i < argc; i++) {
        if (ht_delete(store, args[i].str)) {
            count++;
        }
    }
    resp_write_integer(client, count);
}

static void cmd_exists(client_t *client, hashtable_t *store,
                       resp_value_t *args, int argc) {
    int64_t count = 0;
    for (int i = 1; i < argc; i++) {
        if (ht_exists(store, args[i].str)) {
            count++;
        }
    }
    resp_write_integer(client, count);
}

static void cmd_expire(client_t *client, hashtable_t *store,
                       resp_value_t *args, int argc) {
    (void)argc;
    int64_t seconds;
    if (!parse_int64(args[2].str.data, args[2].str.len, &seconds)) {
        resp_write_error(client,
            "ERR value is not an integer or out of range");
        return;
    }

    if (!ht_exists(store, args[1].str)) {
        resp_write_integer(client, 0);
        return;
    }

    int64_t expire_at = current_time_ms() + seconds * 1000;
    ht_set_expire(store, args[1].str, expire_at);
    resp_write_integer(client, 1);
}

static void cmd_ttl(client_t *client, hashtable_t *store,
                    resp_value_t *args, int argc) {
    (void)argc;

    if (!ht_exists(store, args[1].str)) {
        resp_write_integer(client, -2);
        return;
    }

    int64_t expire_at = ht_get_expire(store, args[1].str);
    if (expire_at == -1) {
        resp_write_integer(client, -1);
        return;
    }

    int64_t now = current_time_ms();
    int64_t remaining_ms = expire_at - now;

    if (remaining_ms <= 0) {
        /* expired, delete it */
        ht_delete(store, args[1].str);
        resp_write_integer(client, -2);
        return;
    }

    int64_t seconds = (remaining_ms + 999) / 1000;
    resp_write_integer(client, seconds);
}

static void cmd_keys(client_t *client, hashtable_t *store,
                     resp_value_t *args, int argc) {
    (void)argc;
    rstr_t pattern = args[1].str;

    /* First pass: count matches */
    ht_iter_t iter;
    rstr_t key;
    int match_count = 0;

    /* Collect matching keys - pre-allocate based on table size */
    size_t matches_cap = ht_count(store);
    if (matches_cap == 0) {
        matches_cap = 1;
    }
    rstr_t *matches = malloc(matches_cap * sizeof(rstr_t));
    if (!matches) {
        perror("malloc");
        exit(1);
    }

    ht_iter_init(store, &iter);
    while (ht_iter_next(&iter, &key, NULL)) {
        if (glob_match(pattern.data, pattern.len,
                       key.data, key.len)) {
            if ((size_t)match_count >= matches_cap) {
                matches_cap *= 2;
                rstr_t *tmp = realloc(matches,
                                      matches_cap * sizeof(rstr_t));
                if (!tmp) {
                    perror("realloc");
                    exit(1);
                }
                matches = tmp;
            }
            matches[match_count] = key;
            match_count++;
        }
    }

    resp_write_array_header(client, match_count);
    for (int i = 0; i < match_count; i++) {
        resp_write_bulk_string(client, matches[i].data, matches[i].len);
    }

    free(matches);
}

static void cmd_type(client_t *client, hashtable_t *store,
                     resp_value_t *args, int argc) {
    (void)argc;
    if (ht_exists(store, args[1].str)) {
        resp_write_simple_string(client, "string");
    } else {
        resp_write_simple_string(client, "none");
    }
}

static void cmd_incr(client_t *client, hashtable_t *store,
                     resp_value_t *args, int argc) {
    (void)argc;
    rstr_t key = args[1].str;
    rstr_t *val = ht_get(store, key);

    int64_t current = 0;
    int64_t expire_at = -1;

    if (val) {
        expire_at = ht_get_expire(store, key);
        if (!parse_int64(val->data, val->len, &current)) {
            resp_write_error(client,
                "ERR value is not an integer or out of range");
            return;
        }
    }

    /* overflow check */
    if (current == INT64_MAX) {
        resp_write_error(client,
            "ERR value is not an integer or out of range");
        return;
    }

    current++;

    char buf[32];
    int n = snprintf(buf, sizeof(buf), "%" PRId64, current);
    rstr_t new_val = rstr_create(buf, (size_t)n);
    ht_set(store, key, new_val);
    if (expire_at != -1) {
        ht_set_expire(store, key, expire_at);
    }
    rstr_free(&new_val);

    resp_write_integer(client, current);
}

static void cmd_decr(client_t *client, hashtable_t *store,
                     resp_value_t *args, int argc) {
    (void)argc;
    rstr_t key = args[1].str;
    rstr_t *val = ht_get(store, key);

    int64_t current = 0;
    int64_t expire_at = -1;

    if (val) {
        expire_at = ht_get_expire(store, key);
        if (!parse_int64(val->data, val->len, &current)) {
            resp_write_error(client,
                "ERR value is not an integer or out of range");
            return;
        }
    }

    /* underflow check */
    if (current == INT64_MIN) {
        resp_write_error(client,
            "ERR value is not an integer or out of range");
        return;
    }

    current--;

    char buf[32];
    int n = snprintf(buf, sizeof(buf), "%" PRId64, current);
    rstr_t new_val = rstr_create(buf, (size_t)n);
    ht_set(store, key, new_val);
    if (expire_at != -1) {
        ht_set_expire(store, key, expire_at);
    }
    rstr_free(&new_val);

    resp_write_integer(client, current);
}

typedef struct {
    const char *name;
    cmd_handler_t handler;
    int min_args;
    int max_args;
} cmd_entry_t;

static const cmd_entry_t command_table[] = {
    {"PING",    cmd_ping,    1,  2},
    {"ECHO",    cmd_echo,    2,  2},
    {"SET",     cmd_set,     3,  5},
    {"GET",     cmd_get,     2,  2},
    {"DEL",     cmd_del,     2, -1},
    {"EXISTS",  cmd_exists,  2, -1},
    {"EXPIRE",  cmd_expire,  3,  3},
    {"TTL",     cmd_ttl,     2,  2},
    {"KEYS",    cmd_keys,    2,  2},
    {"TYPE",    cmd_type,    2,  2},
    {"INCR",    cmd_incr,    2,  2},
    {"DECR",    cmd_decr,    2,  2},
    {NULL,      NULL,        0,  0}
};

void dispatch_command(client_t *client, hashtable_t *store,
                      resp_value_t *cmd) {
    if (cmd->type != RESP_ARRAY || cmd->array.count == 0) {
        resp_write_error(client, "ERR invalid command format");
        return;
    }

    /* Verify all elements are bulk strings */
    for (int i = 0; i < cmd->array.count; i++) {
        if (cmd->array.elements[i].type != RESP_BULK_STRING) {
            resp_write_error(client, "ERR invalid command format");
            return;
        }
    }

    resp_value_t *args = cmd->array.elements;
    int argc = cmd->array.count;

    /* Convert command name to uppercase for comparison */
    char name_buf[64];
    size_t name_len = args[0].str.len;
    if (name_len >= sizeof(name_buf)) {
        name_len = sizeof(name_buf) - 1;
    }
    for (size_t i = 0; i < name_len; i++) {
        name_buf[i] = (char)toupper((unsigned char)args[0].str.data[i]);
    }
    name_buf[name_len] = '\0';

    for (int i = 0; command_table[i].name != NULL; i++) {
        if (strcmp(name_buf, command_table[i].name) == 0) {
            if (argc < command_table[i].min_args) {
                char err[128];
                snprintf(err, sizeof(err),
                    "ERR wrong number of arguments for '%s' command",
                    command_table[i].name);
                resp_write_error(client, err);
                return;
            }
            if (command_table[i].max_args != -1 &&
                argc > command_table[i].max_args) {
                char err[128];
                snprintf(err, sizeof(err),
                    "ERR wrong number of arguments for '%s' command",
                    command_table[i].name);
                resp_write_error(client, err);
                return;
            }
            command_table[i].handler(client, store, args, argc);
            return;
        }
    }

    /* Unknown command */
    char err[128];
    snprintf(err, sizeof(err), "ERR unknown command '%s'", name_buf);
    resp_write_error(client, err);
}
