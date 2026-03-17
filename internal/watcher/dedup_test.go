package watcher

import (
	"testing"
	"time"
)

func TestDeduplicator(t *testing.T) {
	t.Run("first call returns false", func(t *testing.T) {
		d := newDeduplicator()
		e := FailureEvent{
			Namespace:     "ns",
			PodName:       "pod",
			ContainerName: "app",
			FailureType:   FailureTypeOOMKilled,
		}
		if got := d.isDuplicate(e); got != false {
			t.Errorf("isDuplicate() = %v, want false", got)
		}
	})

	t.Run("second call within window returns true", func(t *testing.T) {
		d := newDeduplicator()
		e := FailureEvent{
			Namespace:     "ns",
			PodName:       "pod",
			ContainerName: "app",
			FailureType:   FailureTypeOOMKilled,
		}
		d.isDuplicate(e) // first call records it
		if got := d.isDuplicate(e); got != true {
			t.Errorf("isDuplicate() = %v, want true", got)
		}
	})

	t.Run("same key after window expires returns false", func(t *testing.T) {
		now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		d := newDeduplicator()
		d.clock = func() time.Time { return now }

		e := FailureEvent{
			Namespace:     "ns",
			PodName:       "pod",
			ContainerName: "app",
			FailureType:   FailureTypeCrashLoopBackOff,
		}

		d.isDuplicate(e) // records at `now`

		// Advance clock past the dedup window.
		d.clock = func() time.Time { return now.Add(dedupWindow + time.Second) }

		if got := d.isDuplicate(e); got != false {
			t.Errorf("isDuplicate() after window = %v, want false", got)
		}
	})

	t.Run("same pod different container name is duplicate", func(t *testing.T) {
		// Reproduces the real-world case where the watcher fires two events for
		// the same pod failure: first with ContainerName="" (ContainerCreating),
		// then with ContainerName="app" (ErrImagePull). Both must be deduped.
		d := newDeduplicator()
		e1 := FailureEvent{
			Namespace:     "ns",
			PodName:       "pod",
			ContainerName: "",
			FailureType:   FailureTypeImagePullBackOff,
		}
		e2 := FailureEvent{
			Namespace:     "ns",
			PodName:       "pod",
			ContainerName: "app",
			FailureType:   FailureTypeImagePullBackOff,
		}
		d.isDuplicate(e1) // first event records the key
		if got := d.isDuplicate(e2); got != true {
			t.Errorf("isDuplicate(e2 with real container name) = %v, want true (same pod/type)", got)
		}
	})

	t.Run("different key returns false independently", func(t *testing.T) {
		d := newDeduplicator()
		e1 := FailureEvent{
			Namespace:     "ns",
			PodName:       "pod1",
			ContainerName: "app",
			FailureType:   FailureTypeOOMKilled,
		}
		e2 := FailureEvent{
			Namespace:     "ns",
			PodName:       "pod2",
			ContainerName: "app",
			FailureType:   FailureTypeOOMKilled,
		}

		d.isDuplicate(e1) // record e1

		if got := d.isDuplicate(e2); got != false {
			t.Errorf("isDuplicate(e2) = %v, want false (different key)", got)
		}
	})
}
