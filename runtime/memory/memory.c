// WayLang memory manager.
//
// Objects are individually malloc'd with a 32-byte header and tracked in a
// growable table plus a pointer set used by the collector and by pointer
// validation. Allocation never collects: collection happens at explicit
// safepoints emitted by the compiler at statement boundaries, where all live
// references are reachable from stack slots or globals.
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "../internal.h"

static WayObj **objects = NULL;
static int64_t object_count = 0;
static int64_t object_cap = 0;

static void **set_slots = NULL;
static int64_t set_cap = 0;
static int64_t set_count = 0;

static int64_t heap_bytes = 0;
static int64_t next_gc = 8 * 1024 * 1024;
static int gc_pending = 0;

static char *stack_base = NULL;
static int saved_argc = 0;
static char **saved_argv = NULL;

static void **global_roots = NULL;
static int64_t global_count = 0;
static int64_t global_cap = 0;

static int64_t collection_count = 0;

// --- pointer set ----------------------------------------------------------

static uint64_t ptr_hash(void *p) {
  uint64_t x = (uint64_t)(uintptr_t)p;
  x ^= x >> 33;
  x *= 0xff51afd7ed558ccdULL;
  x ^= x >> 33;
  x *= 0xc4ceb9fe1a85ec53ULL;
  x ^= x >> 33;
  return x;
}

static void set_insert(void *p);

static void set_grow(int64_t new_cap) {
  void **old = set_slots;
  int64_t old_cap = set_cap;
  set_slots = (void **)calloc((size_t)new_cap, sizeof(void *));
  if (!set_slots) {
    way_fatal("out of memory growing the GC table");
  }
  set_cap = new_cap;
  set_count = 0;
  for (int64_t i = 0; i < old_cap; i++) {
    if (old && old[i]) {
      set_insert(old[i]);
    }
  }
  free(old);
}

static void set_insert(void *p) {
  if (set_cap == 0 || (set_count + 1) * 2 >= set_cap) {
    set_grow(set_cap == 0 ? 1024 : set_cap * 2);
  }
  uint64_t mask = (uint64_t)set_cap - 1;
  uint64_t i = ptr_hash(p) & mask;
  while (set_slots[i]) {
    if (set_slots[i] == p) {
      return;
    }
    i = (i + 1) & mask;
  }
  set_slots[i] = p;
  set_count++;
}

static void set_clear(void) {
  if (set_slots) {
    memset(set_slots, 0, (size_t)set_cap * sizeof(void *));
  }
  set_count = 0;
}

static int set_contains(void *p) {
  if (set_cap == 0 || p == NULL) {
    return 0;
  }
  uint64_t mask = (uint64_t)set_cap - 1;
  uint64_t i = ptr_hash(p) & mask;
  for (;;) {
    if (!set_slots[i]) {
      return 0;
    }
    if (set_slots[i] == p) {
      return 1;
    }
    i = (i + 1) & mask;
  }
}

static void track(WayObj *o) {
  if (object_count == object_cap) {
    int64_t cap = object_cap == 0 ? 1024 : object_cap * 2;
    WayObj **grown = (WayObj **)realloc(objects, (size_t)cap * sizeof(WayObj *));
    if (!grown) {
      way_fatal("out of memory growing the GC object table");
    }
    objects = grown;
    object_cap = cap;
  }
  objects[object_count++] = o;
  set_insert(way_payload_of(o));
}

// --- allocation -----------------------------------------------------------

void *way_alloc(int64_t payload_size, int64_t kind, WayTypeInfo *ti) {
  if (payload_size < 0) {
    way_fatal("negative allocation size");
  }
  WayObj *o = (WayObj *)malloc((size_t)(WAY_HDR_SIZE + payload_size));
  if (!o) {
    way_fatal("out of memory allocating %lld bytes", (long long)payload_size);
  }
  o->magic = WAY_OBJ_MAGIC;
  o->mark = 0;
  o->kind = (uint32_t)kind;
  o->size = payload_size;
  o->ti = ti;
  void *payload = way_payload_of(o);
  memset(payload, 0, (size_t)payload_size);
  track(o);
  heap_bytes += payload_size;
  if (heap_bytes > next_gc) {
    gc_pending = 1;
  }
  return payload;
}

