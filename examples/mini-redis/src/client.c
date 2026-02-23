#include "client.h"
#include <stdlib.h>
#include <string.h>
#include <stdio.h>
#include <unistd.h>

#define MIN_BUF_SIZE 1024

void client_init(client_t *c) {
    c->fd = -1;
    c->read_buf = NULL;
    c->read_len = 0;
    c->read_cap = 0;
    c->write_buf = NULL;
    c->write_len = 0;
    c->write_cap = 0;
}

void client_close(client_t *c) {
    if (c->fd >= 0) {
        close(c->fd);
    }
    free(c->read_buf);
    free(c->write_buf);
    client_init(c);
}

void client_write_append(client_t *c, const char *data, size_t len) {
    if (len == 0) {
        return;
    }

    size_t needed = c->write_len + len;
    if (needed > c->write_cap) {
        size_t new_cap = c->write_cap * 2;
        if (new_cap < needed) {
            new_cap = needed;
        }
        if (new_cap < MIN_BUF_SIZE) {
            new_cap = MIN_BUF_SIZE;
        }
        char *new_buf = realloc(c->write_buf, new_cap);
        if (!new_buf) {
            perror("realloc");
            exit(1);
        }
        c->write_buf = new_buf;
        c->write_cap = new_cap;
    }

    memcpy(c->write_buf + c->write_len, data, len);
    c->write_len += len;
}
