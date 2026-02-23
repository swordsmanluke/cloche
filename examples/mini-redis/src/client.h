#ifndef CLIENT_H
#define CLIENT_H

#include <stddef.h>

#define MAX_CLIENTS 1024

typedef struct {
    int fd;
    char *read_buf;
    size_t read_len;
    size_t read_cap;
    char *write_buf;
    size_t write_len;
    size_t write_cap;
} client_t;

void client_init(client_t *c);
void client_close(client_t *c);
void client_write_append(client_t *c, const char *data, size_t len);

#endif
