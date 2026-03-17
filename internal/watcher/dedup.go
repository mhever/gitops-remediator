package watcher

import (
	"fmt"
	"sync"
	"time"
)

const dedupWindow = 10 * time.Minute

// deduplicator suppresses repeated FailureEvents for the same pod/type
// within a rolling time window. Container name is intentionally excluded from
// the key: the same pod failure fires multiple events with different container
// values (empty string during ContainerCreating, then the real name once the
// pull is retried). Keying on namespace/pod/type is sufficient to catch these.
type deduplicator struct {
	mu    sync.Mutex
	seen  map[string]time.Time
	clock func() time.Time // injectable for tests
}

func newDeduplicator() *deduplicator {
	return &deduplicator{
		seen:  make(map[string]time.Time),
		clock: time.Now,
	}
}

// purge removes all entries from seen that have expired beyond dedupWindow.
// Must be called with d.mu held.
func (d *deduplicator) purge() {
	now := d.clock()
	for k, t := range d.seen {
		if now.Sub(t) >= dedupWindow {
			delete(d.seen, k)
		}
	}
}

// isDuplicate returns true if an identical FailureEvent was seen within the
// dedup window, and records the current time if not.
func (d *deduplicator) isDuplicate(e FailureEvent) bool {
	key := fmt.Sprintf("%s/%s/%s", e.Namespace, e.PodName, e.FailureType)
	now := d.clock()

	d.mu.Lock()
	defer d.mu.Unlock()

	d.purge()

	if last, ok := d.seen[key]; ok && now.Sub(last) < dedupWindow {
		return true
	}

	d.seen[key] = now
	return false
}
