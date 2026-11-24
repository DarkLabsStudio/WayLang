// WayLang program entry point and small runtime builtins.
#include <math.h>
#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#ifdef _WIN32
#include <windows.h>
#else
#include <unistd.h>
#endif

#include "../internal.h"

// Emitted by the compiler for the user's `func main()`.
extern void way_user_main(void);

// --- diagnostics ----------------------------------------------------------

void way_fatal(const char *fmt, ...) {
  fflush(stdout);
  fputs("runtime error: ", stderr);
  va_list ap;
  va_start(ap, fmt);
  vfprintf(stderr, fmt, ap);
  va_end(ap);
  fputc('\n', stderr);
  fflush(stderr);
  exit(1);
}

// --- os -------------------------------------------------------------------

void way_os_exit(int64_t code) {
  fflush(stdout);
  exit((int)code);
}

void *way_os_getenv(void *name) {
  char *key = (char *)malloc((size_t)way_str_len(name) + 1);
  if (key == NULL) {
    way_fatal("out of memory in os.getenv");
  }
  memcpy(key, way_str_bytes(name), (size_t)way_str_len(name));
  key[way_str_len(name)] = '\0';
  const char *value = getenv(key);
  free(key);
  if (value == NULL) {
    return NULL;
  }
  return way_str_from_cstr(value);
}

WaySlice way_os_args(void) {
  WaySlice out;
  int argc = way_rt_argc();
  char **argv = way_rt_argv();
  int64_t n = argc > 1 ? argc - 1 : 0;
  out.data = way_seq_alloc(n, (int64_t)sizeof(void *));
  out.len = n;
  out.cap = n;
  void **slots = (void **)out.data;
  for (int64_t i = 0; i < n; i++) {
    slots[i] = way_str_from_cstr(argv[i + 1]);
  }
  return out;
}

// --- time -----------------------------------------------------------------

int64_t way_time_now(void) {
#ifdef _WIN32
  FILETIME ft;
  GetSystemTimeAsFileTime(&ft);
  uint64_t t = ((uint64_t)ft.dwHighDateTime << 32) | (uint64_t)ft.dwLowDateTime;
  // 100ns ticks since 1601-01-01 to milliseconds since the Unix epoch.
  return (int64_t)((t - 116444736000000000ULL) / 10000ULL);
#else
  struct timespec ts;
  clock_gettime(CLOCK_REALTIME, &ts);
  return (int64_t)ts.tv_sec * 1000 + (int64_t)(ts.tv_nsec / 1000000);
#endif
}

void way_time_sleep(int64_t ms) {
  if (ms <= 0) {
    return;
  }
#ifdef _WIN32
  Sleep((DWORD)ms);
#else
  struct timespec ts;
  ts.tv_sec = (time_t)(ms / 1000);
  ts.tv_nsec = (long)((ms % 1000) * 1000000);
  nanosleep(&ts, NULL);
#endif
}

// --- math -----------------------------------------------------------------

double way_math_sqrt(double v) { return sqrt(v); }
double way_math_pow(double a, double b) { return pow(a, b); }
double way_math_floor(double v) { return floor(v); }
double way_math_ceil(double v) { return ceil(v); }
double way_math_round(double v) { return round(v); }
double way_math_abs(double v) { return fabs(v); }

int64_t way_math_min(int64_t a, int64_t b) { return a < b ? a : b; }
int64_t way_math_max(int64_t a, int64_t b) { return a > b ? a : b; }

// --- entry ----------------------------------------------------------------

int main(int argc, char **argv) {
  way_rt_init(argc, argv);
  way_user_main();
  fflush(stdout);
  return 0;
}
