#ifndef RESP_H
#define RESP_H

#include "rstr.h"
#include "client.h"
#include <stdint.h>

typedef enum {
    RESP_SIMPLE_STRING,
    RESP_ERROR,
    RESP_INTEGER,
    RESP_BULK_STRING,
    RESP_NULL_BULK_STRING,
    RESP_ARRAY
} resp_type_t;

typedef struct resp_value {
    resp_type_t type;
    union {
        rstr_t str;             /* SIMPLE_STRING, ERROR, BULK_STRING */
        int64_t integer;        /* INTEGER */
        struct {
            struct resp_value *elements;
            int count;
        } array;
    };
} resp_value_t;

int resp_parse(const char *buf, size_t len, resp_value_t *out);
void resp_value_free(resp_value_t *val);

void resp_write_simple_string(client_t *c, const char *str);
void resp_write_error(client_t *c, const char *msg);
void resp_write_integer(client_t *c, int64_t val);
void resp_write_bulk_string(client_t *c, const char *data, size_t len);
void resp_write_null_bulk_string(client_t *c);
void resp_write_array_header(client_t *c, int count);

#endif
