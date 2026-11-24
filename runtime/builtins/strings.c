// WayLang string objects, conversions and string builtins.
//
// A string is a managed payload shaped { int64 length; char bytes[]; } with a
// trailing NUL byte that is not counted in the length. String literals emitted
// by the compiler use exactly this layout, so they can be read (and concatenated
// with) objects allocated here.
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "../internal.h"

#define WAY_REPLACEMENT 0xFFFDu

// alloc_str allocates a string payload with room for a terminating NUL.
static void *alloc_str(int64_t len, int kind) {
  if (len < 0) {
    way_fatal("negative string length");
  }
  void *p = way_alloc((int64_t)sizeof(int64_t) + len + 1, kind, NULL);
  WayStr *s = way_str(p);
  s->len = len;
  s->bytes[len] = '\0';
  return p;
}

void *way_str_from_cstr(const char *text) {
  if (text == NULL) {
    return alloc_str(0, WAY_KIND_STRING);
  }
  int64_t len = (int64_t)strlen(text);
  void *p = alloc_str(len, WAY_KIND_STRING);
  memcpy(way_str_bytes(p), text, (size_t)len);
  return p;
}

void *way_str_from_bytes(const char *bytes, int64_t len) {
  void *p = alloc_str(len, WAY_KIND_STRING);
  if (len > 0) {
    memcpy(way_str_bytes(p), bytes, (size_t)len);
  }
  return p;
}

int64_t way_str_len(void *p) {
  if (p == NULL) {
    return 0;
  }
  return way_str(p)->len;
}

void *way_str_concat(void *a, void *b) {
  int64_t la = way_str_len(a);
  int64_t lb = way_str_len(b);
  void *p = alloc_str(la + lb, WAY_KIND_STRING);
  if (la > 0) {
    memcpy(way_str_bytes(p), way_str_bytes(a), (size_t)la);
  }
  if (lb > 0) {
    memcpy(way_str_bytes(p) + la, way_str_bytes(b), (size_t)lb);
  }
  return p;
}

int64_t way_str_cmp(void *a, void *b) {
  int64_t la = way_str_len(a);
  int64_t lb = way_str_len(b);
  int64_t n = la < lb ? la : lb;
  if (n > 0) {
    int c = memcmp(way_str_bytes(a), way_str_bytes(b), (size_t)n);
    if (c != 0) {
      return c < 0 ? -1 : 1;
    }
  }
  if (la == lb) {
    return 0;
  }
  return la < lb ? -1 : 1;
}

bool way_str_eq(void *a, void *b) {
  if (a == b) {
    return true;
  }
  int64_t la = way_str_len(a);
  int64_t lb = way_str_len(b);
  if (la != lb) {
    return false;
  }
  if (la == 0) {
    return true;
  }
  return memcmp(way_str_bytes(a), way_str_bytes(b), (size_t)la) == 0;
}

int8_t way_str_index(void *s, int64_t i) {
  way_bounds_check(i, way_str_len(s));
  return (int8_t)(unsigned char)way_str_bytes(s)[i];
}

void *way_str_substr(void *s, int64_t low, int64_t high) {
  int64_t len = way_str_len(s);
  if (low < 0 || high < low || high > len) {
    way_fatal("slice bounds out of range [%lld:%lld] with length %lld",
              (long long)low, (long long)high, (long long)len);
  }
  return way_str_from_bytes(way_str_bytes(s) + low, high - low);
}

// --- UTF-8 ----------------------------------------------------------------

// rune_decode decodes the rune starting at byte i. Invalid sequences yield the
// Unicode replacement character and advance by one byte.
static int32_t rune_decode(const unsigned char *b, int64_t len, int64_t *i) {
  int64_t pos = *i;
  unsigned char c = b[pos];
  if (c < 0x80) {
    *i = pos + 1;
    return (int32_t)c;
  }
  int extra;
  int32_t cp;
  if ((c & 0xE0) == 0xC0) {
    extra = 1;
    cp = c & 0x1F;
  } else if ((c & 0xF0) == 0xE0) {
    extra = 2;
    cp = c & 0x0F;
  } else if ((c & 0xF8) == 0xF0) {
    extra = 3;
    cp = c & 0x07;
  } else {
    *i = pos + 1;
    return (int32_t)WAY_REPLACEMENT;
  }
  if (pos + extra >= len) {
    *i = pos + 1;
    return (int32_t)WAY_REPLACEMENT;
  }
  for (int k = 1; k <= extra; k++) {
    unsigned char cc = b[pos + k];
    if ((cc & 0xC0) != 0x80) {
      *i = pos + 1;
      return (int32_t)WAY_REPLACEMENT;
    }
    cp = (cp << 6) | (cc & 0x3F);
  }
  *i = pos + extra + 1;
  return cp;
}

int64_t way_str_rune_len(void *s) {
  const unsigned char *b = (const unsigned char *)way_str_bytes(s);
  int64_t len = way_str_len(s);
  int64_t count = 0;
  int64_t i = 0;
  while (i < len) {
    rune_decode(b, len, &i);
    count++;
  }
  return count;
}

