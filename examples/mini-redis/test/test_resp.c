#include "test.h"
#include "resp.h"
#include "client.h"
#include <stdlib.h>
#include <string.h>

static int test_parse_simple_string(void) {
    resp_value_t val;
    const char *input = "+OK\r\n";
    int r = resp_parse(input, strlen(input), &val);
    ASSERT_TRUE(r > 0);
    ASSERT_EQ_INT(val.type, RESP_SIMPLE_STRING);
    ASSERT_EQ_STR(val.str.data, "OK");
    ASSERT_EQ_INT(val.str.len, 2);
    resp_value_free(&val);
    return 0;
}

static int test_parse_error(void) {
    resp_value_t val;
    const char *input = "-ERR msg\r\n";
    int r = resp_parse(input, strlen(input), &val);
    ASSERT_TRUE(r > 0);
    ASSERT_EQ_INT(val.type, RESP_ERROR);
    ASSERT_EQ_STR(val.str.data, "ERR msg");
    resp_value_free(&val);
    return 0;
}

static int test_parse_integer(void) {
    resp_value_t val;
    const char *input = ":42\r\n";
    int r = resp_parse(input, strlen(input), &val);
    ASSERT_TRUE(r > 0);
    ASSERT_EQ_INT(val.type, RESP_INTEGER);
    ASSERT_EQ_INT(val.integer, 42);
    return 0;
}

static int test_parse_negative_integer(void) {
    resp_value_t val;
    const char *input = ":-1\r\n";
    int r = resp_parse(input, strlen(input), &val);
    ASSERT_TRUE(r > 0);
    ASSERT_EQ_INT(val.type, RESP_INTEGER);
    ASSERT_EQ_INT(val.integer, -1);
    return 0;
}

static int test_parse_bulk_string(void) {
    resp_value_t val;
    const char *input = "$5\r\nHello\r\n";
    int r = resp_parse(input, strlen(input), &val);
    ASSERT_TRUE(r > 0);
    ASSERT_EQ_INT(val.type, RESP_BULK_STRING);
    ASSERT_EQ_INT(val.str.len, 5);
    ASSERT_EQ_STR(val.str.data, "Hello");
    resp_value_free(&val);
    return 0;
}

static int test_parse_empty_bulk_string(void) {
    resp_value_t val;
    const char *input = "$0\r\n\r\n";
    int r = resp_parse(input, strlen(input), &val);
    ASSERT_TRUE(r > 0);
    ASSERT_EQ_INT(val.type, RESP_BULK_STRING);
    ASSERT_EQ_INT(val.str.len, 0);
    resp_value_free(&val);
    return 0;
}

static int test_parse_null_bulk_string(void) {
    resp_value_t val;
    const char *input = "$-1\r\n";
    int r = resp_parse(input, strlen(input), &val);
    ASSERT_TRUE(r > 0);
    ASSERT_EQ_INT(val.type, RESP_NULL_BULK_STRING);
    return 0;
}

static int test_parse_array(void) {
    resp_value_t val;
    const char *input = "*2\r\n$3\r\nfoo\r\n$3\r\nbar\r\n";
    int r = resp_parse(input, strlen(input), &val);
    ASSERT_TRUE(r > 0);
    ASSERT_EQ_INT(val.type, RESP_ARRAY);
    ASSERT_EQ_INT(val.array.count, 2);
    ASSERT_EQ_INT(val.array.elements[0].type, RESP_BULK_STRING);
    ASSERT_EQ_STR(val.array.elements[0].str.data, "foo");
    ASSERT_EQ_STR(val.array.elements[1].str.data, "bar");
    resp_value_free(&val);
    return 0;
}

static int test_parse_empty_array(void) {
    resp_value_t val;
    const char *input = "*0\r\n";
    int r = resp_parse(input, strlen(input), &val);
    ASSERT_TRUE(r > 0);
    ASSERT_EQ_INT(val.type, RESP_ARRAY);
    ASSERT_EQ_INT(val.array.count, 0);
    resp_value_free(&val);
    return 0;
}

static int test_parse_nested_array(void) {
    resp_value_t val;
    const char *input = "*2\r\n*1\r\n$3\r\nfoo\r\n$3\r\nbar\r\n";
    int r = resp_parse(input, strlen(input), &val);
    ASSERT_TRUE(r > 0);
    ASSERT_EQ_INT(val.type, RESP_ARRAY);
    ASSERT_EQ_INT(val.array.count, 2);
    ASSERT_EQ_INT(val.array.elements[0].type, RESP_ARRAY);
    ASSERT_EQ_INT(val.array.elements[0].array.count, 1);
    ASSERT_EQ_STR(val.array.elements[0].array.elements[0].str.data, "foo");
    ASSERT_EQ_INT(val.array.elements[1].type, RESP_BULK_STRING);
    ASSERT_EQ_STR(val.array.elements[1].str.data, "bar");
    resp_value_free(&val);
    return 0;
}

static int test_parse_incomplete_simple(void) {
    resp_value_t val;
    const char *input = "+OK";
    int r = resp_parse(input, strlen(input), &val);
    ASSERT_EQ_INT(r, 0);
    return 0;
}

static int test_parse_incomplete_bulk(void) {
    resp_value_t val;
    const char *input = "$5\r\nHel";
    int r = resp_parse(input, strlen(input), &val);
    ASSERT_EQ_INT(r, 0);
    return 0;
}

static int test_parse_incomplete_array(void) {
    resp_value_t val;
    const char *input = "*2\r\n$3\r\nfoo\r\n";
    int r = resp_parse(input, strlen(input), &val);
    ASSERT_EQ_INT(r, 0);
    return 0;
}

static int test_parse_malformed_prefix(void) {
    resp_value_t val;
    const char *input = "!garbage\r\n";
    int r = resp_parse(input, strlen(input), &val);
    ASSERT_EQ_INT(r, -1);
    return 0;
}

static int test_parse_bulk_binary_data(void) {
    resp_value_t val;
    /* Bulk string with embedded null byte */
    const char input[] = "$5\r\nHe\0lo\r\n";
    int r = resp_parse(input, sizeof(input) - 1, &val);
    ASSERT_TRUE(r > 0);
    ASSERT_EQ_INT(val.type, RESP_BULK_STRING);
    ASSERT_EQ_INT(val.str.len, 5);
    ASSERT_TRUE(memcmp(val.str.data, "He\0lo", 5) == 0);
    resp_value_free(&val);
    return 0;
}

test_case_t resp_tests[] = {
    {"test_parse_simple_string",     test_parse_simple_string},
    {"test_parse_error",             test_parse_error},
    {"test_parse_integer",           test_parse_integer},
    {"test_parse_negative_integer",  test_parse_negative_integer},
    {"test_parse_bulk_string",       test_parse_bulk_string},
    {"test_parse_empty_bulk_string", test_parse_empty_bulk_string},
    {"test_parse_null_bulk_string",  test_parse_null_bulk_string},
    {"test_parse_array",             test_parse_array},
    {"test_parse_empty_array",       test_parse_empty_array},
    {"test_parse_nested_array",      test_parse_nested_array},
    {"test_parse_incomplete_simple", test_parse_incomplete_simple},
    {"test_parse_incomplete_bulk",   test_parse_incomplete_bulk},
    {"test_parse_incomplete_array",  test_parse_incomplete_array},
    {"test_parse_malformed_prefix",  test_parse_malformed_prefix},
    {"test_parse_bulk_binary_data",  test_parse_bulk_binary_data},
};
int resp_test_count = sizeof(resp_tests) / sizeof(resp_tests[0]);
