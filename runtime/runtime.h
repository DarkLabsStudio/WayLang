// WayLang runtime public interface.
//
// Generated code and the runtime agree on these declarations. All managed
// values are pointers to garbage-collected payloads; strings are
// { int64 length; char bytes[] } payloads, and slices/boxes are plain structs.
#ifndef WAY_RUNTIME_H
#define WAY_RUNTIME_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

// Runtime type information. `name` points at a WayLang string object.
typedef struct WayTypeInfo {
  const void *name;
  int64_t kind;
  int64_t nslots;
  void **methods;
} WayTypeInfo;

// An `any` / interface value.
typedef struct {
  void *data;
  WayTypeInfo *ti;
} WayAny;

// A slice value: { data, len, cap }. Matches the compiler's %way.slice.
typedef struct {
  void *data;
  int64_t len;
  int64_t cap;
} WaySlice;

// Dynamic type tags, shared with the compiler backend.
enum {
  WAY_KIND_INT = 1,
  WAY_KIND_FLOAT = 2,
  WAY_KIND_BOOL = 3,
  WAY_KIND_STRING = 4,
  WAY_KIND_STRUCT = 5,
  WAY_KIND_SLICE = 6,
  WAY_KIND_MAP = 7,
  WAY_KIND_ERROR = 8,
  WAY_KIND_FUNC = 9,
  WAY_KIND_IFACE = 10
};

// --- memory and GC --------------------------------------------------------

void *way_alloc(int64_t payload_size, int64_t kind, WayTypeInfo *ti);
void *way_box_new(int64_t size, WayTypeInfo *ti);
void *way_seq_alloc(int64_t count, int64_t elem_size);
void *way_box_assert(void *data, WayTypeInfo *actual, WayTypeInfo *expected);
void way_gc_safepoint(void);
void way_gc_collect(void);
void way_register_global(void *addr);
void way_rt_init(int argc, char **argv);
int64_t way_gc_collection_count(void);
int64_t way_gc_live_bytes(void);
void way_fatal(const char *fmt, ...);

// --- strings --------------------------------------------------------------

void *way_str_from_cstr(const char *text);
void *way_str_from_bytes(const char *bytes, int64_t len);
int64_t way_str_len(void *s);
void *way_str_concat(void *a, void *b);
int64_t way_str_cmp(void *a, void *b);
bool way_str_eq(void *a, void *b);
int8_t way_str_index(void *s, int64_t i);
void *way_str_substr(void *s, int64_t low, int64_t high);
int64_t way_str_rune_len(void *s);
int32_t way_str_rune_at(void *s, int64_t rune_index);
int64_t way_str_to_int(void *s);
double way_str_to_float(void *s);
bool way_str_to_bool(void *s);
int8_t way_str_to_byte(void *s);
int32_t way_str_to_rune(void *s);
void *way_str_from_int(int64_t v);
void *way_str_from_float(double v);
void *way_str_from_bool(bool v);
void *way_str_from_rune(int32_t v);
void *way_strings_upper(void *s);
void *way_strings_lower(void *s);
void *way_strings_trim(void *s);
bool way_strings_contains(void *s, void *sub);
bool way_strings_has_prefix(void *s, void *pre);
bool way_strings_has_suffix(void *s, void *suf);
int64_t way_strings_index(void *s, void *sub);
void *way_strings_replace(void *s, void *old, void *new_s);
void *way_strings_repeat(void *s, int64_t n);

// --- dynamic values -------------------------------------------------------

void *way_error_new(void *msg);
void *way_any_to_string(WayAny v);
void *way_any_type_name(WayAny v);
int64_t way_any_as_int(WayAny v);
double way_any_as_float(WayAny v);
bool way_any_as_bool(WayAny v);
WayAny way_any_add(WayAny a, WayAny b);
WayAny way_any_sub(WayAny a, WayAny b);
WayAny way_any_mul(WayAny a, WayAny b);
WayAny way_any_div(WayAny a, WayAny b);
WayAny way_any_mod(WayAny a, WayAny b);
WayAny way_any_neg(WayAny a);
bool way_any_eq(WayAny a, WayAny b);
bool way_any_ne(WayAny a, WayAny b);
bool way_any_lt(WayAny a, WayAny b);
bool way_any_le(WayAny a, WayAny b);
bool way_any_gt(WayAny a, WayAny b);
bool way_any_ge(WayAny a, WayAny b);

// --- collections ----------------------------------------------------------

typedef struct WayMap WayMap;
WayMap *way_map_new(int64_t ksize, int64_t vsize,
                    uint64_t (*hash)(const void *), bool (*eq)(const void *, const void *));
int64_t way_map_len(WayMap *m);
int64_t way_map_cap(WayMap *m);
void way_map_set(WayMap *m, const void *key, const void *val);
bool way_map_get_into(WayMap *m, const void *key, void *out);
bool way_map_entry(WayMap *m, int64_t i, void *keyOut, void *valOut);
uint64_t way_hash_int(const void *p);
bool way_eq_int(const void *a, const void *b);
uint64_t way_hash_bool(const void *p);
bool way_eq_bool(const void *a, const void *b);
uint64_t way_hash_str(const void *p);
bool way_eq_str(const void *a, const void *b);

// --- builtins -------------------------------------------------------------

void way_bounds_check(int64_t idx, int64_t len);
void way_print_str(void *s);
void way_print_int(int64_t v);
void way_print_float(double v);
void way_print_bool(bool v);
void way_print_any(WayAny v);
int64_t way_type_id(WayAny v);
void way_os_exit(int64_t code);
void *way_os_getenv(void *name);
WaySlice way_os_args(void);
int way_rt_argc(void);
char **way_rt_argv(void);
int64_t way_time_now(void);
void way_time_sleep(int64_t ms);
double way_math_sqrt(double v);
double way_math_pow(double a, double b);
double way_math_floor(double v);
double way_math_ceil(double v);
double way_math_round(double v);
double way_math_abs(double v);
int64_t way_math_min(int64_t a, int64_t b);
int64_t way_math_max(int64_t a, int64_t b);

#endif