int32_t way_str_rune_at(void *s, int64_t rune_index) {
  const unsigned char *b = (const unsigned char *)way_str_bytes(s);
  int64_t len = way_str_len(s);
  int64_t i = 0;
  int64_t n = 0;
  while (i < len) {
    int32_t cp = rune_decode(b, len, &i);
    if (n == rune_index) {
      return cp;
    }
    n++;
  }
  way_fatal("rune index %lld out of range [0:%lld)", (long long)rune_index,
            (long long)n);
  return 0;
}

// --- conversions ----------------------------------------------------------

// temporary returns a NUL-terminated copy of s for the C library helpers.
static char *temporary(void *s) {
  int64_t len = way_str_len(s);
  char *buf = (char *)malloc((size_t)len + 1);
  if (buf == NULL) {
    way_fatal("out of memory converting a string");
  }
  if (len > 0) {
    memcpy(buf, way_str_bytes(s), (size_t)len);
  }
  buf[len] = '\0';
  return buf;
}

static char *trimmed_copy(void *s) {
  char *buf = temporary(s);
  char *start = buf;
  while (*start == ' ' || *start == '\t' || *start == '\n' || *start == '\r' ||
         *start == '\v' || *start == '\f') {
    start++;
  }
  return start;
}

int64_t way_str_to_int(void *s) {
  char *buf = temporary(s);
  char *start = trimmed_copy(s);
  char *end = NULL;
  long long v = strtoll(start, &end, 10);
  if (end == start) {
    way_fatal("cannot convert %s to int", buf);
  }
  free(buf);
  return (int64_t)v;
}

double way_str_to_float(void *s) {
  char *buf = temporary(s);
  char *start = trimmed_copy(s);
  char *end = NULL;
  double v = strtod(start, &end);
  if (end == start) {
    way_fatal("cannot convert %s to float", buf);
  }
  free(buf);
  return v;
}

bool way_str_to_bool(void *s) {
  char *buf = trimmed_copy(s);
  bool v;
  if (strncmp(buf, "true", 4) == 0 && buf[4] == '\0') {
    v = true;
  } else if (strncmp(buf, "false", 5) == 0 && buf[5] == '\0') {
    v = false;
  } else if (buf[0] == '1' && buf[1] == '\0') {
    v = true;
  } else if (buf[0] == '0' && buf[1] == '\0') {
    v = false;
  } else {
    way_fatal("cannot convert %s to bool", buf);
    v = false;
  }
  return v;
}

int8_t way_str_to_byte(void *s) {
  int64_t v = way_str_to_int(s);
  if (v < 0 || v > 255) {
    way_fatal("cannot convert %lld to byte: out of range", (long long)v);
  }
  return (int8_t)(uint8_t)v;
}

int32_t way_str_to_rune(void *s) {
  if (way_str_len(s) == 0) {
    way_fatal("cannot convert an empty string to rune");
  }
  int64_t i = 0;
  return rune_decode((const unsigned char *)way_str_bytes(s), way_str_len(s), &i);
}

void *way_str_from_int(int64_t v) {
  char buf[32];
  int n = snprintf(buf, sizeof(buf), "%lld", (long long)v);
  return way_str_from_bytes(buf, n);
}

void *way_str_from_float(double v) {
  char buf[64];
  int n = snprintf(buf, sizeof(buf), "%g", v);
  return way_str_from_bytes(buf, n);
}

void *way_str_from_bool(bool v) {
  return way_str_from_cstr(v ? "true" : "false");
}

void *way_str_from_rune(int32_t v) {
  unsigned char buf[4];
  uint32_t cp = (uint32_t)v;
  int n;
  if (cp < 0x80) {
    buf[0] = (unsigned char)cp;
    n = 1;
  } else if (cp < 0x800) {
    buf[0] = (unsigned char)(0xC0 | (cp >> 6));
    buf[1] = (unsigned char)(0x80 | (cp & 0x3F));
    n = 2;
  } else if (cp < 0x10000) {
    buf[0] = (unsigned char)(0xE0 | (cp >> 12));
    buf[1] = (unsigned char)(0x80 | ((cp >> 6) & 0x3F));
    buf[2] = (unsigned char)(0x80 | (cp & 0x3F));
    n = 3;
  } else {
    buf[0] = (unsigned char)(0xF0 | (cp >> 18));
    buf[1] = (unsigned char)(0x80 | ((cp >> 12) & 0x3F));
    buf[2] = (unsigned char)(0x80 | ((cp >> 6) & 0x3F));
    buf[3] = (unsigned char)(0x80 | (cp & 0x3F));
    n = 4;
  }
  return way_str_from_bytes((const char *)buf, n);
}

// --- strings package ------------------------------------------------------

static void *map_ascii(void *s, int upper) {
  int64_t len = way_str_len(s);
  void *p = way_str_from_bytes(way_str_bytes(s), len);
  char *b = way_str_bytes(p);
  for (int64_t i = 0; i < len; i++) {
    unsigned char c = (unsigned char)b[i];
    if (upper && c >= 'a' && c <= 'z') {
      b[i] = (char)(c - 32);
    } else if (!upper && c >= 'A' && c <= 'Z') {
      b[i] = (char)(c + 32);
    }
  }
  return p;
}

