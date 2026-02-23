#include "test.h"
#include "resp.h"
#include "client.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <signal.h>
#include <sys/types.h>
#include <sys/wait.h>
#include <sys/socket.h>
#include <netinet/in.h>
#include <arpa/inet.h>
#include <errno.h>
#include <stdarg.h>

static pid_t server_pid = -1;
static int test_port = 0;

static void start_test_server(void) {
    test_port = 30000 + (getpid() % 10000);
    server_pid = fork();
    if (server_pid == 0) {
        char port_str[16];
        snprintf(port_str, sizeof(port_str), "%d", test_port);
        execl("./mini-redis", "mini-redis", "--port", port_str, NULL);
        perror("execl");
        _exit(1);
    }

    /* Wait for server to be ready */
    for (int attempt = 0; attempt < 50; attempt++) {
        usleep(50000); /* 50ms */
        int fd = socket(AF_INET, SOCK_STREAM, 0);
        if (fd < 0) continue;

        struct sockaddr_in addr;
        memset(&addr, 0, sizeof(addr));
        addr.sin_family = AF_INET;
        addr.sin_port = htons((uint16_t)test_port);
        inet_pton(AF_INET, "127.0.0.1", &addr.sin_addr);

        if (connect(fd, (struct sockaddr *)&addr, sizeof(addr)) == 0) {
            close(fd);
            return;
        }
        close(fd);
    }
    fprintf(stderr, "Failed to connect to test server\n");
}

static void stop_test_server(void) {
    if (server_pid > 0) {
        kill(server_pid, SIGTERM);
        waitpid(server_pid, NULL, 0);
        server_pid = -1;
    }
}

/* Persistent read buffer for handling pipelined responses */
static char read_bufs[16][8192];
static size_t read_lens[16];

static int fd_to_slot(int fd) {
    return fd % 16;
}

static void test_reset_buf(int fd) {
    read_lens[fd_to_slot(fd)] = 0;
}

static int test_connect(void) {
    int fd = socket(AF_INET, SOCK_STREAM, 0);
    if (fd < 0) return -1;

    struct sockaddr_in addr;
    memset(&addr, 0, sizeof(addr));
    addr.sin_family = AF_INET;
    addr.sin_port = htons((uint16_t)test_port);
    inet_pton(AF_INET, "127.0.0.1", &addr.sin_addr);

    if (connect(fd, (struct sockaddr *)&addr, sizeof(addr)) < 0) {
        close(fd);
        return -1;
    }
    test_reset_buf(fd);
    return fd;
}

static void test_send(int fd, const char *data, size_t len) {
    size_t sent = 0;
    while (sent < len) {
        ssize_t n = send(fd, data + sent, len - sent, 0);
        if (n <= 0) break;
        sent += (size_t)n;
    }
}

static int test_read_response(int fd, resp_value_t *out) {
    int slot = fd_to_slot(fd);
    char *buf = read_bufs[slot];
    size_t *total = &read_lens[slot];

    while (*total < sizeof(read_bufs[0])) {
        /* Try to parse what we have first */
        if (*total > 0) {
            int r = resp_parse(buf, *total, out);
            if (r > 0) {
                /* Consume parsed bytes */
                size_t remaining = *total - (size_t)r;
                if (remaining > 0) {
                    memmove(buf, buf + r, remaining);
                }
                *total = remaining;
                return r;
            }
            if (r < 0) return -1;
        }

        /* Need more data */
        ssize_t n = recv(fd, buf + *total,
                         sizeof(read_bufs[0]) - *total, 0);
        if (n <= 0) return -1;
        *total += (size_t)n;
    }
    return -1;
}

static void test_send_command(int fd, int argc, ...) {
    va_list ap;
    va_start(ap, argc);

    char buf[4096];
    int off = snprintf(buf, sizeof(buf), "*%d\r\n", argc);

    for (int i = 0; i < argc; i++) {
        const char *arg = va_arg(ap, const char *);
        size_t len = strlen(arg);
        off += snprintf(buf + off, sizeof(buf) - (size_t)off,
                        "$%zu\r\n", len);
        memcpy(buf + off, arg, len);
        off += (int)len;
        buf[off++] = '\r';
        buf[off++] = '\n';
    }
    va_end(ap);

    test_send(fd, buf, (size_t)off);
}

