#include "test.h"
#include "hashtable.h"
#include "util.h"
#include <unistd.h>

static int test_ttl_set_and_check(void) {
    hashtable_t *ht = ht_create();
    rstr_t key = rstr_create("k", 1);
    rstr_t val = rstr_create("v", 1);

    ht_set(ht, key, val);
    int64_t expire_at = current_time_ms() + 2000;
    ht_set_expire(ht, key, expire_at);

    int64_t got = ht_get_expire(ht, key);
    ASSERT_EQ_INT(got, expire_at);

    rstr_free(&key);
    rstr_free(&val);
    ht_destroy(ht);
    return 0;
}

static int test_ttl_not_expired_yet(void) {
    hashtable_t *ht = ht_create();
    rstr_t key = rstr_create("k", 1);
    rstr_t val = rstr_create("v", 1);

    ht_set(ht, key, val);
    ht_set_expire(ht, key, current_time_ms() + 10000);

    ASSERT_NOT_NULL(ht_get(ht, key));

    rstr_free(&key);
    rstr_free(&val);
    ht_destroy(ht);
    return 0;
}

static int test_ttl_expired(void) {
    hashtable_t *ht = ht_create();
    rstr_t key = rstr_create("k", 1);
    rstr_t val = rstr_create("v", 1);

    ht_set(ht, key, val);
    ht_set_expire(ht, key, current_time_ms() + 1);
    usleep(10000); /* 10ms, well past 1ms expiry */

    ASSERT_NULL(ht_get(ht, key));

    rstr_free(&key);
    rstr_free(&val);
    ht_destroy(ht);
    return 0;
}

static int test_ttl_delete_removes_expiry(void) {
    hashtable_t *ht = ht_create();
    rstr_t key = rstr_create("k", 1);
    rstr_t val = rstr_create("v", 1);

    ht_set(ht, key, val);
    ht_set_expire(ht, key, current_time_ms() + 10000);
    ht_delete(ht, key);

    ASSERT_NULL(ht_get(ht, key));
    ASSERT_FALSE(ht_exists(ht, key));

    rstr_free(&key);
    rstr_free(&val);
    ht_destroy(ht);
    return 0;
}

static int test_ttl_overwrite_resets(void) {
    hashtable_t *ht = ht_create();
    rstr_t key = rstr_create("k", 1);
    rstr_t val1 = rstr_create("v1", 2);
    rstr_t val2 = rstr_create("v2", 2);

    ht_set(ht, key, val1);
    ht_set_expire(ht, key, current_time_ms() + 10000);

    /* Overwrite resets TTL */
    ht_set(ht, key, val2);
    ASSERT_EQ_INT(ht_get_expire(ht, key), -1);

    rstr_free(&key);
    rstr_free(&val1);
    rstr_free(&val2);
    ht_destroy(ht);
    return 0;
}

static int test_ttl_expire_makes_tombstone(void) {
    hashtable_t *ht = ht_create();
    rstr_t key = rstr_create("k", 1);
    rstr_t val = rstr_create("v", 1);

    ht_set(ht, key, val);
    ASSERT_EQ_INT(ht_count(ht), 1);

    ht_set_expire(ht, key, current_time_ms() + 1);
    usleep(10000);

    /* Access triggers lazy expiration */
    ASSERT_NULL(ht_get(ht, key));
    ASSERT_EQ_INT(ht_count(ht), 0);

    rstr_free(&key);
    rstr_free(&val);
    ht_destroy(ht);
    return 0;
}

static int test_ttl_no_expiry_by_default(void) {
    hashtable_t *ht = ht_create();
    rstr_t key = rstr_create("k", 1);
    rstr_t val = rstr_create("v", 1);

    ht_set(ht, key, val);
    ASSERT_EQ_INT(ht_get_expire(ht, key), -1);

    rstr_free(&key);
    rstr_free(&val);
    ht_destroy(ht);
    return 0;
}

test_case_t ttl_tests[] = {
    {"test_ttl_set_and_check",       test_ttl_set_and_check},
    {"test_ttl_not_expired_yet",     test_ttl_not_expired_yet},
    {"test_ttl_expired",             test_ttl_expired},
    {"test_ttl_delete_removes_expiry", test_ttl_delete_removes_expiry},
    {"test_ttl_overwrite_resets",    test_ttl_overwrite_resets},
    {"test_ttl_expire_makes_tombstone", test_ttl_expire_makes_tombstone},
    {"test_ttl_no_expiry_by_default", test_ttl_no_expiry_by_default},
};
int ttl_test_count = sizeof(ttl_tests) / sizeof(ttl_tests[0]);
