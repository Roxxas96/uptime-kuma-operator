package kuma

import "context"

// Client manages Kuma monitors on behalf of the operator's controllers.
// Upsert creates a monitor when id is 0, or updates the existing monitor
// with that id otherwise; it returns the monitor's Kuma ID (unchanged from
// id on update, newly assigned on create).
type Client interface {
	Upsert(ctx context.Context, id int64, spec MonitorSpec) (int64, error)
	Delete(ctx context.Context, id int64) error
}
