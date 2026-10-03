package kuma

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"syscall"

	bremlkuma "github.com/breml/go-uptime-kuma-client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// reconnectingClient is a Client that survives the loss of its Socket.IO
// connection. bremlkuma.Client dials once and never reconnects: after Kuma
// restarts, or a proxy or the network drops the WebSocket, every later call
// fails with the same "use of closed network connection" until the operator
// is restarted. reconnectingClient discards a connection as soon as a call
// reports it lost, and dials a fresh one on the next call, so the
// reconciler's normal error requeue is enough to recover.
type reconnectingClient struct {
	// dial opens a new connection. It must be bound to a context that lives
	// as long as the process: bremlkuma.New keeps that context for the
	// connection's whole lifetime, so a per-reconcile context would close
	// the connection as soon as the reconcile returned.
	dial func() (Client, error)

	mu      sync.Mutex
	current Client
}

// newReconnectingClient returns a reconnectingClient whose first connection
// is initial, so a bad URL or bad credentials still fail at startup.
func newReconnectingClient(initial Client, dial func() (Client, error)) *reconnectingClient {
	return &reconnectingClient{dial: dial, current: initial}
}

// get returns the live connection, dialing a new one if the last was
// discarded. Concurrent callers wait for a single dial rather than each
// opening their own; the dial is bounded by connectTimeout.
func (r *reconnectingClient) get() (Client, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.current != nil {
		return r.current, nil
	}
	c, err := r.dial()
	if err != nil {
		return nil, fmt.Errorf("reconnect to Uptime Kuma: %w", err)
	}
	r.current = c
	return c, nil
}

// check discards c if err shows its connection is gone. Only the connection
// that failed is discarded: a concurrent call may already have replaced it.
func (r *reconnectingClient) check(ctx context.Context, c Client, err error) {
	if !connectionLost(ctx, err) {
		return
	}
	r.mu.Lock()
	if r.current != c {
		r.mu.Unlock()
		return
	}
	r.current = nil
	r.mu.Unlock()

	logf.FromContext(ctx).Info("Uptime Kuma connection lost, reconnecting on the next call", "reason", err.Error())
	if closer, ok := c.(io.Closer); ok {
		// Best effort: the transport is already broken, and Close only
		// stops the old client's goroutines.
		go func() { _ = closer.Close() }()
	}
}

// connectionLost reports whether err means the connection itself is unusable,
// as opposed to Kuma rejecting one request.
func connectionLost(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	// A Socket.IO client whose transport stopped cancels its own context,
	// and Emit then returns context.Canceled. The same error with the
	// caller's context still alive can only have come from the client.
	if errors.Is(err, context.Canceled) {
		return ctx.Err() == nil
	}
	var opErr *net.OpError
	return errors.Is(err, net.ErrClosed) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.As(err, &opErr) ||
		// No ack within the operation timeout: a half-open connection
		// (peer gone without a FIN) looks exactly like this.
		errors.Is(err, bremlkuma.ErrOperationTimeout)
}

func (r *reconnectingClient) Upsert(ctx context.Context, id int64, spec MonitorSpec) (int64, error) {
	c, err := r.get()
	if err != nil {
		return 0, err
	}
	got, err := c.Upsert(ctx, id, spec)
	r.check(ctx, c, err)
	return got, err
}

func (r *reconnectingClient) Delete(ctx context.Context, id int64) error {
	c, err := r.get()
	if err != nil {
		return err
	}
	err = c.Delete(ctx, id)
	r.check(ctx, c, err)
	return err
}

func (r *reconnectingClient) ExistingSpecs(ctx context.Context) (map[int64]MonitorSpec, error) {
	c, err := r.get()
	if err != nil {
		return nil, err
	}
	got, err := c.ExistingSpecs(ctx)
	r.check(ctx, c, err)
	return got, err
}

func (r *reconnectingClient) Notifications(ctx context.Context) (map[string]int64, error) {
	c, err := r.get()
	if err != nil {
		return nil, err
	}
	got, err := c.Notifications(ctx)
	r.check(ctx, c, err)
	return got, err
}

func (r *reconnectingClient) FindGroup(ctx context.Context, name string) (int64, bool, error) {
	c, err := r.get()
	if err != nil {
		return 0, false, err
	}
	id, found, err := c.FindGroup(ctx, name)
	r.check(ctx, c, err)
	return id, found, err
}

func (r *reconnectingClient) Tags(ctx context.Context) (map[string]int64, error) {
	c, err := r.get()
	if err != nil {
		return nil, err
	}
	got, err := c.Tags(ctx)
	r.check(ctx, c, err)
	return got, err
}

func (r *reconnectingClient) CreateTag(ctx context.Context, name string) (int64, error) {
	c, err := r.get()
	if err != nil {
		return 0, err
	}
	id, err := c.CreateTag(ctx, name)
	r.check(ctx, c, err)
	return id, err
}

func (r *reconnectingClient) SetMonitorTags(ctx context.Context, monitorID int64, tagIDs []int64) error {
	c, err := r.get()
	if err != nil {
		return err
	}
	err = c.SetMonitorTags(ctx, monitorID, tagIDs)
	r.check(ctx, c, err)
	return err
}

var _ Client = (*reconnectingClient)(nil)
