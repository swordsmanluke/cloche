#include "server.h"
#include "client.h"
#include "resp.h"
#include "hashtable.h"
#include "commands.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <errno.h>
#include <signal.h>
#include <fcntl.h>
#include <poll.h>
#include <sys/socket.h>
#include <netinet/in.h>
#include <arpa/inet.h>

#define RECV_BUF_SIZE 4096
#define MIN_BUF_SIZE 1024

volatile sig_atomic_t g_shutdown = 0;

static void signal_handler(int sig) {
    (void)sig;
    g_shutdown = 1;
}

int start_server(int port) {
    int fd = socket(AF_INET, SOCK_STREAM, 0);
    if (fd < 0) {
        perror("socket");
        exit(1);
    }

    int opt = 1;
    if (setsockopt(fd, SOL_SOCKET, SO_REUSEADDR, &opt, sizeof(opt)) < 0) {
        perror("setsockopt");
        close(fd);
        exit(1);
    }

    /* Set non-blocking */
    int flags = fcntl(fd, F_GETFL, 0);
    if (flags < 0 || fcntl(fd, F_SETFL, flags | O_NONBLOCK) < 0) {
        perror("fcntl");
        close(fd);
        exit(1);
    }

    struct sockaddr_in addr;
    memset(&addr, 0, sizeof(addr));
    addr.sin_family = AF_INET;
    addr.sin_addr.s_addr = htonl(INADDR_ANY);
    addr.sin_port = htons((uint16_t)port);

    if (bind(fd, (struct sockaddr *)&addr, sizeof(addr)) < 0) {
        perror("bind");
        close(fd);
        exit(1);
    }

    if (listen(fd, 128) < 0) {
        perror("listen");
        close(fd);
        exit(1);
    }

    return fd;
}

static void accept_new_client(int listen_fd, client_t *clients) {
    while (1) {
        int client_fd = accept(listen_fd, NULL, NULL);
        if (client_fd < 0) {
            if (errno == EAGAIN || errno == EWOULDBLOCK) {
                break;
            }
            perror("accept");
            break;
        }

        /* Set client socket to non-blocking */
        int flags = fcntl(client_fd, F_GETFL, 0);
        if (flags >= 0) {
            fcntl(client_fd, F_SETFL, flags | O_NONBLOCK);
        }

        /* Find a free slot */
        int slot = -1;
        for (int i = 0; i < MAX_CLIENTS; i++) {
            if (clients[i].fd == -1) {
                slot = i;
                break;
            }
        }

        if (slot == -1) {
            /* No room */
            close(client_fd);
            continue;
        }

        client_init(&clients[slot]);
        clients[slot].fd = client_fd;
    }
}

static void handle_client_read(client_t *c, hashtable_t *store) {
    char tmp[RECV_BUF_SIZE];
    ssize_t n = recv(c->fd, tmp, sizeof(tmp), 0);

    if (n <= 0) {
        if (n == 0 || (errno != EAGAIN && errno != EWOULDBLOCK)) {
            client_close(c);
        }
        return;
    }

    /* Append to read buffer */
    size_t needed = c->read_len + (size_t)n;
    if (needed > c->read_cap) {
        size_t new_cap = c->read_cap * 2;
        if (new_cap < needed) {
            new_cap = needed;
        }
        if (new_cap < MIN_BUF_SIZE) {
            new_cap = MIN_BUF_SIZE;
        }
        char *new_buf = realloc(c->read_buf, new_cap);
        if (!new_buf) {
            perror("realloc");
            exit(1);
        }
        c->read_buf = new_buf;
        c->read_cap = new_cap;
    }
    memcpy(c->read_buf + c->read_len, tmp, (size_t)n);
    c->read_len += (size_t)n;

    /* Parse and execute loop */
    while (c->read_len > 0 && c->fd >= 0) {
        resp_value_t cmd;
        int consumed = resp_parse(c->read_buf, c->read_len, &cmd);

        if (consumed == 0) {
            break; /* need more data */
        }

        if (consumed < 0) {
            resp_write_error(c, "ERR Protocol error");
            client_close(c);
            return;
        }

        dispatch_command(c, store, &cmd);
        resp_value_free(&cmd);

        /* Remove consumed bytes */
        size_t remaining = c->read_len - (size_t)consumed;
        if (remaining > 0) {
            memmove(c->read_buf, c->read_buf + consumed, remaining);
        }
        c->read_len = remaining;
    }
}