/* ---- Integration tests ---- */

static int test_int_ping(void) {
    int fd = test_connect();
    ASSERT_TRUE(fd >= 0);

    test_send_command(fd, 1, "PING");

    resp_value_t val;
    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    ASSERT_EQ_INT(val.type, RESP_SIMPLE_STRING);
    ASSERT_EQ_STR(val.str.data, "PONG");
    resp_value_free(&val);

    close(fd);
    return 0;
}

static int test_int_ping_with_msg(void) {
    int fd = test_connect();
    ASSERT_TRUE(fd >= 0);

    test_send_command(fd, 2, "PING", "hello");

    resp_value_t val;
    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    ASSERT_EQ_INT(val.type, RESP_BULK_STRING);
    ASSERT_EQ_STR(val.str.data, "hello");
    resp_value_free(&val);

    close(fd);
    return 0;
}

static int test_int_echo(void) {
    int fd = test_connect();
    ASSERT_TRUE(fd >= 0);

    test_send_command(fd, 2, "ECHO", "test");

    resp_value_t val;
    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    ASSERT_EQ_INT(val.type, RESP_BULK_STRING);
    ASSERT_EQ_STR(val.str.data, "test");
    resp_value_free(&val);

    close(fd);
    return 0;
}

static int test_int_set_get(void) {
    int fd = test_connect();
    ASSERT_TRUE(fd >= 0);

    test_send_command(fd, 3, "SET", "foo", "bar");
    resp_value_t val;
    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    ASSERT_EQ_INT(val.type, RESP_SIMPLE_STRING);
    ASSERT_EQ_STR(val.str.data, "OK");
    resp_value_free(&val);

    test_send_command(fd, 2, "GET", "foo");
    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    ASSERT_EQ_INT(val.type, RESP_BULK_STRING);
    ASSERT_EQ_STR(val.str.data, "bar");
    resp_value_free(&val);

    close(fd);
    return 0;
}

static int test_int_get_nonexistent(void) {
    int fd = test_connect();
    ASSERT_TRUE(fd >= 0);

    test_send_command(fd, 2, "GET", "nonexistent_key_xyz");
    resp_value_t val;
    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    ASSERT_EQ_INT(val.type, RESP_NULL_BULK_STRING);

    close(fd);
    return 0;
}

static int test_int_set_overwrite(void) {
    int fd = test_connect();
    ASSERT_TRUE(fd >= 0);

    test_send_command(fd, 3, "SET", "ow_key", "v1");
    resp_value_t val;
    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    resp_value_free(&val);

    test_send_command(fd, 3, "SET", "ow_key", "v2");
    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    resp_value_free(&val);

    test_send_command(fd, 2, "GET", "ow_key");
    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    ASSERT_EQ_INT(val.type, RESP_BULK_STRING);
    ASSERT_EQ_STR(val.str.data, "v2");
    resp_value_free(&val);

    close(fd);
    return 0;
}

static int test_int_del(void) {
    int fd = test_connect();
    ASSERT_TRUE(fd >= 0);

    test_send_command(fd, 3, "SET", "del_key", "val");
    resp_value_t val;
    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    resp_value_free(&val);

    test_send_command(fd, 2, "DEL", "del_key");
    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    ASSERT_EQ_INT(val.type, RESP_INTEGER);
    ASSERT_EQ_INT(val.integer, 1);

    test_send_command(fd, 2, "GET", "del_key");
    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    ASSERT_EQ_INT(val.type, RESP_NULL_BULK_STRING);

    close(fd);
    return 0;
}

static int test_int_del_multi(void) {
    int fd = test_connect();
    ASSERT_TRUE(fd >= 0);

    resp_value_t val;
    test_send_command(fd, 3, "SET", "dm1", "v");
    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    resp_value_free(&val);

    test_send_command(fd, 3, "SET", "dm2", "v");
    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    resp_value_free(&val);

    /* DEL dm1 dm2 dm_nonexistent => should return 2 */
    test_send_command(fd, 4, "DEL", "dm1", "dm2", "dm_nonexistent");
    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    ASSERT_EQ_INT(val.type, RESP_INTEGER);
    ASSERT_EQ_INT(val.integer, 2);

    close(fd);
    return 0;
}

