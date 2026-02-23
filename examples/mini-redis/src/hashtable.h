#ifndef HASHTABLE_H
#define HASHTABLE_H

#include "rstr.h"
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

typedef enum {
    ENTRY_EMPTY,
    ENTRY_OCCUPIED,
    ENTRY_TOMBSTONE
} entry_state_t;

typedef struct {
    entry_state_t state;
    rstr_t key;
    rstr_t value;
    int64_t expire_at;  /* ms since epoch, -1 = no expiration */
} ht_entry_t;

typedef struct {
    ht_entry_t *entries;
    size_t capacity;
    size_t count;       /* OCCUPIED entries */
    size_t used;        /* OCCUPIED + TOMBSTONE */
} hashtable_t;

typedef struct {
    hashtable_t *ht;
    size_t index;
} ht_iter_t;

hashtable_t *ht_create(void);
void ht_destroy(hashtable_t *ht);
bool ht_set(hashtable_t *ht, rstr_t key, rstr_t value);
rstr_t *ht_get(hashtable_t *ht, rstr_t key);
bool ht_delete(hashtable_t *ht, rstr_t key);
bool ht_exists(hashtable_t *ht, rstr_t key);
void ht_set_expire(hashtable_t *ht, rstr_t key, int64_t expire_at_ms);
int64_t ht_get_expire(hashtable_t *ht, rstr_t key);
size_t ht_count(hashtable_t *ht);

void ht_iter_init(hashtable_t *ht, ht_iter_t *iter);
bool ht_iter_next(ht_iter_t *iter, rstr_t *key, rstr_t *value);

#endif
