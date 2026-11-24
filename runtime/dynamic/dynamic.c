// WayLang dynamic (`any`) values.
//
// An `any` is a { data, typeinfo } box. Primitive operations on dynamic values
// dispatch on the boxed type kind at run time, which is what makes gradual
// typing work: static code is fully monomorphic, while `any` values carry their
// representation with them.
#include <string.h>

#include "../internal.h"

// Names must be WayLang strings so that type(v) can return them directly.
typedef struct {
  int64_t len;
  char bytes[16];
} WayName;

static const WayName name_int = {3, "int"};
static const WayName name_float = {5, "float"};
static const WayName name_bool = {4, "bool"};
static const WayName name_string = {6, "string"};
static const WayName name_nil = {3, "nil"};

static const WayTypeInfo ti_int = {&name_int, WAY_KIND_INT, 0, NULL};
static const WayTypeInfo ti_float = {&name_float, WAY_KIND_FLOAT, 0, NULL};
static const WayTypeInfo ti_bool = {&name_bool, WAY_KIND_BOOL, 0, NULL};
static const WayTypeInfo ti_string = {&name_string, WAY_KIND_STRING, 0, NULL};

static int kind_of(WayAny v) {
  if (v.data == NULL) {
    return 0;
  }
  return v.ti ? (int)v.ti->kind : 0;
}

static WayAny box_int(int64_t x) {
  WayAny v;
  v.data = way_alloc((int64_t)sizeof(int64_t), WAY_KIND_INT, NULL);
  *(int64_t *)v.data = x;
  v.ti = (WayTypeInfo *)&ti_int;
  return v;
}

static WayAny box_float(double x) {
  WayAny v;
  v.data = way_alloc((int64_t)sizeof(double), WAY_KIND_FLOAT, NULL);
  *(double *)v.data = x;
  v.ti = (WayTypeInfo *)&ti_float;
  return v;
}

static WayAny box_str(void *s) {
  WayAny v;
  v.data = s;
  v.ti = (WayTypeInfo *)&ti_string;
  return v;
}

// --- inspection -----------------------------------------------------------

void *way_any_type_name(WayAny v) {
  if (v.data == NULL) {
    return (void *)&name_nil;
  }
  if (v.ti != NULL && v.ti->name != NULL) {
    return (void *)v.ti->name;
  }
  return (void *)&name_nil;
}

int64_t way_type_id(WayAny v) { return kind_of(v); }

void *way_any_to_string(WayAny v) {
  switch (kind_of(v)) {
  case WAY_KIND_INT:
    return way_str_from_int(*(int64_t *)v.data);
  case WAY_KIND_FLOAT:
    return way_str_from_float(*(double *)v.data);
  case WAY_KIND_BOOL:
    return way_str_from_bool(*(bool *)v.data);
  case WAY_KIND_STRING:
  case WAY_KIND_ERROR:
    return v.data;
  default:
    return way_str_from_cstr("<nil>");
  }
}

void way_print_any(WayAny v) { way_print_str(way_any_to_string(v)); }

// --- unboxing -------------------------------------------------------------

static const char *kind_name(WayAny v) {
  switch (kind_of(v)) {
  case WAY_KIND_INT:
    return "int";
  case WAY_KIND_FLOAT:
    return "float";
  case WAY_KIND_BOOL:
    return "bool";
  case WAY_KIND_STRING:
    return "string";
  case WAY_KIND_ERROR:
    return "error";
  default:
    return "nil";
  }
}

int64_t way_any_as_int(WayAny v) {
  switch (kind_of(v)) {
  case WAY_KIND_INT:
    return *(int64_t *)v.data;
  case WAY_KIND_FLOAT:
    return (int64_t)*(double *)v.data;
  default:
    way_fatal("cannot convert %s to int", kind_name(v));
    return 0;
  }
}

double way_any_as_float(WayAny v) {
  switch (kind_of(v)) {
  case WAY_KIND_INT:
    return (double)*(int64_t *)v.data;
  case WAY_KIND_FLOAT:
    return *(double *)v.data;
  default:
    way_fatal("cannot convert %s to float", kind_name(v));
    return 0;
  }
}

bool way_any_as_bool(WayAny v) {
  switch (kind_of(v)) {
  case WAY_KIND_BOOL:
    return *(bool *)v.data;
  default:
    way_fatal("cannot convert %s to bool", kind_name(v));
    return false;
  }
}

// --- arithmetic -----------------------------------------------------------

static bool both_int(WayAny a, WayAny b) {
  return kind_of(a) == WAY_KIND_INT && kind_of(b) == WAY_KIND_INT;
}

static int64_t int_of(WayAny v) { return *(int64_t *)v.data; }

static double float_of(WayAny v) {
  if (kind_of(v) == WAY_KIND_INT) {
    return (double)*(int64_t *)v.data;
  }
  return *(double *)v.data;
}

