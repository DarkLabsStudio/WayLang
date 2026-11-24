// Internal runtime definitions shared by the memory manager, collector and
// dynamic value helpers.
#ifndef WAY_INTERNAL_H
#define WAY_INTERNAL_H

#include "runtime.h"

#define WAY_OBJ_MAGIC 0x5741594F424A3031ULL
#define WAY_HDR_SIZE 32

typedef struct WayObj {
  uint64_t magic;
  uint32_t mark;
  uint32_t kind;
  int64_t size;          // payload bytes
  WayTypeInfo *ti;
} WayObj;

// Payload pointer <-> header.
static inline WayObj *way_obj_of(void *payload) {
  return (WayObj *)((char *)payload - WAY_HDR_SIZE);
}
static inline void *way_payload_of(WayObj *o) {
  return (char *)o + WAY_HDR_SIZE;
}

// WayLang strings are { int64 length; char bytes[]; } payloads.
typedef struct {
  int64_t len;
  char bytes[];
} WayStr;

static inline WayStr *way_str(void *p) { return (WayStr *)p; }
static inline char *way_str_bytes(void *p) { return ((WayStr *)p)->bytes; }

// Memory manager bookkeeping, used by the collector.
int64_t way_mem_count(void);
WayObj *way_mem_at(int64_t i);
WayObj *way_mem_find(void *payload);
void way_mem_sweep(void);
int64_t way_mem_live_bytes(void);
void way_mem_note_threshold(void);

// Collector entry points.
void way_gc_mark_roots(void);
void way_gc_mark_object(WayObj *o);

// Global roots registered by generated code.
void way_gc_mark_globals(void);

#endif
