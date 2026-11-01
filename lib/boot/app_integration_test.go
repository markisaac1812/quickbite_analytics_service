//go:build integration

package boot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/quickbite/analytics-service/app/analytics/repository"
	"github.com/quickbite/analytics-service/internal/testkit"
	"github.com/quickbite/analytics-service/lib/config"
	"github.com/quickbite/analytics-service/lib/coreevents"
)

// testApp is the whole service booted the way cmd/api boots it — run() with
// real MongoDB, real RabbitMQ and a real HTTP listener — plus the core-service
// stub and side channels to arrange/assert state. Go analogue of
// order-service's tests/helpers/server.ts (startTestServer).
type testApp struct {
	cfg     config.Config
	baseURL string
	core    *testkit.CoreStub
	mongo   *testkit.TestMongo
	amqp    *testkit.AmqpProbe

	ownsMongo bool // false when the database is borrowed from another app

	cancel context.CancelFunc
	done   chan error
}

// app is shared by every integration test of this package (one boot per
// package, like one startTestServer() per Jest file).
var app *testApp

func TestMain(m *testing.M) {
	var err error
	app, err = startApp("boot", nil, func(c *config.Config) {
		c.Rabbit.Prefetch = 1 // one worker: events apply in publish order, so tests can use a sentinel event
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "start test app:", err)
		os.Exit(1)
	}
	code := m.Run()
	if err := app.stop(); err != nil {
		fmt.Fprintln(os.Stderr, "stop test app:", err)
		code = 1
	}
	os.Exit(code)
}

func quietLogger() *slog.Logger {
	if os.Getenv("TEST_LOG") != "" {
		return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// startApp boots run() in the background and waits until /health is 200.
// With shared != nil the app uses that (already indexed) database instead of
// creating its own: building the indexes of a fresh database costs seconds.
func startApp(suffix string, shared *testkit.TestMongo, mutate func(*config.Config)) (*testApp, error) {
	core := testkit.NewCoreStub()
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	cfg := testkit.Config(suffix, core.URL(), port)
	if mutate != nil {
		mutate(&cfg)
	}

	mongo, owns := shared, shared == nil
	if owns {
		if mongo, err = testkit.OpenMongo(suffix); err != nil {
			core.Close()
			return nil, err
		}
	} else {
		cfg.Mongo.Database = shared.DB.Name()
	}
	probe, err := testkit.OpenAmqp()
	if err != nil {
		core.Close()
		if owns {
			mongo.Close()
		}
		return nil, err
	}

	a := &testApp{cfg: cfg, baseURL: fmt.Sprintf("http://127.0.0.1:%d", port), core: core, mongo: mongo, amqp: probe, ownsMongo: owns, done: make(chan error, 1)}
	a.dropTopology() // leftovers of an aborted earlier run
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	go func() { a.done <- run(ctx, cfg, quietLogger()) }()

	deadline := time.Now().Add(30 * time.Second)
	for {
		select {
		case err := <-a.done:
			a.cleanup()
			return nil, fmt.Errorf("run exited during boot: %w", err)
		default:
		}
		if res, err := http.Get(a.baseURL + "/health"); err == nil {
			res.Body.Close()
			if res.StatusCode == http.StatusOK {
				return a, nil
			}
		}
		if time.Now().After(deadline) {
			_ = a.stop()
			return nil, errors.New("service did not become healthy in 30s")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// stop cancels run's context (≈ SIGTERM), waits for the graceful shutdown
// and removes everything the test created.
func (a *testApp) stop() error {
	a.cancel()
	var err error
	select {
	case err = <-a.done:
	case <-time.After(30 * time.Second):
		err = errors.New("run did not return after shutdown")
	}
	a.cleanup()
	return err
}

func (a *testApp) cleanup() {
	a.dropTopology()
	a.amqp.Close()
	if a.ownsMongo {
		a.mongo.Close()
	}
	a.core.Close()
}

func (a *testApp) dropTopology() {
	r, c := a.cfg.Rabbit, a.cfg.CoreEvents
	a.amqp.DeleteQueues(r.Queue, r.DLQ, c.Queue, c.DLQ)
	a.amqp.DeleteExchanges(r.Exchange, r.DLX, c.Exchange, c.DLX)
}

// reset gives each test a clean slate (≈ beforeEach(app.reset)): empty
// collections, default core permissions, empty queues.
func (a *testApp) reset(t *testing.T) {
	t.Helper()
	if err := a.mongo.Reset(); err != nil {
		t.Fatal(err)
	}
	a.core.Reset()
	for _, q := range []string{a.cfg.Rabbit.Queue, a.cfg.Rabbit.DLQ, a.cfg.CoreEvents.Queue, a.cfg.CoreEvents.DLQ} {
		a.amqp.Purge(q)
	}
}

// publish sends an envelope to order.events with its eventType as routing key.
func (a *testApp) publish(t *testing.T, env coreevents.Envelope) {
	t.Helper()
	a.publishRaw(t, a.cfg.Rabbit.Exchange, env.EventType, testkit.JSON(env))
}

func (a *testApp) publishRaw(t *testing.T, exchange, routingKey string, body []byte) {
	t.Helper()
	if err := a.amqp.Publish(exchange, routingKey, body); err != nil {
		t.Fatal(err)
	}
}

// waitApplied blocks until eventID's dedupe marker exists (= committed).
func (a *testApp) waitApplied(t *testing.T, eventID string) {
	t.Helper()
	testkit.Eventually(t, 10*time.Second, func() bool {
		return a.mongo.Count(repository.CollEventIDs, map[string]any{"event_id": eventID}) == 1
	}, "event "+eventID+" applied")
}

// waitDeadLettered blocks until queue holds n messages.
func (a *testApp) waitDeadLettered(t *testing.T, queue string, n int) {
	t.Helper()
	testkit.Eventually(t, 10*time.Second, func() bool { return a.amqp.MessageCount(queue) == n },
		fmt.Sprintf("%d message(s) in %s", n, queue))
}

func (a *testApp) get(t *testing.T, path string, caller *testkit.Caller) testkit.Response {
	t.Helper()
	return testkit.GetURL(t, a.baseURL, "/api/v1/analytics"+path, caller)
}
