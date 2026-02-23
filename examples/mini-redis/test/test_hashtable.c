#include "test.h"
#include "hashtable.h"
#include "rstr.h"
#include <stdlib.h>
#include <string.h>

static int test_ht_insert_and_get(void) {
    hashtable_t *ht = ht_create();
    rstr_t key = rstr_create("hello", 5);
    rstr_t val = rstr_create("world", 5);

    ht_set(ht, key, val);
    rstr_t *got = ht_get(ht, key);
    ASSERT_NOT_NULL(got);
    ASSERT_EQ_RSTR(*got, val);

    rstr_free(&key);
    rstr_free(&val);
    ht_destroy(ht);
    return 0;
}

static int test_ht_overwrite(void) {
    hashtable_t *ht = ht_create();
    rstr_t key = rstr_create("key", 3);
    rstr_t val1 = rstr_create("val1", 4);
    rstr_t val2 = rstr_create("val2", 4);

    ht_set(ht, key, val1);
    ht_set(ht, key, val2);
    rstr_t *got = ht_get(ht, key);
    ASSERT_NOT_NULL(got);
    ASSERT_EQ_RSTR(*got, val2);
    ASSERT_EQ_INT(ht_count(ht), 1);

    rstr_free(&key);
    rstr_free(&val1);
    rstr_free(&val2);
    ht_destroy(ht);
    return 0;
}

static int test_ht_delete(void) {
    hashtable_t *ht = ht_create();
    rstr_t key = rstr_create("key", 3);
    rstr_t val = rstr_create("val", 3);

    ht_set(ht, key, val);
    ASSERT_TRUE(ht_delete(ht, key));
    ASSERT_NULL(ht_get(ht, key));
    ASSERT_EQ_INT(ht_count(ht), 0);

    rstr_free(&key);
    rstr_free(&val);
    ht_destroy(ht);
    return 0;
}

static int test_ht_delete_nonexistent(void) {
    hashtable_t *ht = ht_create();
    rstr_t key = rstr_create("nope", 4);

    ASSERT_FALSE(ht_delete(ht, key));

    rstr_free(&key);
    ht_destroy(ht);
    return 0;
}

static int test_ht_get_nonexistent(void) {
    hashtable_t *ht = ht_create();
    rstr_t key = rstr_create("nope", 4);

    ASSERT_NULL(ht_get(ht, key));

    rstr_free(&key);
    ht_destroy(ht);
    return 0;
}

static int test_ht_resize(void) {
    hashtable_t *ht = ht_create();
    /* Initial capacity is 64, load factor 0.7 => resize at >44 */
    char buf[32];
    for (int i = 0; i < 50; i++) {
        int n = snprintf(buf, sizeof(buf), "key%d", i);
        rstr_t key = rstr_create(buf, (size_t)n);
        rstr_t val = rstr_create(buf, (size_t)n);
        ht_set(ht, key, val);
        rstr_free(&key);
        rstr_free(&val);
    }

    ASSERT_EQ_INT(ht_count(ht), 50);
    ASSERT_TRUE(ht->capacity > 64);

    /* Verify all keys still accessible */
    for (int i = 0; i < 50; i++) {
        int n = snprintf(buf, sizeof(buf), "key%d", i);
        rstr_t key = rstr_create(buf, (size_t)n);
        ASSERT_NOT_NULL(ht_get(ht, key));
        rstr_free(&key);
    }

    ht_destroy(ht);
    return 0;
}

