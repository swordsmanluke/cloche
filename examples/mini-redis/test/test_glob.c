#include "test.h"
#include "glob.h"
#include <string.h>

static int test_glob_literal(void) {
    ASSERT_TRUE(glob_match("hello", 5, "hello", 5));
    ASSERT_FALSE(glob_match("hello", 5, "world", 5));
    return 0;
}

static int test_glob_star(void) {
    ASSERT_TRUE(glob_match("h*o", 3, "hello", 5));
    ASSERT_TRUE(glob_match("h*o", 3, "ho", 2));
    ASSERT_FALSE(glob_match("h*o", 3, "hex", 3));
    return 0;
}

static int test_glob_star_all(void) {
    ASSERT_TRUE(glob_match("*", 1, "anything", 8));
    ASSERT_TRUE(glob_match("*", 1, "", 0));
    ASSERT_TRUE(glob_match("*", 1, "a", 1));
    return 0;
}

static int test_glob_question(void) {
    ASSERT_TRUE(glob_match("h?llo", 5, "hello", 5));
    ASSERT_TRUE(glob_match("h?llo", 5, "hallo", 5));
    ASSERT_FALSE(glob_match("h?llo", 5, "hllo", 4));
    return 0;
}

static int test_glob_char_class(void) {
    ASSERT_TRUE(glob_match("h[ae]llo", 8, "hello", 5));
    ASSERT_TRUE(glob_match("h[ae]llo", 8, "hallo", 5));
    ASSERT_FALSE(glob_match("h[ae]llo", 8, "hillo", 5));
    return 0;
}

static int test_glob_negated_class(void) {
    ASSERT_TRUE(glob_match("h[!ae]llo", 9, "hillo", 5));
    ASSERT_FALSE(glob_match("h[!ae]llo", 9, "hello", 5));
    ASSERT_FALSE(glob_match("h[!ae]llo", 9, "hallo", 5));
    return 0;
}

static int test_glob_empty_pattern(void) {
    ASSERT_TRUE(glob_match("", 0, "", 0));
    ASSERT_FALSE(glob_match("", 0, "a", 1));
    return 0;
}

static int test_glob_empty_string(void) {
    ASSERT_TRUE(glob_match("*", 1, "", 0));
    ASSERT_FALSE(glob_match("?", 1, "", 0));
    return 0;
}

static int test_glob_consecutive_stars(void) {
    ASSERT_TRUE(glob_match("**", 2, "anything", 8));
    ASSERT_TRUE(glob_match("**", 2, "", 0));
    ASSERT_TRUE(glob_match("h**o", 4, "hello", 5));
    return 0;
}

static int test_glob_complex(void) {
    ASSERT_TRUE(glob_match("user:*:name", 11, "user:123:name", 13));
    ASSERT_FALSE(glob_match("user:*:name", 11, "user:123:age", 12));
    return 0;
}

static int test_glob_question_star(void) {
    ASSERT_TRUE(glob_match("?*", 2, "a", 1));
    ASSERT_TRUE(glob_match("?*", 2, "abc", 3));
    ASSERT_FALSE(glob_match("?*", 2, "", 0));
    return 0;
}

test_case_t glob_tests[] = {
    {"test_glob_literal",          test_glob_literal},
    {"test_glob_star",             test_glob_star},
    {"test_glob_star_all",         test_glob_star_all},
    {"test_glob_question",         test_glob_question},
    {"test_glob_char_class",       test_glob_char_class},
    {"test_glob_negated_class",    test_glob_negated_class},
    {"test_glob_empty_pattern",    test_glob_empty_pattern},
    {"test_glob_empty_string",     test_glob_empty_string},
    {"test_glob_consecutive_stars", test_glob_consecutive_stars},
    {"test_glob_complex",          test_glob_complex},
    {"test_glob_question_star",    test_glob_question_star},
};
int glob_test_count = sizeof(glob_tests) / sizeof(glob_tests[0]);
