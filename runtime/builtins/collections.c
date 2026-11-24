// WayLang maps.
//
// A map is a managed object (kind WAY_KIND_MAP) holding two GC-managed byte
// arrays for keys and values plus an occupancy table. Because the key and value
// arrays are heap objects, the conservative collector scans them and keeps any
// strings or aggregates they reference alive.
//
// Collisions use linear probing with tombstones so that deletion (not yet
// exposed in the language) would stay correct.
#include <stdlib.h>
#include <string.h>

#include "../internal.h"

struct WayMap {
  int64_t ksize;
  int64_t vsize;
  uint64_t (*hash)(const void *);
  bool (*eq)(const void *, const void *);
  int64_t count;
  int64_t cap;
  int64_t used;  // occupied or tombstoned slots
  unsigned char *state;  // 0 empty, 1 full, 2 tombstone
  void *keys;
  void *vals;
};

static void *array_alloc(int64_t bytes) {
  if (bytes <= 0) {
    return NULL;
  }
  return way_alloc(bytes, WAY_KIND_SLICE, NULL);
}

static void *slot_at(void *array, int64_t i) {
  return (char *)array + i;
}

static void map_grow(WayMap *m) {
  int64_t old_cap = m->cap;
  int64_t new_cap = old_cap == 0 ? 8 : old_cap * 2;
  unsigned char *old_state = m->state;
  void *old_keys = m->keys;
  void *old_vals = m->vals;

  m->state = (unsigned char *)array_alloc(new_cap);
  m->keys = array_alloc(new_cap * m->ksize);
  m->vals = array_alloc(new_cap * m->vsize);
  m->cap = new_cap;
  m->used = 0;
  m->count = 0;
  if (!m->state || !m->keys || !m->vals) {
    way_fatal("out of memory growing a map");
  }

  for (int64_t i = 0; i < old_cap; i++) {
    if (old_state[i] != 1) {
      continue;
    }
    const void *k = slot_at(old_keys, i * m->ksize);
    const void *v = slot_at(old_vals, i * m->vsize);
    uint64_t mask = (uint64_t)new_cap - 1;
    uint64_t j = m->hash(k) & mask;
    while (m->state[j] == 1) {
      j = (j + 1) & mask;
    }
    m->state[j] = 1;
    memcpy(slot_at(m->keys, (int64_t)j * m->ksize), k, (size_t)m->ksize);
    memcpy(slot_at(m->vals, (int64_t)j * m->vsize), v, (size_t)m->vsize);
    m->used++;
    m->count++;
  }
}

WayMap *way_map_new(int64_t ksize, int64_t vsize, uint64_t (*hash)(const void *),
                    bool (*eq)(const void *, const void *)) {
  if (ksize <= 0 || vsize <= 0 || hash == NULL || eq == NULL) {
    way_fatal("invalid map descriptor");
  }
  WayMap *m = (WayMap *)way_alloc((int64_t)sizeof(WayMap), WAY_KIND_MAP, NULL);
  m->ksize = ksize;
  m->vsize = vsize;
  m->hash = hash;
  m->eq = eq;
  m->count = 0;
  m->cap = 0;
  m->used = 0;
  m->state = NULL;
  m->keys = NULL;
  m->vals = NULL;
  return m;
}

int64_t way_map_len(WayMap *m) { return m == NULL ? 0 : m->count; }

int64_t way_map_cap(WayMap *m) { return m == NULL ? 0 : m->cap; }