static int test_int_exists(void) {
    int fd = test_connect();
    ASSERT_TRUE(fd >= 0);

    resp_value_t val;
    test_send_command(fd, 3, "SET", "ex1", "v");
    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    resp_value_free(&val);

    test_send_command(fd, 3, "SET", "ex2", "v");
    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    resp_value_free(&val);

    test_send_command(fd, 4, "EXISTS", "ex1", "ex2", "ex_nope");
    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    ASSERT_EQ_INT(val.type, RESP_INTEGER);
    ASSERT_EQ_INT(val.integer, 2);

    close(fd);
    return 0;
}

static int test_int_expire_ttl(void) {
    int fd = test_connect();
    ASSERT_TRUE(fd >= 0);

    resp_value_t val;
    test_send_command(fd, 3, "SET", "ttlkey", "v");
    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    resp_value_free(&val);

    test_send_command(fd, 3, "EXPIRE", "ttlkey", "10");
    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    ASSERT_EQ_INT(val.type, RESP_INTEGER);
    ASSERT_EQ_INT(val.integer, 1);

    test_send_command(fd, 2, "TTL", "ttlkey");
    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    ASSERT_EQ_INT(val.type, RESP_INTEGER);
    ASSERT_TRUE(val.integer >= 9 && val.integer <= 10);

    close(fd);
    return 0;
}

static int test_int_set_ex(void) {
    int fd = test_connect();
    ASSERT_TRUE(fd >= 0);

    resp_value_t val;
    test_send_command(fd, 5, "SET", "exkey", "val", "EX", "1");
    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    ASSERT_EQ_INT(val.type, RESP_SIMPLE_STRING);
    ASSERT_EQ_STR(val.str.data, "OK");
    resp_value_free(&val);

    /* Verify it exists now */
    test_send_command(fd, 2, "GET", "exkey");
    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    ASSERT_EQ_INT(val.type, RESP_BULK_STRING);
    ASSERT_EQ_STR(val.str.data, "val");
    resp_value_free(&val);

    /* Wait for expiry */
    usleep(1500000); /* 1.5 seconds */

    test_send_command(fd, 2, "GET", "exkey");
    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    ASSERT_EQ_INT(val.type, RESP_NULL_BULK_STRING);

    close(fd);
    return 0;
}

static int test_int_ttl_no_expiry(void) {
    int fd = test_connect();
    ASSERT_TRUE(fd >= 0);

    resp_value_t val;
    test_send_command(fd, 3, "SET", "noexpkey", "v");
    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    resp_value_free(&val);

    test_send_command(fd, 2, "TTL", "noexpkey");
    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    ASSERT_EQ_INT(val.type, RESP_INTEGER);
    ASSERT_EQ_INT(val.integer, -1);

    close(fd);
    return 0;
}

static int test_int_ttl_no_key(void) {
    int fd = test_connect();
    ASSERT_TRUE(fd >= 0);

    resp_value_t val;
    test_send_command(fd, 2, "TTL", "totally_missing_key");
    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    ASSERT_EQ_INT(val.type, RESP_INTEGER);
    ASSERT_EQ_INT(val.integer, -2);

    close(fd);
    return 0;
}

static int test_int_keys_pattern(void) {
    int fd = test_connect();
    ASSERT_TRUE(fd >= 0);

    resp_value_t val;
    test_send_command(fd, 3, "SET", "user:100", "a");
    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    resp_value_free(&val);

    test_send_command(fd, 3, "SET", "user:200", "b");
    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    resp_value_free(&val);

    test_send_command(fd, 3, "SET", "item:1", "c");
    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    resp_value_free(&val);

    test_send_command(fd, 2, "KEYS", "user:*");
    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    ASSERT_EQ_INT(val.type, RESP_ARRAY);
    ASSERT_EQ_INT(val.array.count, 2);
    resp_value_free(&val);

    close(fd);
    return 0;
}

