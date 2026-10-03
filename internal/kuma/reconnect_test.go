package kuma

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	bremlkuma "github.com/breml/go-uptime-kuma-client"
)

// closableFake is a FakeClient that records Close, like realClient's
// Disconnect.
type closableFake struct {
	*FakeClient
	closed atomic.Bool
}

func (c *closableFake) Close() error {
	c.closed.Store(true)
	return nil
}

// dialer hands out a new closableFake per dial and counts the dials.
type dialer struct {
	mu      sync.Mutex
	dials   int
	clients []*closableFake
	err     error
}

func (d *dialer) dial() (Client, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.dials++
	if d.err != nil {
		return nil, d.err
	}
	c := &closableFake{FakeClient: NewFakeClient()}
	d.clients = append(d.clients, c)
	return c, nil
}

// errClosedConn has the shape of the error seen in production after Kuma
// dropped the WebSocket: "get monitors: getMonitorList: write tcp
// 10.244.3.37:51710->10.108.139.224:3001: use of closed network connection".
var errClosedConn = fmt.Errorf("get monitors: getMonitorList: %w",
	&net.OpError{Op: "write", Net: "tcp", Err: net.ErrClosed})

func newTestClient(t *testing.T) (*ReconnectingClient, *dialer, *closableFake) {
	t.Helper()
	d := &dialer{}
	initial, err := d.dial()
	if err != nil {
		t.Fatal(err)
	}
	d.dials = 0
	return newReconnectingClient(initial, d.dial), d, initial.(*closableFake)
}

func waitClosed(t *testing.T, c *closableFake) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !c.closed.Load() {
		if time.Now().After(deadline) {
			t.Fatal("lost connection was never closed")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestReconnectingClient_RedialsAfterConnectionLost(t *testing.T) {
	ctx := context.Background()
	r, d, first := newTestClient(t)
	first.ExistingSpecsErr = errClosedConn

	if _, err := r.ExistingSpecs(ctx); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("first call: got %v, want the connection error passed through", err)
	}
	waitClosed(t, first)

	if _, err := r.ExistingSpecs(ctx); err != nil {
		t.Fatalf("call after reconnect: %v", err)
	}
	if d.dials != 1 {
		t.Fatalf("dials = %d, want 1", d.dials)
	}

	// The new connection is kept for later calls.
	if _, err := r.Tags(ctx); err != nil {
		t.Fatalf("second call on new connection: %v", err)
	}
	if d.dials != 1 {
		t.Fatalf("dials = %d after a successful call, want still 1", d.dials)
	}
}

func TestReconnectingClient_KeepsConnectionOnRequestError(t *testing.T) {
	ctx := context.Background()
	r, d, first := newTestClient(t)
	// What syncEmit returns when Kuma answers the request with ok=false.
	first.UpsertErr = errors.New("editMonitor: Invalid URL")

	if _, err := r.Upsert(ctx, 1, MonitorSpec{}); err == nil {
		t.Fatal("want the request error passed through")
	}
	first.UpsertErr = nil
	if _, err := r.Upsert(ctx, 0, MonitorSpec{}); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if d.dials != 0 || first.closed.Load() {
		t.Fatalf("a rejected request must not drop the connection (dials=%d closed=%v)", d.dials, first.closed.Load())
	}
}

func TestReconnectingClient_ConnectionLostErrors(t *testing.T) {
	live := context.Background()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name string
		ctx  context.Context
		err  error
		want bool
	}{
		{"closed network connection", live, errClosedConn, true},
		{"operation timeout", live, fmt.Errorf("get monitors: getMonitorList: %w", bremlkuma.ErrOperationTimeout), true},
		{"socket.io client context canceled", live, fmt.Errorf("getMonitorList: %w", context.Canceled), true},
		{"caller canceled", canceled, fmt.Errorf("getMonitorList: %w", context.Canceled), false},
		{"request rejected", live, errors.New("addMonitor: Invalid URL"), false},
		{"not found", live, bremlkuma.ErrNotFound, false},
		{"nil", live, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := connectionLost(tt.ctx, tt.err); got != tt.want {
				t.Fatalf("connectionLost(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestReconnectingClient_RetriesFailedDial(t *testing.T) {
	ctx := context.Background()
	r, d, first := newTestClient(t)
	first.DeleteErr = errClosedConn
	_ = r.Delete(ctx, 1)

	d.err = errors.New("connect to server: dial tcp: connection refused")
	if err := r.Delete(ctx, 1); err == nil {
		t.Fatal("want the dial error while Kuma is down")
	}

	d.err = nil
	if err := r.Delete(ctx, 1); err != nil {
		t.Fatalf("call once Kuma is back: %v", err)
	}
	if d.dials != 2 {
		t.Fatalf("dials = %d, want 2 (one failed, one succeeded)", d.dials)
	}
}

// A call that started on a connection which has since been replaced must not
// discard the replacement when it fails.
func TestReconnectingClient_StaleFailureKeepsNewConnection(t *testing.T) {
	ctx := context.Background()
	r, d, first := newTestClient(t)

	first.ExistingSpecsErr = errClosedConn
	_, _ = r.ExistingSpecs(ctx)
	if _, err := r.Tags(ctx); err != nil {
		t.Fatal(err)
	}

	// A late failure reported against the old connection.
	r.check(ctx, first, errClosedConn)

	if _, err := r.Tags(ctx); err != nil {
		t.Fatal(err)
	}
	if d.dials != 1 {
		t.Fatalf("dials = %d, want 1: the replacement connection was discarded", d.dials)
	}
}

func (d *dialer) setErr(err error) {
	d.mu.Lock()
	d.err = err
	d.mu.Unlock()
}

func (d *dialer) dialCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dials
}

func TestReconnectingClient_ReadyCheck(t *testing.T) {
	ctx := context.Background()
	r, d, first := newTestClient(t)

	if err := r.ReadyCheck(nil); err != nil {
		t.Fatalf("ready with the initial connection: %v", err)
	}

	// Kuma goes away: the failing call marks the client not ready, and
	// while Kuma stays down the probe keeps failing with the dial error.
	d.setErr(errors.New("connect to server: dial tcp: connection refused"))
	first.ExistingSpecsErr = errClosedConn
	_, _ = r.ExistingSpecs(ctx)
	if err := r.ReadyCheck(nil); err == nil || !errors.Is(err, net.ErrClosed) {
		t.Fatalf("ReadyCheck after connection loss = %v, want the lost-connection error", err)
	}
	waitFor(t, "a background dial while Kuma is down", func() bool {
		return d.dialCount() >= 1 && errors.Is(readyErr(r), d.err)
	})

	// Kuma comes back: with no reconcile calling in, the probe's own
	// background dial brings the client back to ready.
	d.setErr(nil)
	waitFor(t, "ready once Kuma is back", func() bool { return r.ReadyCheck(nil) == nil })

	if _, err := r.ExistingSpecs(ctx); err != nil {
		t.Fatalf("call on the connection the probe dialed: %v", err)
	}
}

func readyErr(r *ReconnectingClient) error {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	return r.down
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