void *way_strings_upper(void *s) { return map_ascii(s, 1); }

void *way_strings_lower(void *s) { return map_ascii(s, 0); }

static bool is_space(char c) {
  return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' ||
         c == '\f';
}

void *way_strings_trim(void *s) {
  const char *b = way_str_bytes(s);
  int64_t len = way_str_len(s);
  int64_t low = 0;
  int64_t high = len;
  while (low < high && is_space(b[low])) {
    low++;
  }
  while (high > low && is_space(b[high - 1])) {
    high--;
  }
  return way_str_from_bytes(b + low, high - low);
}

bool way_strings_contains(void *s, void *sub) {
  return way_strings_index(s, sub) >= 0;
}

bool way_strings_has_prefix(void *s, void *pre) {
  int64_t ls = way_str_len(s);
  int64_t lp = way_str_len(pre);
  if (lp > ls) {
    return false;
  }
  return lp == 0 || memcmp(way_str_bytes(s), way_str_bytes(pre), (size_t)lp) == 0;
}

bool way_strings_has_suffix(void *s, void *suf) {
  int64_t ls = way_str_len(s);
  int64_t lf = way_str_len(suf);
  if (lf > ls) {
    return false;
  }
  return lf == 0 ||
         memcmp(way_str_bytes(s) + (ls - lf), way_str_bytes(suf), (size_t)lf) == 0;
}

int64_t way_strings_index(void *s, void *sub) {
  int64_t ls = way_str_len(s);
  int64_t lp = way_str_len(sub);
  if (lp == 0) {
    return 0;
  }
  if (lp > ls) {
    return -1;
  }
  const char *hay = way_str_bytes(s);
  const char *needle = way_str_bytes(sub);
  for (int64_t i = 0; i + lp <= ls; i++) {
    if (memcmp(hay + i, needle, (size_t)lp) == 0) {
      return i;
    }
  }
  return -1;
}

void *way_strings_replace(void *s, void *old, void *new_s) {
  int64_t ls = way_str_len(s);
  int64_t lo = way_str_len(old);
  int64_t ln = way_str_len(new_s);
  if (lo == 0) {
    return way_str_from_bytes(way_str_bytes(s), ls);
  }
  int64_t hits = 0;
  for (int64_t i = 0; i + lo <= ls;) {
    if (memcmp(way_str_bytes(s) + i, way_str_bytes(old), (size_t)lo) == 0) {
      hits++;
      i += lo;
    } else {
      i++;
    }
  }
  if (hits == 0) {
    return way_str_from_bytes(way_str_bytes(s), ls);
  }
  int64_t out_len = ls + hits * (ln - lo);
  void *p = alloc_str(out_len, WAY_KIND_STRING);
  char *out = way_str_bytes(p);
  const char *src = way_str_bytes(s);
  int64_t i = 0;
  int64_t o = 0;
  while (i < ls) {
    if (i + lo <= ls && memcmp(src + i, way_str_bytes(old), (size_t)lo) == 0) {
      if (ln > 0) {
        memcpy(out + o, way_str_bytes(new_s), (size_t)ln);
      }
      o += ln;
      i += lo;
    } else {
      out[o++] = src[i++];
    }
  }
  return p;
}

void *way_strings_repeat(void *s, int64_t n) {
  int64_t ls = way_str_len(s);
  if (n <= 0 || ls == 0) {
    return way_str_from_bytes("", 0);
  }
  if (ls > (int64_t)1 << 40 || n > ((int64_t)1 << 40) / ls) {
    way_fatal("strings.repeat result is too large");
  }
  void *p = alloc_str(ls * n, WAY_KIND_STRING);
  char *out = way_str_bytes(p);
  for (int64_t i = 0; i < n; i++) {
    memcpy(out + i * ls, way_str_bytes(s), (size_t)ls);
  }
  return p;
}

// --- printing -------------------------------------------------------------

static void write_bytes(const char *bytes, int64_t len) {
  if (len <= 0) {
    return;
  }
  fwrite(bytes, 1, (size_t)len, stdout);
  if (memchr(bytes, '\n', (size_t)len) != NULL) {
    fflush(stdout);
  }
}

void way_print_str(void *s) { write_bytes(way_str_bytes(s), way_str_len(s)); }

void way_print_int(int64_t v) {
  char buf[32];
  int n = snprintf(buf, sizeof(buf), "%lld", (long long)v);
  write_bytes(buf, n);
}

void way_print_float(double v) {
  char buf[64];
  int n = snprintf(buf, sizeof(buf), "%g", v);
  write_bytes(buf, n);
}

void way_print_bool(bool v) { write_bytes(v ? "true" : "false", v ? 4 : 5); }

// --- bounds checking ------------------------------------------------------

void way_bounds_check(int64_t idx, int64_t len) {
  if (idx < 0 || idx >= len) {
    way_fatal("index out of range [%lld] with length %lld", (long long)idx,
              (long long)len);
  }
}