static int test_int_type(void) {
    int fd = test_connect();
    ASSERT_TRUE(fd >= 0);

    resp_value_t val;
    test_send_command(fd, 3, "SET", "typekey", "v");
    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    resp_value_free(&val);

    test_send_command(fd, 2, "TYPE", "typekey");
    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    ASSERT_EQ_INT(val.type, RESP_SIMPLE_STRING);
    ASSERT_EQ_STR(val.str.data, "string");
    resp_value_free(&val);

    test_send_command(fd, 2, "TYPE", "missing_typekey");
    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    ASSERT_EQ_INT(val.type, RESP_SIMPLE_STRING);
    ASSERT_EQ_STR(val.str.data, "none");
    resp_value_free(&val);

    close(fd);
    return 0;
}

static int test_int_incr_new(void) {
    int fd = test_connect();
    ASSERT_TRUE(fd >= 0);

    resp_value_t val;
    test_send_command(fd, 2, "INCR", "incr_new_key");
    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    ASSERT_EQ_INT(val.type, RESP_INTEGER);
    ASSERT_EQ_INT(val.integer, 1);

    close(fd);
    return 0;
}

static int test_int_incr_existing(void) {
    int fd = test_connect();
    ASSERT_TRUE(fd >= 0);

    resp_value_t val;
    test_send_command(fd, 3, "SET", "incr_ex_key", "10");
    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    resp_value_free(&val);

    test_send_command(fd, 2, "INCR", "incr_ex_key");
    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    ASSERT_EQ_INT(val.type, RESP_INTEGER);
    ASSERT_EQ_INT(val.integer, 11);

    close(fd);
    return 0;
}

static int test_int_incr_non_integer(void) {
    int fd = test_connect();
    ASSERT_TRUE(fd >= 0);

    resp_value_t val;
    test_send_command(fd, 3, "SET", "incr_str_key", "abc");
    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    resp_value_free(&val);

    test_send_command(fd, 2, "INCR", "incr_str_key");
    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    ASSERT_EQ_INT(val.type, RESP_ERROR);
    resp_value_free(&val);

    close(fd);
    return 0;
}

static int test_int_decr(void) {
    int fd = test_connect();
    ASSERT_TRUE(fd >= 0);

    resp_value_t val;
    test_send_command(fd, 3, "SET", "decr_key", "10");
    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    resp_value_free(&val);

    test_send_command(fd, 2, "DECR", "decr_key");
    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    ASSERT_EQ_INT(val.type, RESP_INTEGER);
    ASSERT_EQ_INT(val.integer, 9);

    close(fd);
    return 0;
}

static int test_int_pipeline(void) {
    int fd = test_connect();
    ASSERT_TRUE(fd >= 0);

    /* Send 3 commands at once */
    char buf[512];
    int off = 0;
    /* PING */
    off += snprintf(buf + off, sizeof(buf) - (size_t)off,
                    "*1\r\n$4\r\nPING\r\n");
    /* SET pipeline_k v */
    off += snprintf(buf + off, sizeof(buf) - (size_t)off,
                    "*3\r\n$3\r\nSET\r\n$10\r\npipeline_k\r\n$1\r\nv\r\n");
    /* GET pipeline_k */
    off += snprintf(buf + off, sizeof(buf) - (size_t)off,
                    "*2\r\n$3\r\nGET\r\n$10\r\npipeline_k\r\n");
    test_send(fd, buf, (size_t)off);

    /* Read 3 responses */
    resp_value_t val;

    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    ASSERT_EQ_INT(val.type, RESP_SIMPLE_STRING);
    ASSERT_EQ_STR(val.str.data, "PONG");
    resp_value_free(&val);

    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    ASSERT_EQ_INT(val.type, RESP_SIMPLE_STRING);
    ASSERT_EQ_STR(val.str.data, "OK");
    resp_value_free(&val);

    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    ASSERT_EQ_INT(val.type, RESP_BULK_STRING);
    ASSERT_EQ_STR(val.str.data, "v");
    resp_value_free(&val);

    close(fd);
    return 0;
}