void way_map_set(WayMap *m, const void *key, const void *val) {
  if (m == NULL) {
    way_fatal("assignment to a nil map");
  }
  if (m->cap == 0 || (m->used + 1) * 4 >= m->cap * 3) {
    map_grow(m);
  }
  uint64_t mask = (uint64_t)m->cap - 1;
  uint64_t i = m->hash(key) & mask;
  int64_t tomb = -1;
  for (;;) {
    unsigned char st = m->state[i];
    if (st == 0) {
      int64_t slot = tomb >= 0 ? tomb : (int64_t)i;
      m->state[slot] = 1;
      memcpy(slot_at(m->keys, slot * m->ksize), key, (size_t)m->ksize);
      memcpy(slot_at(m->vals, slot * m->vsize), val, (size_t)m->vsize);
      m->count++;
      if (tomb < 0) {
        m->used++;
      }
      return;
    }
    if (st == 2) {
      if (tomb < 0) {
        tomb = (int64_t)i;
      }
    } else if (m->eq(slot_at(m->keys, (int64_t)i * m->ksize), key)) {
      memcpy(slot_at(m->vals, (int64_t)i * m->vsize), val, (size_t)m->vsize);
      return;
    }
    i = (i + 1) & mask;
  }
}

bool way_map_get_into(WayMap *m, const void *key, void *out) {
  if (m == NULL || m->cap == 0) {
    return false;
  }
  uint64_t mask = (uint64_t)m->cap - 1;
  uint64_t i = m->hash(key) & mask;
  for (;;) {
    unsigned char st = m->state[i];
    if (st == 0) {
      return false;
    }
    if (st == 1 && m->eq(slot_at(m->keys, (int64_t)i * m->ksize), key)) {
      memcpy(out, slot_at(m->vals, (int64_t)i * m->vsize), (size_t)m->vsize);
      return true;
    }
    i = (i + 1) & mask;
  }
}

bool way_map_entry(WayMap *m, int64_t i, void *keyOut, void *valOut) {
  if (m == NULL || i < 0) {
    return false;
  }
  int64_t seen = 0;
  for (int64_t j = 0; j < m->cap; j++) {
    if (m->state[j] != 1) {
      continue;
    }
    if (seen == i) {
      if (keyOut != NULL) {
        memcpy(keyOut, slot_at(m->keys, j * m->ksize), (size_t)m->ksize);
      }
      if (valOut != NULL) {
        memcpy(valOut, slot_at(m->vals, j * m->vsize), (size_t)m->vsize);
      }
      return true;
    }
    seen++;
  }
  return false;
}

// --- hashing and equality -------------------------------------------------

static uint64_t mix64(uint64_t x) {
  x ^= x >> 33;
  x *= 0xff51afd7ed558ccdULL;
  x ^= x >> 33;
  x *= 0xc4ceb9fe1a85ec53ULL;
  x ^= x >> 33;
  return x;
}

uint64_t way_hash_int(const void *p) {
  int64_t v = 0;
  memcpy(&v, p, sizeof(v));
  return mix64((uint64_t)v);
}

bool way_eq_int(const void *a, const void *b) {
  int64_t x = 0;
  int64_t y = 0;
  memcpy(&x, a, sizeof(x));
  memcpy(&y, b, sizeof(y));
  return x == y;
}

uint64_t way_hash_bool(const void *p) {
  unsigned char v = 0;
  memcpy(&v, p, 1);
  return v ? 0x9e3779b97f4a7c15ULL : 0xc2b2ae3d27d4eb4fULL;
}

bool way_eq_bool(const void *a, const void *b) {
  unsigned char x = 0;
  unsigned char y = 0;
  memcpy(&x, a, 1);
  memcpy(&y, b, 1);
  return (x != 0) == (y != 0);
}

uint64_t way_hash_str(const void *p) {
  void *s = NULL;
  memcpy(&s, p, sizeof(s));
  const unsigned char *b = (const unsigned char *)way_str_bytes(s);
  int64_t len = way_str_len(s);
  uint64_t h = 1469598103934665603ULL;
  for (int64_t i = 0; i < len; i++) {
    h ^= b[i];
    h *= 1099511628211ULL;
  }
  return mix64(h);
}

bool way_eq_str(const void *a, const void *b) {
  void *x = NULL;
  void *y = NULL;
  memcpy(&x, a, sizeof(x));
  memcpy(&y, b, sizeof(y));
  return way_str_eq(x, y);
}
