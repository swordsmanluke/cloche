#ifndef RSTR_H
#define RSTR_H

#include <stdbool.h>
#include <stddef.h>

typedef struct {
    char *data;
    size_t len;
} rstr_t;

rstr_t rstr_create(const char *data, size_t len);
rstr_t rstr_dup(rstr_t s);
void rstr_free(rstr_t *s);
bool rstr_eq(rstr_t a, rstr_t b);

#endif