static void handle_client_write(client_t *c) {
    if (c->write_len == 0) {
        return;
    }

    ssize_t n = send(c->fd, c->write_buf, c->write_len, 0);
    if (n < 0) {
        if (errno != EAGAIN && errno != EWOULDBLOCK) {
            client_close(c);
        }
        return;
    }

    size_t remaining = c->write_len - (size_t)n;
    if (remaining > 0) {
        memmove(c->write_buf, c->write_buf + n, remaining);
    }
    c->write_len = remaining;
}

int main(int argc, char *argv[]) {
    int port = 6379;

    /* Parse --port argument */
    for (int i = 1; i < argc; i++) {
        if (strcmp(argv[i], "--port") == 0 && i + 1 < argc) {
            port = atoi(argv[i + 1]);
            i++;
        }
    }

    /* Install signal handlers */
    struct sigaction sa;
    memset(&sa, 0, sizeof(sa));
    sa.sa_handler = signal_handler;
    sigemptyset(&sa.sa_mask);
    sigaction(SIGINT, &sa, NULL);
    sigaction(SIGTERM, &sa, NULL);

    int listen_fd = start_server(port);
    fprintf(stderr, "Mini-Redis server listening on port %d\n", port);

    hashtable_t *store = ht_create();

    client_t clients[MAX_CLIENTS];
    for (int i = 0; i < MAX_CLIENTS; i++) {
        client_init(&clients[i]);
    }

    struct pollfd fds[MAX_CLIENTS + 1];
    int poll_to_client[MAX_CLIENTS + 1];

    while (!g_shutdown) {
        /* Build pollfd array */
        int nfds = 0;

        fds[0].fd = listen_fd;
        fds[0].events = POLLIN;
        fds[0].revents = 0;
        poll_to_client[0] = -1;
        nfds = 1;

        for (int i = 0; i < MAX_CLIENTS; i++) {
            if (clients[i].fd < 0) {
                continue;
            }
            fds[nfds].fd = clients[i].fd;
            fds[nfds].events = POLLIN;
            if (clients[i].write_len > 0) {
                fds[nfds].events |= POLLOUT;
            }
            fds[nfds].revents = 0;
            poll_to_client[nfds] = i;
            nfds++;
        }

        int ready = poll(fds, (nfds_t)nfds, 1000);
        if (ready < 0) {
            if (errno == EINTR) {
                continue;
            }
            perror("poll");
            break;
        }

        /* Check listening socket */
        if (fds[0].revents & POLLIN) {
            accept_new_client(listen_fd, clients);
        }

        /* Handle client events */
        for (int i = 1; i < nfds; i++) {
            int ci = poll_to_client[i];

            if (fds[i].revents & (POLLERR | POLLHUP)) {
                client_close(&clients[ci]);
                continue;
            }

            if (fds[i].revents & POLLIN) {
                handle_client_read(&clients[ci], store);
                if (clients[ci].fd < 0) {
                    continue;
                }
            }

            if (fds[i].revents & POLLOUT) {
                handle_client_write(&clients[ci]);
            }
        }
    }

    /* Cleanup */
    for (int i = 0; i < MAX_CLIENTS; i++) {
        if (clients[i].fd >= 0) {
            client_close(&clients[i]);
        }
    }
    ht_destroy(store);
    close(listen_fd);

    return 0;
}
