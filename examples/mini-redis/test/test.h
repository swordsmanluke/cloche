#ifndef TEST_H
#define TEST_H

#include <stdio.h>
#include <string.h>
#include <inttypes.h>

#define ASSERT_TRUE(expr)  do { \
    if (!(expr)) { \
        printf("  FAIL: %s:%d: %s\n", __FILE__, __LINE__, #expr); \
        return 1; \
    } \
} while(0)

#define ASSERT_FALSE(expr)  ASSERT_TRUE(!(expr))

#define ASSERT_EQ_INT(a, b)  do { \
    int64_t _a = (int64_t)(a); \
    int64_t _b = (int64_t)(b); \
    if (_a != _b) { \
        printf("  FAIL: %s:%d: expected %" PRId64 ", got %" PRId64 "\n", \
               __FILE__, __LINE__, _b, _a); \
        return 1; \
    } \
} while(0)

#define ASSERT_EQ_STR(a, b)  do { \
    const char *_a = (a); \
    const char *_b = (b); \
    if (strcmp(_a, _b) != 0) { \
        printf("  FAIL: %s:%d: expected \"%s\", got \"%s\"\n", \
               __FILE__, __LINE__, _b, _a); \
        return 1; \
    } \
} while(0)

#define ASSERT_EQ_RSTR(a, b)  do { \
    rstr_t _a = (a); \
    rstr_t _b = (b); \
    if (_a.len != _b.len || memcmp(_a.data, _b.data, _a.len) != 0) { \
        printf("  FAIL: %s:%d: rstr mismatch\n", __FILE__, __LINE__); \
        return 1; \
    } \
} while(0)

#define ASSERT_NULL(ptr)  do { \
    if ((ptr) != NULL) { \
        printf("  FAIL: %s:%d: expected NULL\n", __FILE__, __LINE__); \
        return 1; \
    } \
} while(0)

#define ASSERT_NOT_NULL(ptr)  do { \
    if ((ptr) == NULL) { \
        printf("  FAIL: %s:%d: unexpected NULL\n", __FILE__, __LINE__); \
        return 1; \
    } \
} while(0)

typedef int (*test_fn)(void);

typedef struct {
    const char *name;
    test_fn fn;
} test_case_t;

static inline int run_test_suite(const char *suite_name,
                                 test_case_t *tests, int count) {
    int passed = 0, failed = 0;
    printf("=== %s ===\n", suite_name);
    for (int i = 0; i < count; i++) {
        int result = tests[i].fn();
        if (result == 0) {
            printf("  PASS: %s\n", tests[i].name);
            passed++;
        } else {
            printf("  FAIL: %s\n", tests[i].name);
            failed++;
        }
    }
    printf("  %d passed, %d failed\n\n", passed, failed);
    return failed;
}

#endif