static int test_int_concurrent(void) {
    int fd1 = test_connect();
    int fd2 = test_connect();
    int fd3 = test_connect();
    ASSERT_TRUE(fd1 >= 0);
    ASSERT_TRUE(fd2 >= 0);
    ASSERT_TRUE(fd3 >= 0);

    resp_value_t val;

    /* Each client sets its own key */
    test_send_command(fd1, 3, "SET", "cc1", "v1");
    test_send_command(fd2, 3, "SET", "cc2", "v2");
    test_send_command(fd3, 3, "SET", "cc3", "v3");

    ASSERT_TRUE(test_read_response(fd1, &val) > 0);
    resp_value_free(&val);
    ASSERT_TRUE(test_read_response(fd2, &val) > 0);
    resp_value_free(&val);
    ASSERT_TRUE(test_read_response(fd3, &val) > 0);
    resp_value_free(&val);

    /* Each client gets its own key */
    test_send_command(fd1, 2, "GET", "cc1");
    test_send_command(fd2, 2, "GET", "cc2");
    test_send_command(fd3, 2, "GET", "cc3");

    ASSERT_TRUE(test_read_response(fd1, &val) > 0);
    ASSERT_EQ_STR(val.str.data, "v1");
    resp_value_free(&val);

    ASSERT_TRUE(test_read_response(fd2, &val) > 0);
    ASSERT_EQ_STR(val.str.data, "v2");
    resp_value_free(&val);

    ASSERT_TRUE(test_read_response(fd3, &val) > 0);
    ASSERT_EQ_STR(val.str.data, "v3");
    resp_value_free(&val);

    close(fd1);
    close(fd2);
    close(fd3);
    return 0;
}

static int test_int_unknown_cmd(void) {
    int fd = test_connect();
    ASSERT_TRUE(fd >= 0);

    test_send_command(fd, 1, "FOOBAR");
    resp_value_t val;
    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    ASSERT_EQ_INT(val.type, RESP_ERROR);
    resp_value_free(&val);

    close(fd);
    return 0;
}

static int test_int_wrong_argc(void) {
    int fd = test_connect();
    ASSERT_TRUE(fd >= 0);

    /* GET with no args */
    test_send_command(fd, 1, "GET");
    resp_value_t val;
    ASSERT_TRUE(test_read_response(fd, &val) > 0);
    ASSERT_EQ_INT(val.type, RESP_ERROR);
    resp_value_free(&val);

    close(fd);
    return 0;
}

test_case_t integration_tests[] = {
    {"test_int_ping",           test_int_ping},
    {"test_int_ping_with_msg",  test_int_ping_with_msg},
    {"test_int_echo",           test_int_echo},
    {"test_int_set_get",        test_int_set_get},
    {"test_int_get_nonexistent", test_int_get_nonexistent},
    {"test_int_set_overwrite",  test_int_set_overwrite},
    {"test_int_del",            test_int_del},
    {"test_int_del_multi",      test_int_del_multi},
    {"test_int_exists",         test_int_exists},
    {"test_int_expire_ttl",     test_int_expire_ttl},
    {"test_int_set_ex",         test_int_set_ex},
    {"test_int_ttl_no_expiry",  test_int_ttl_no_expiry},
    {"test_int_ttl_no_key",     test_int_ttl_no_key},
    {"test_int_keys_pattern",   test_int_keys_pattern},
    {"test_int_type",           test_int_type},
    {"test_int_incr_new",       test_int_incr_new},
    {"test_int_incr_existing",  test_int_incr_existing},
    {"test_int_incr_non_integer", test_int_incr_non_integer},
    {"test_int_decr",           test_int_decr},
    {"test_int_pipeline",       test_int_pipeline},
    {"test_int_concurrent",     test_int_concurrent},
    {"test_int_unknown_cmd",    test_int_unknown_cmd},
    {"test_int_wrong_argc",     test_int_wrong_argc},
};
int integration_test_count = sizeof(integration_tests) / sizeof(integration_tests[0]);

int run_integration_tests(void) {
    start_test_server();
    int failed = run_test_suite("Integration Tests",
                                integration_tests, integration_test_count);
    stop_test_server();
    return failed;
}