void *way_box_new(int64_t size, WayTypeInfo *ti) {
  int kind = ti ? (int)ti->kind : 0;
  return way_alloc(size, kind, ti);
}

void *way_seq_alloc(int64_t count, int64_t elem_size) {
  if (count <= 0) {
    return NULL;
  }
  void *p = way_alloc(count * elem_size, WAY_KIND_SLICE, NULL);
  return p;
}

void *way_box_assert(void *data, WayTypeInfo *actual, WayTypeInfo *expected) {
  if (data == NULL) {
    way_fatal("cannot convert nil to a concrete type");
  }
  if (actual == NULL || (expected != NULL && actual != expected)) {
    way_fatal("dynamic type mismatch: the value has a different concrete type than requested");
  }
  return data;
}

// --- bookkeeping used by the collector ------------------------------------

int64_t way_mem_count(void) { return object_count; }

WayObj *way_mem_at(int64_t i) { return objects[i]; }

WayObj *way_mem_find(void *payload) {
  if (!set_contains(payload)) {
    return NULL;
  }
  WayObj *o = way_obj_of(payload);
  if (o->magic != WAY_OBJ_MAGIC) {
    return NULL;
  }
  return o;
}

void way_mem_sweep(void) {
  int64_t live_bytes = 0;
  int64_t kept = 0;
  for (int64_t i = 0; i < object_count; i++) {
    WayObj *o = objects[i];
    if (o->mark) {
      o->mark = 0;
      objects[kept++] = o;
      live_bytes += o->size;
    } else {
      o->magic = 0;
      free(o);
    }
  }
  object_count = kept;
  heap_bytes = live_bytes;
  next_gc = live_bytes * 2;
  if (next_gc < 1024 * 1024) {
    next_gc = 1024 * 1024;
  }
  set_clear();
  for (int64_t i = 0; i < object_count; i++) {
    set_insert(way_payload_of(objects[i]));
  }
  // Rebuild the set fresh so that dead pointers cannot linger.
  if (set_count * 4 < set_cap && set_cap > 4096) {
    int64_t cap = 1024;
    while (cap < set_count * 4) {
      cap *= 2;
    }
    set_grow(cap);
  }
}

int64_t way_mem_live_bytes(void) { return heap_bytes; }

void way_mem_note_threshold(void) { gc_pending = 1; }

int64_t way_gc_collection_count(void) { return collection_count; }

int64_t way_gc_live_bytes(void) { return heap_bytes; }

void way_mem_collected(void) { collection_count++; }

// --- roots ----------------------------------------------------------------

void way_register_global(void *addr) {
  if (global_count == global_cap) {
    int64_t cap = global_cap == 0 ? 16 : global_cap * 2;
    void **grown = (void **)realloc(global_roots, (size_t)cap * sizeof(void *));
    if (!grown) {
      way_fatal("out of memory registering globals");
    }
    global_roots = grown;
    global_cap = cap;
  }
  global_roots[global_count++] = addr;
}

void way_gc_mark_globals(void) {
  for (int64_t i = 0; i < global_count; i++) {
    void *candidate = *(void **)global_roots[i];
    WayObj *o = way_mem_find(candidate);
    if (o) {
      way_gc_mark_object(o);
    }
  }
}

void way_gc_safepoint(void) {
  if (gc_pending && heap_bytes > next_gc) {
    way_gc_collect();
  }
}

// --- startup --------------------------------------------------------------

void way_rt_init(int argc, char **argv) {
  char local = 0;
  stack_base = &local;
  saved_argc = argc;
  saved_argv = argv;
  const char *threshold = getenv("WAY_GC_THRESHOLD_BYTES");
  if (threshold && *threshold) {
    long long v = atoll(threshold);
    if (v > 1024) {
      next_gc = v;
    }
  }
}

int way_rt_argc(void) { return saved_argc; }
char **way_rt_argv(void) { return saved_argv; }
char *way_rt_stack_base(void) { return stack_base; }
