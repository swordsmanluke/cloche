#include "rstr.h"
#include <stdlib.h>
#include <string.h>
#include <stdio.h>

rstr_t rstr_create(const char *data, size_t len) {
    rstr_t s;
    s.len = len;
    s.data = malloc(len + 1);
    if (!s.data) {
        perror("malloc");
        exit(1);
    }
    if (len > 0 && data) {
        memcpy(s.data, data, len);
    }
    s.data[len] = '\0';
    return s;
}

rstr_t rstr_dup(rstr_t s) {
    return rstr_create(s.data, s.len);
}

void rstr_free(rstr_t *s) {
    if (s) {
        free(s->data);
        s->data = NULL;
        s->len = 0;
    }
}

bool rstr_eq(rstr_t a, rstr_t b) {
    if (a.len != b.len) {
        return false;
    }
    if (a.len == 0) {
        return true;
    }
    return memcmp(a.data, b.data, a.len) == 0;
}
