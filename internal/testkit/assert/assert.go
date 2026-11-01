// Package assert is a tiny, stdlib-only assertion helper for tests (no
// testify: CLAUDE.md §2 forbids new libraries). It imports nothing from this
// module, so any test package — internal or external — can use it without
// an import cycle.
package assert

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// Equal fails the test when got and want are not deeply equal.
func Equal[T any](t testing.TB, got, want T, msg ...string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%snot equal:\n got: %#v\nwant: %#v", prefix(msg), got, want)
	}
}

// True fails the test when cond is false.
func True(t testing.TB, cond bool, msg ...string) {
	t.Helper()
	if !cond {
		t.Fatalf("%sexpected true", prefix(msg))
	}
}

// False fails the test when cond is true.
func False(t testing.TB, cond bool, msg ...string) {
	t.Helper()
	if cond {
		t.Fatalf("%sexpected false", prefix(msg))
	}
}

// NoErr fails the test when err is non-nil.
func NoErr(t testing.TB, err error, msg ...string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%sunexpected error: %v", prefix(msg), err)
	}
}

// Err fails the test when err is nil.
func Err(t testing.TB, err error, msg ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%sexpected an error, got nil", prefix(msg))
	}
}

// ErrIs fails the test unless errors.Is(err, target).
func ErrIs(t testing.TB, err, target error, msg ...string) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("%sexpected error matching %v, got %v", prefix(msg), target, err)
	}
}

// Contains fails the test unless s contains sub.
func Contains(t testing.TB, s, sub string, msg ...string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Fatalf("%sexpected %q to contain %q", prefix(msg), s, sub)
	}
}

// Len fails the test unless the slice has n elements.
func Len[T any](t testing.TB, s []T, n int, msg ...string) {
	t.Helper()
	if len(s) != n {
		t.Fatalf("%sexpected len %d, got %d: %#v", prefix(msg), n, len(s), s)
	}
}

func prefix(msg []string) string {
	if len(msg) == 0 {
		return ""
	}
	return strings.Join(msg, " ") + ": "
}
