// Network.framework owns the route observation. No interface-name/cellular guess,
// browser hint, default-network change or access/profile state enters this boundary.
#import <Foundation/Foundation.h>
#import <Network/Network.h>
#include <stdatomic.h>

static nw_path_monitor_t monitor;
static dispatch_queue_t queue;
// The generation and value commit in one atomic operation. A queued update from
// before Stop/Start cannot publish an old unmetered route into the new lifetime.
static _Atomic unsigned long long state;
static unsigned long long next_generation(void) {
    unsigned long long old = atomic_load(&state), next;
    do { next = (old & ~3ULL) + 4; }
    while (!atomic_compare_exchange_weak(&state, &old, next));
    return next;
}

void portico_network_start(void) {
    if (monitor) return;
    if (@available(macOS 10.15, *)) {
        const unsigned long long token = next_generation();
        queue = dispatch_queue_create("tv.getportico.desktop.network", DISPATCH_QUEUE_SERIAL);
        monitor = nw_path_monitor_create();
        if (!monitor) { queue = nil; return; }
        nw_path_monitor_set_queue(monitor, queue);
        nw_path_monitor_set_update_handler(monitor, ^(nw_path_t path) {
            unsigned next = 0;
            if (nw_path_get_status(path) == nw_path_status_satisfied)
                next = nw_path_is_expensive(path) || nw_path_is_constrained(path) ? 1 : 2;
            unsigned long long old = atomic_load(&state);
            while ((old & ~3ULL) == token &&
                   !atomic_compare_exchange_weak(&state, &old, token | next)) {}
        });
        nw_path_monitor_start(monitor);
    }
}

void portico_network_stop(void) {
    next_generation();
    if (@available(macOS 10.15, *)) {
        if (monitor) nw_path_monitor_cancel(monitor);
    }
    monitor = nil;
    queue = nil;
}

unsigned portico_network_cost(void) { return atomic_load(&state) & 3; }
