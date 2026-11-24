// Placeholder for WayLang's future concurrency runtime.
//
// WayLang does not implement goroutines, channels or a task scheduler yet. This
// translation unit exists so the runtime layout is stable and so the absence of
// a scheduler is explicit rather than hidden. Nothing here is called by
// generated code.
#include <stdint.h>

// Reports whether the scheduler is available. Always 0 until concurrency lands.
int64_t way_scheduler_available(void) { return 0; }
