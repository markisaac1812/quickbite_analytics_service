//go:build integration

package repository_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/quickbite/analytics-service/internal/testkit"
)

// tm is this package's test database (quickbite_analytics_test_repository),
// dropped and re-indexed once, emptied before every test.
var tm *testkit.TestMongo

func TestMain(m *testing.M) {
	var err error
	if tm, err = testkit.OpenMongo("repository"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	tm.Close()
	os.Exit(code)
}

// reset gives a test a clean slate (≈ beforeEach(app.reset)).
func reset(t *testing.T) {
	t.Helper()
	if err := tm.Reset(); err != nil {
		t.Fatal(err)
	}
}

// cancelled is a context that is already done: every driver call fails fast,
// which exercises the repositories' error wrapping.
func cancelled() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

var at = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
