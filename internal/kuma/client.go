package kuma

import (
	"context"
	"time"

	bremlkuma "github.com/breml/go-uptime-kuma-client"
)

// connectTimeout bounds how long NewClient waits for the initial Socket.IO
// connection (and, with autosetup, the first-run setup) to complete. Without
// it, a stalled connection (bad URL, network partition, a proxy that mangles
// long-polling/WebSocket upgrades) blocks bremlkuma.New forever — and since
// NewClient runs before the manager creates any other goroutines, that hang
// is indistinguishable from a real deadlock, which crashes the process via
// Go's runtime deadlock detector instead of returning a retryable error.
const connectTimeout = 30 * time.Second

// Client manages Kuma monitors on behalf of the operator's controllers.
// Upsert creates a monitor when id is 0, or updates the existing monitor
// with that id otherwise; it returns the monitor's Kuma ID (unchanged from
// id on update, newly assigned on create).
type Client interface {
	Upsert(ctx context.Context, id int64, spec MonitorSpec) (int64, error)
	Delete(ctx context.Context, id int64) error
}

// realClient is a Client backed by a real Socket.IO connection to an
// Uptime Kuma instance via the breml/go-uptime-kuma-client library.
type realClient struct {
	inner *bremlkuma.Client
}

// NewClient logs into the Uptime Kuma instance at url and returns a Client
// backed by the real Socket.IO connection. The connection attempt is bounded
// by connectTimeout, so a stalled Kuma endpoint fails fast instead of
// hanging the caller forever.
//
// ctx is not wrapped with its own timeout here: bremlkuma.New retains ctx
// for the connection's entire lifetime (its internal goroutines treat
// ctx.Done() as a shutdown signal, not just a connect-attempt deadline), so
// wrapping it in a context.WithTimeout that gets canceled once this
// function returns would silently kill every subsequent call on a
// successfully-connected client. bremlkuma.WithConnectTimeout bounds the
// connection attempt on its own, independent of ctx's cancellation state,
// which is what actually fixes the original hang.
func NewClient(ctx context.Context, url, username, password string) (Client, error) {
	return newClient(ctx, url, username, password, connectTimeout)
}

func newClient(ctx context.Context, url, username, password string, timeout time.Duration) (Client, error) {
	c, err := bremlkuma.New(ctx, url, username, password, bremlkuma.WithConnectTimeout(timeout))
	if err != nil {
		return nil, err
	}
	return &realClient{inner: c}, nil
}

func (r *realClient) Upsert(ctx context.Context, id int64, spec MonitorSpec) (int64, error) {
	mon, err := ToBremlMonitor(id, spec)
	if err != nil {
		return 0, err
	}
	if id == 0 {
		return r.inner.CreateMonitor(ctx, mon)
	}
	if err := r.inner.UpdateMonitor(ctx, mon); err != nil {
		return 0, err
	}
	return id, nil
}

func (r *realClient) Delete(ctx context.Context, id int64) error {
	return r.inner.DeleteMonitor(ctx, id)
}

var _ Client = (*realClient)(nil)
