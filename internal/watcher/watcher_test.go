package watcher

import (
	"context"
	"testing"
)

func TestNoopWatcher_RunReturnsCanceledOnCancel(t *testing.T) {
	w := &NoopWatcher{}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- w.Run(ctx)
	}()

	cancel()

	err := <-done
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
}

func TestFailureTypeConstants(t *testing.T) {
	if FailureTypeOOMKilled != "OOMKilled" {
		t.Errorf("FailureTypeOOMKilled = %q, want %q", FailureTypeOOMKilled, "OOMKilled")
	}
	if FailureTypeCrashLoopBackOff != "CrashLoopBackOff" {
		t.Errorf("FailureTypeCrashLoopBackOff = %q, want %q", FailureTypeCrashLoopBackOff, "CrashLoopBackOff")
	}
	if FailureTypeImagePullBackOff != "ImagePullBackOff" {
		t.Errorf("FailureTypeImagePullBackOff = %q, want %q", FailureTypeImagePullBackOff, "ImagePullBackOff")
	}
}
