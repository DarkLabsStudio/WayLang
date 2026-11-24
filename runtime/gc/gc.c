// WayLang garbage collector.
//
// A precise-safepoint, conservative-tracing mark-sweep collector:
//   * the compiler calls way_gc_safepoint() at statement boundaries, where
//     every live reference is stored in a stack slot, a global, or a field of
//     a reachable heap object;
//   * roots are the native stack, the saved registers, and registered globals;
//   * heap payloads are scanned conservatively, so aggregates, slices and maps
//     keep their referents alive without per-type stack maps.
//
// Unreachable objects are freed; cycles are collected. Objects are never moved,
// so interior pointers stay valid.
#include <setjmp.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include "../internal.h"

extern char *way_rt_stack_base(void);
extern void way_mem_collected(void);

static WayObj **mark_stack = NULL;
static int64_t mark_count = 0;
static int64_t mark_cap = 0;

static void push_mark(WayObj *o) {
  if (mark_count == mark_cap) {
    int64_t cap = mark_cap == 0 ? 256 : mark_cap * 2;
    WayObj **grown = (WayObj **)realloc(mark_stack, (size_t)cap * sizeof(WayObj *));
    if (!grown) {
      way_fatal("out of memory in the garbage collector");
    }
    mark_stack = grown;
    mark_cap = cap;
  }
  mark_stack[mark_count++] = o;
}

void way_gc_mark_object(WayObj *o) {
  if (o == NULL || o->mark) {
    return;
  }
  o->mark = 1;
  push_mark(o);
}

// scan_words conservatively marks any heap pointer found in [start, end).
static void scan_words(const char *start, const char *end) {
  const uintptr_t mask = sizeof(void *) - 1;
  uintptr_t p = ((uintptr_t)start + mask) & ~mask;
  uintptr_t limit = (uintptr_t)end;
  for (; p + sizeof(void *) <= limit; p += sizeof(void *)) {
    void *candidate = *(void **)p;
    if (candidate == NULL) {
      continue;
    }
    WayObj *o = way_mem_find(candidate);
    if (o) {
      way_gc_mark_object(o);
    }
  }
}

// string_payload returns true for payloads that hold no pointers.
static bool string_payload(WayObj *o) {
  if (o->kind == WAY_KIND_STRING || o->kind == WAY_KIND_ERROR) {
    return true;
  }
  if (o->ti && (o->ti->kind == WAY_KIND_STRING || o->ti->kind == WAY_KIND_ERROR)) {
    return true;
  }
  return false;
}

static void mark_children(WayObj *o) {
  if (string_payload(o)) {
    return;
  }
  char *payload = (char *)way_payload_of(o);
  scan_words(payload, payload + o->size);
}

// way_gc_mark_roots scans the native stack and the saved register set.
void way_gc_mark_roots(void) {
  jmp_buf regs;
  setjmp(regs);
  scan_words((const char *)&regs, (const char *)&regs + sizeof(regs));

  char here = 0;
  char *top = &here;
  char *base = way_rt_stack_base();
  if (base && base > top) {
    scan_words(top, base);
  } else if (base && base < top) {
    scan_words(base, top);
  }
  way_gc_mark_globals();
}

void way_gc_collect(void) {
  way_mem_collected();
  way_gc_mark_roots();
  while (mark_count > 0) {
    WayObj *o = mark_stack[--mark_count];
    mark_children(o);
  }
  way_mem_sweep();
}
