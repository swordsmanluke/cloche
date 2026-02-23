#include "hashtable.h"
#include "util.h"
#include <stdlib.h>
#include <string.h>
#include <stdio.h>

#define HT_INITIAL_CAPACITY 64
#define HT_LOAD_FACTOR 0.7

static uint32_t fnv1a_hash(const char *data, size_t len) {
    uint32_t hash = 2166136261u;
    for (size_t i = 0; i < len; i++) {
        hash ^= (uint8_t)data[i];
        hash *= 16777619u;
    }
    return hash;
}

static bool is_expired(ht_entry_t *entry) {
    if (entry->expire_at == -1) {
        return false;
    }
    return current_time_ms() >= entry->expire_at;
}

static void entry_free(ht_entry_t *entry) {
    rstr_free(&entry->key);
    rstr_free(&entry->value);
    entry->state = ENTRY_TOMBSTONE;
    entry->expire_at = -1;
}

static size_t probe(hashtable_t *ht, rstr_t key, bool *found) {
    uint32_t hash = fnv1a_hash(key.data, key.len);
    size_t idx = hash & (ht->capacity - 1);
    size_t first_tombstone = (size_t)-1;

    for (size_t i = 0; i < ht->capacity; i++) {
        size_t slot = (idx + i) & (ht->capacity - 1);
        ht_entry_t *e = &ht->entries[slot];

        if (e->state == ENTRY_EMPTY) {
            *found = false;
            return (first_tombstone != (size_t)-1) ?
                   first_tombstone : slot;
        }

        if (e->state == ENTRY_TOMBSTONE) {
            if (first_tombstone == (size_t)-1) {
                first_tombstone = slot;
            }
            continue;
        }

        /* ENTRY_OCCUPIED */
        if (rstr_eq(e->key, key)) {
            if (is_expired(e)) {
                entry_free(e);
                ht->count--;
                /* don't decrement used: tombstone replaces occupied */
                *found = false;
                return (first_tombstone != (size_t)-1) ?
                       first_tombstone : slot;
            }
            *found = true;
            return slot;
        }
    }

    *found = false;
    return (first_tombstone != (size_t)-1) ? first_tombstone : 0;
}

static void ht_resize(hashtable_t *ht) {
    size_t new_cap = ht->capacity * 2;
    ht_entry_t *new_entries = calloc(new_cap, sizeof(ht_entry_t));
    if (!new_entries) {
        perror("calloc");
        exit(1);
    }

    for (size_t i = 0; i < new_cap; i++) {
        new_entries[i].expire_at = -1;
    }

    for (size_t i = 0; i < ht->capacity; i++) {
        ht_entry_t *e = &ht->entries[i];
        if (e->state != ENTRY_OCCUPIED) {
            continue;
        }

        uint32_t hash = fnv1a_hash(e->key.data, e->key.len);
        size_t idx = hash & (new_cap - 1);

        while (new_entries[idx].state == ENTRY_OCCUPIED) {
            idx = (idx + 1) & (new_cap - 1);
        }

        new_entries[idx] = *e;
    }

    free(ht->entries);
    ht->entries = new_entries;
    ht->capacity = new_cap;
    ht->used = ht->count; /* tombstones are gone */
}

hashtable_t *ht_create(void) {
    hashtable_t *ht = malloc(sizeof(hashtable_t));
    if (!ht) {
        perror("malloc");
        exit(1);
    }
    ht->capacity = HT_INITIAL_CAPACITY;
    ht->count = 0;
    ht->used = 0;
    ht->entries = calloc(ht->capacity, sizeof(ht_entry_t));
    if (!ht->entries) {
        perror("calloc");
        exit(1);
    }
    for (size_t i = 0; i < ht->capacity; i++) {
        ht->entries[i].expire_at = -1;
    }
    return ht;
}

void ht_destroy(hashtable_t *ht) {
    if (!ht) {
        return;
    }
    for (size_t i = 0; i < ht->capacity; i++) {
        if (ht->entries[i].state == ENTRY_OCCUPIED) {
            rstr_free(&ht->entries[i].key);
            rstr_free(&ht->entries[i].value);
        }
    }
    free(ht->entries);
    free(ht);
}

bool ht_set(hashtable_t *ht, rstr_t key, rstr_t value) {
    if ((double)ht->used >= (double)ht->capacity * HT_LOAD_FACTOR) {
        ht_resize(ht);
    }

    bool found;
    size_t slot = probe(ht, key, &found);

    if (found) {
        /* overwrite existing */
        rstr_free(&ht->entries[slot].key);
        rstr_free(&ht->entries[slot].value);
        ht->entries[slot].key = rstr_dup(key);
        ht->entries[slot].value = rstr_dup(value);
        ht->entries[slot].expire_at = -1;
        return false; /* not a new key */
    }

    /* inserting into empty or tombstone slot */
    bool was_empty = (ht->entries[slot].state == ENTRY_EMPTY);
    ht->entries[slot].state = ENTRY_OCCUPIED;
    ht->entries[slot].key = rstr_dup(key);
    ht->entries[slot].value = rstr_dup(value);
    ht->entries[slot].expire_at = -1;
    ht->count++;
    if (was_empty) {
        ht->used++;
    }
    return true; /* new key */
}

rstr_t *ht_get(hashtable_t *ht, rstr_t key) {
    bool found;
    size_t slot = probe(ht, key, &found);
    if (!found) {
        return NULL;
    }
    return &ht->entries[slot].value;
}

bool ht_delete(hashtable_t *ht, rstr_t key) {
    bool found;
    size_t slot = probe(ht, key, &found);
    if (!found) {
        return false;
    }
    entry_free(&ht->entries[slot]);
    ht->count--;
    /* used stays the same: tombstone replaces occupied */
    return true;
}

bool ht_exists(hashtable_t *ht, rstr_t key) {
    bool found;
    probe(ht, key, &found);
    return found;
}

void ht_set_expire(hashtable_t *ht, rstr_t key, int64_t expire_at_ms) {
    bool found;
    size_t slot = probe(ht, key, &found);
    if (found) {
        ht->entries[slot].expire_at = expire_at_ms;
    }
}

int64_t ht_get_expire(hashtable_t *ht, rstr_t key) {
    bool found;
    size_t slot = probe(ht, key, &found);
    if (!found) {
        return -1;
    }
    return ht->entries[slot].expire_at;
}

size_t ht_count(hashtable_t *ht) {
    return ht->count;
}

void ht_iter_init(hashtable_t *ht, ht_iter_t *iter) {
    iter->ht = ht;
    iter->index = 0;
}

bool ht_iter_next(ht_iter_t *iter, rstr_t *key, rstr_t *value) {
    while (iter->index < iter->ht->capacity) {
        ht_entry_t *e = &iter->ht->entries[iter->index];
        iter->index++;

        if (e->state != ENTRY_OCCUPIED) {
            continue;
        }

        /* Check expiration during iteration */
        if (is_expired(e)) {
            entry_free(e);
            iter->ht->count--;
            continue;
        }

        if (key) {
            *key = e->key;
        }
        if (value) {
            *value = e->value;
        }
        return true;
    }
    return false;
}
