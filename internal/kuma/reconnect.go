package kuma

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"syscall"

	bremlkuma "github.com/breml/go-uptime-kuma-client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// ReconnectingClient is a Client that survives the loss of its Socket.IO
// connection. bremlkuma.Client dials once and never reconnects: after Kuma
// restarts, or a proxy or the network drops the WebSocket, every later call
// fails with the same "use of closed network connection" until the operator
// is restarted. ReconnectingClient discards a connection as soon as a call
// reports it lost, and dials a fresh one on the next call, so the
// reconciler's normal error requeue is enough to recover.
type ReconnectingClient struct {
	// dial opens a new connection. It must be bound to a context that lives
	// as long as the process: bremlkuma.New keeps that context for the
	// connection's whole lifetime, so a per-reconcile context would close
	// the connection as soon as the reconcile returned.
	dial func() (Client, error)

	// mu guards current and is held for a whole dial, so concurrent callers
	// wait for one dial rather than each opening their own.
	mu      sync.Mutex
	current Client

	// stateMu guards down, which ReadyCheck reads without waiting on a dial
	// in progress under mu. down is nil while a connection is held, and
	// otherwise the reason there is none.
	stateMu sync.Mutex
	down    error
}

// newReconnectingClient returns a ReconnectingClient whose first connection
// is initial, so a bad URL or bad credentials still fail at startup.
func newReconnectingClient(initial Client, dial func() (Client, error)) *ReconnectingClient {
	return &ReconnectingClient{dial: dial, current: initial}
}

// get returns the live connection, dialing a new one if the last was
// discarded. Concurrent callers wait for a single dial rather than each
// opening their own; the dial is bounded by connectTimeout.
func (r *ReconnectingClient) get() (Client, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.getLocked()
}

func (r *ReconnectingClient) getLocked() (Client, error) {
	if r.current != nil {
		return r.current, nil
	}
	c, err := r.dial()
	if err != nil {
		err = fmt.Errorf("reconnect to Uptime Kuma: %w", err)
		r.setDown(err)
		return nil, err
	}
	r.current = c
	r.setDown(nil)
	return c, nil
}

func (r *ReconnectingClient) setDown(err error) {
	r.stateMu.Lock()
	r.down = err
	r.stateMu.Unlock()
}

// ReadyCheck is a controller-runtime healthz.Checker for the readiness
// probe: it fails while the operator holds no live Kuma connection, so a
// lost connection shows up as an unready pod rather than only as reconcile
// errors in the logs. It never blocks on a dial: when the connection is down
// and no dial is in progress, it starts one in the background, so the pod
// turns ready again once Kuma is back even if no reconcile is pending.
//
// It is deliberately not a liveness check: restarting the operator does not
// bring Kuma back, it only adds a crash loop on top of the outage.
func (r *ReconnectingClient) ReadyCheck(_ *http.Request) error {
	r.stateMu.Lock()
	down := r.down
	r.stateMu.Unlock()
	if down == nil {
		return nil
	}
	if r.mu.TryLock() {
		go func() {
			defer r.mu.Unlock()
			_, _ = r.getLocked()
		}()
	}
	return down
}

// check discards c if err shows its connection is gone. Only the connection
// that failed is discarded: a concurrent call may already have replaced it.
func (r *ReconnectingClient) check(ctx context.Context, c Client, err error) {
	if !connectionLost(ctx, err) {
		return
	}
	r.mu.Lock()
	if r.current != c {
		r.mu.Unlock()
		return
	}
	r.current = nil
	// Set under mu, so it cannot overwrite the up state of a dial that
	// replaces this connection right after.
	r.setDown(fmt.Errorf("connection to Uptime Kuma lost: %w", err))
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

func (r *ReconnectingClient) Upsert(ctx context.Context, id int64, spec MonitorSpec) (int64, error) {
	c, err := r.get()
	if err != nil {
		return 0, err
	}
	got, err := c.Upsert(ctx, id, spec)
	r.check(ctx, c, err)
	return got, err
}

func (r *ReconnectingClient) Delete(ctx context.Context, id int64) error {
	c, err := r.get()
	if err != nil {
		return err
	}
	err = c.Delete(ctx, id)
	r.check(ctx, c, err)
	return err
}

func (r *ReconnectingClient) ExistingSpecs(ctx context.Context) (map[int64]MonitorSpec, error) {
	c, err := r.get()
	if err != nil {
		return nil, err
	}
	got, err := c.ExistingSpecs(ctx)
	r.check(ctx, c, err)
	return got, err
}

func (r *ReconnectingClient) Notifications(ctx context.Context) (map[string]int64, error) {
	c, err := r.get()
	if err != nil {
		return nil, err
	}
	got, err := c.Notifications(ctx)
	r.check(ctx, c, err)
	return got, err
}

func (r *ReconnectingClient) FindGroup(ctx context.Context, name string) (int64, bool, error) {
	c, err := r.get()
	if err != nil {
		return 0, false, err
	}
	id, found, err := c.FindGroup(ctx, name)
	r.check(ctx, c, err)
	return id, found, err
}

func (r *ReconnectingClient) Tags(ctx context.Context) (map[string]int64, error) {
	c, err := r.get()
	if err != nil {
		return nil, err
	}
	got, err := c.Tags(ctx)
	r.check(ctx, c, err)
	return got, err
}

func (r *ReconnectingClient) CreateTag(ctx context.Context, name string) (int64, error) {
	c, err := r.get()
	if err != nil {
		return 0, err
	}
	id, err := c.CreateTag(ctx, name)
	r.check(ctx, c, err)
	return id, err
}

func (r *ReconnectingClient) SetMonitorTags(ctx context.Context, monitorID int64, tagIDs []int64) error {
	c, err := r.get()
	if err != nil {
		return err
	}
	err = c.SetMonitorTags(ctx, monitorID, tagIDs)
	r.check(ctx, c, err)
	return err
}

var _ Client = (*ReconnectingClient)(nil)