static int test_ht_many_keys(void) {
    hashtable_t *ht = ht_create();
    char buf[32];
    for (int i = 0; i < 1000; i++) {
        int n = snprintf(buf, sizeof(buf), "k%d", i);
        rstr_t key = rstr_create(buf, (size_t)n);
        n = snprintf(buf, sizeof(buf), "v%d", i);
        rstr_t val = rstr_create(buf, (size_t)n);
        ht_set(ht, key, val);
        rstr_free(&key);
        rstr_free(&val);
    }

    ASSERT_EQ_INT(ht_count(ht), 1000);

    for (int i = 0; i < 1000; i++) {
        int n = snprintf(buf, sizeof(buf), "k%d", i);
        rstr_t key = rstr_create(buf, (size_t)n);
        rstr_t *got = ht_get(ht, key);
        ASSERT_NOT_NULL(got);

        n = snprintf(buf, sizeof(buf), "v%d", i);
        rstr_t expected = rstr_create(buf, (size_t)n);
        ASSERT_EQ_RSTR(*got, expected);
        rstr_free(&key);
        rstr_free(&expected);
    }

    ht_destroy(ht);
    return 0;
}

static int test_ht_iterator(void) {
    hashtable_t *ht = ht_create();
    int n_keys = 20;
    char buf[32];
    for (int i = 0; i < n_keys; i++) {
        int n = snprintf(buf, sizeof(buf), "iter%d", i);
        rstr_t key = rstr_create(buf, (size_t)n);
        rstr_t val = rstr_create(buf, (size_t)n);
        ht_set(ht, key, val);
        rstr_free(&key);
        rstr_free(&val);
    }

    ht_iter_t iter;
    rstr_t key, value;
    int count = 0;
    ht_iter_init(ht, &iter);
    while (ht_iter_next(&iter, &key, &value)) {
        count++;
    }
    ASSERT_EQ_INT(count, n_keys);

    ht_destroy(ht);
    return 0;
}

static int test_ht_iterator_with_tombstones(void) {
    hashtable_t *ht = ht_create();
    char buf[32];
    for (int i = 0; i < 10; i++) {
        int n = snprintf(buf, sizeof(buf), "ts%d", i);
        rstr_t key = rstr_create(buf, (size_t)n);
        rstr_t val = rstr_create(buf, (size_t)n);
        ht_set(ht, key, val);
        rstr_free(&key);
        rstr_free(&val);
    }

    /* Delete half */
    for (int i = 0; i < 5; i++) {
        int n = snprintf(buf, sizeof(buf), "ts%d", i);
        rstr_t key = rstr_create(buf, (size_t)n);
        ht_delete(ht, key);
        rstr_free(&key);
    }

    ht_iter_t iter;
    rstr_t key;
    int count = 0;
    ht_iter_init(ht, &iter);
    while (ht_iter_next(&iter, &key, NULL)) {
        count++;
    }
    ASSERT_EQ_INT(count, 5);

    ht_destroy(ht);
    return 0;
}

static int test_ht_binary_keys(void) {
    hashtable_t *ht = ht_create();
    char key_data[] = "ab\0cd";
    rstr_t key = rstr_create(key_data, 5);
    rstr_t val = rstr_create("value", 5);

    ht_set(ht, key, val);
    rstr_t *got = ht_get(ht, key);
    ASSERT_NOT_NULL(got);
    ASSERT_EQ_RSTR(*got, val);

    /* Different key with same prefix but different after null */
    char key2_data[] = "ab\0ce";
    rstr_t key2 = rstr_create(key2_data, 5);
    ASSERT_NULL(ht_get(ht, key2));

    rstr_free(&key);
    rstr_free(&key2);
    rstr_free(&val);
    ht_destroy(ht);
    return 0;
}

test_case_t hashtable_tests[] = {
    {"test_ht_insert_and_get",          test_ht_insert_and_get},
    {"test_ht_overwrite",               test_ht_overwrite},
    {"test_ht_delete",                  test_ht_delete},
    {"test_ht_delete_nonexistent",      test_ht_delete_nonexistent},
    {"test_ht_get_nonexistent",         test_ht_get_nonexistent},
    {"test_ht_resize",                  test_ht_resize},
    {"test_ht_many_keys",               test_ht_many_keys},
    {"test_ht_iterator",                test_ht_iterator},
    {"test_ht_iterator_with_tombstones", test_ht_iterator_with_tombstones},
    {"test_ht_binary_keys",             test_ht_binary_keys},
};
int hashtable_test_count = sizeof(hashtable_tests) / sizeof(hashtable_tests[0]);
