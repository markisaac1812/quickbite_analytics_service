package testkit

import (
	"testing"
	"time"
)

// Eventually polls cond until it returns true or timeout elapses
// (≈ waitFor() in tests/helpers/wait.ts). For effects of asynchronous
// consumers: publish, then wait for the aggregate to show up.
func Eventually(t testing.TB, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for: %s", timeout, msg)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
