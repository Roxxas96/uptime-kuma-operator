package kuma

import (
	"context"

	bremlkuma "github.com/breml/go-uptime-kuma-client"
)

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
// backed by the real Socket.IO connection.
func NewClient(ctx context.Context, url, username, password string) (Client, error) {
	c, err := bremlkuma.New(ctx, url, username, password)
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