enum { OP_ADD, OP_SUB, OP_MUL, OP_DIV, OP_MOD };

static WayAny num_op(int op, WayAny a, WayAny b) {
  int ka = kind_of(a);
  int kb = kind_of(b);
  if (op == OP_ADD && (ka == WAY_KIND_STRING || kb == WAY_KIND_STRING)) {
    return box_str(way_str_concat(way_any_to_string(a), way_any_to_string(b)));
  }
  if ((ka != WAY_KIND_INT && ka != WAY_KIND_FLOAT) ||
      (kb != WAY_KIND_INT && kb != WAY_KIND_FLOAT)) {
    way_fatal("arithmetic on unsupported dynamic types (%s, %s)", kind_name(a),
              kind_name(b));
  }
  if (both_int(a, b) && op != OP_DIV) {
    int64_t x = int_of(a);
    int64_t y = int_of(b);
    switch (op) {
    case OP_ADD:
      return box_int(x + y);
    case OP_SUB:
      return box_int(x - y);
    case OP_MUL:
      return box_int(x * y);
    default:
      if (y == 0) {
        way_fatal("integer division by zero");
      }
      return box_int(x % y);
    }
  }
  double x = float_of(a);
  double y = float_of(b);
  switch (op) {
  case OP_ADD:
    return box_float(x + y);
  case OP_SUB:
    return box_float(x - y);
  case OP_MUL:
    return box_float(x * y);
  default:
    if (y == 0.0) {
      way_fatal("division by zero");
    }
    return box_float(x / y);
  }
}

WayAny way_any_add(WayAny a, WayAny b) { return num_op(OP_ADD, a, b); }
WayAny way_any_sub(WayAny a, WayAny b) { return num_op(OP_SUB, a, b); }
WayAny way_any_mul(WayAny a, WayAny b) { return num_op(OP_MUL, a, b); }
WayAny way_any_div(WayAny a, WayAny b) { return num_op(OP_DIV, a, b); }
WayAny way_any_mod(WayAny a, WayAny b) { return num_op(OP_MOD, a, b); }

WayAny way_any_neg(WayAny a) {
  if (kind_of(a) == WAY_KIND_INT) {
    return box_int(-int_of(a));
  }
  if (kind_of(a) == WAY_KIND_FLOAT) {
    return box_float(-float_of(a));
  }
  way_fatal("cannot negate a %s value", kind_name(a));
  return a;
}

// --- comparison -----------------------------------------------------------

static int64_t compare(WayAny a, WayAny b) {
  int ka = kind_of(a);
  int kb = kind_of(b);
  if (ka == WAY_KIND_STRING || ka == WAY_KIND_ERROR) {
    if (kb != WAY_KIND_STRING && kb != WAY_KIND_ERROR) {
      way_fatal("cannot compare %s with %s", kind_name(a), kind_name(b));
    }
    return way_str_cmp(a.data, b.data);
  }
  if ((ka == WAY_KIND_INT || ka == WAY_KIND_FLOAT) &&
      (kb == WAY_KIND_INT || kb == WAY_KIND_FLOAT)) {
    if (ka == WAY_KIND_INT && kb == WAY_KIND_INT) {
      int64_t x = int_of(a);
      int64_t y = int_of(b);
      return x < y ? -1 : (x > y ? 1 : 0);
    }
    double x = float_of(a);
    double y = float_of(b);
    return x < y ? -1 : (x > y ? 1 : 0);
  }
  if (ka == WAY_KIND_BOOL && kb == WAY_KIND_BOOL) {
    bool x = *(bool *)a.data;
    bool y = *(bool *)b.data;
    return x == y ? 0 : (x ? 1 : -1);
  }
  if (a.data == NULL && b.data == NULL) {
    return 0;
  }
  way_fatal("cannot compare %s with %s", kind_name(a), kind_name(b));
  return 0;
}

bool way_any_eq(WayAny a, WayAny b) { return compare(a, b) == 0; }
bool way_any_ne(WayAny a, WayAny b) { return compare(a, b) != 0; }
bool way_any_lt(WayAny a, WayAny b) { return compare(a, b) < 0; }
bool way_any_le(WayAny a, WayAny b) { return compare(a, b) <= 0; }
bool way_any_gt(WayAny a, WayAny b) { return compare(a, b) > 0; }
bool way_any_ge(WayAny a, WayAny b) { return compare(a, b) >= 0; }

// --- errors ---------------------------------------------------------------

void *way_error_new(void *msg) {
  int64_t len = way_str_len(msg);
  void *p = way_alloc((int64_t)sizeof(int64_t) + len + 1, WAY_KIND_ERROR, NULL);
  WayStr *e = way_str(p);
  e->len = len;
  if (len > 0) {
    memcpy(e->bytes, way_str_bytes(msg), (size_t)len);
  }
  e->bytes[len] = '\0';
  return p;
}
