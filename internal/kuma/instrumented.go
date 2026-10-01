package kuma

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// instrumentedClient wraps a Client, recording a request counter and a latency
// histogram for every Kuma call, labelled by operation and outcome.
type instrumentedClient struct {
	inner    Client
	requests metric.Int64Counter
	duration metric.Float64Histogram
}

// Instrument wraps c so every call is recorded on meter as
// uptime_kuma_operator.kuma.requests and
// uptime_kuma_operator.kuma.request.duration, with an operation attribute
// naming the Client method (create, update, delete, list_monitors,
// list_notifications, find_group, list_tags, create_tag, set_monitor_tags)
// and outcome=success|error.
func Instrument(c Client, meter metric.Meter) (Client, error) {
	requests, err := meter.Int64Counter(
		"uptime_kuma_operator.kuma.requests",
		metric.WithDescription("Number of requests made to Uptime Kuma."),
		metric.WithUnit("{request}"),
	)
	if err != nil {
		return nil, err
	}
	duration, err := meter.Float64Histogram(
		"uptime_kuma_operator.kuma.request.duration",
		metric.WithDescription("Latency of requests made to Uptime Kuma."),
		metric.WithUnit("s"),
		// Kuma calls are Socket.IO round-trips: typically tens of
		// milliseconds, seconds when Kuma is under load.
		metric.WithExplicitBucketBoundaries(0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30),
	)
	if err != nil {
		return nil, err
	}
	return &instrumentedClient{inner: c, requests: requests, duration: duration}, nil
}

func (c *instrumentedClient) Upsert(ctx context.Context, id int64, spec MonitorSpec) (int64, error) {
	op := "update"
	if id == 0 {
		op = "create"
	}
	start := time.Now()
	newID, err := c.inner.Upsert(ctx, id, spec)
	c.record(ctx, op, start, err)
	return newID, err
}

func (c *instrumentedClient) Delete(ctx context.Context, id int64) error {
	start := time.Now()
	err := c.inner.Delete(ctx, id)
	c.record(ctx, "delete", start, err)
	return err
}

func (c *instrumentedClient) ExistingSpecs(ctx context.Context) (map[int64]MonitorSpec, error) {
	start := time.Now()
	specs, err := c.inner.ExistingSpecs(ctx)
	c.record(ctx, "list_monitors", start, err)
	return specs, err
}

func (c *instrumentedClient) Notifications(ctx context.Context) (map[string]int64, error) {
	start := time.Now()
	ids, err := c.inner.Notifications(ctx)
	c.record(ctx, "list_notifications", start, err)
	return ids, err
}

func (c *instrumentedClient) FindGroup(ctx context.Context, name string) (int64, bool, error) {
	start := time.Now()
	id, found, err := c.inner.FindGroup(ctx, name)
	c.record(ctx, "find_group", start, err)
	return id, found, err
}

func (c *instrumentedClient) Tags(ctx context.Context) (map[string]int64, error) {
	start := time.Now()
	ids, err := c.inner.Tags(ctx)
	c.record(ctx, "list_tags", start, err)
	return ids, err
}

func (c *instrumentedClient) CreateTag(ctx context.Context, name string) (int64, error) {
	start := time.Now()
	id, err := c.inner.CreateTag(ctx, name)
	c.record(ctx, "create_tag", start, err)
	return id, err
}

func (c *instrumentedClient) SetMonitorTags(ctx context.Context, monitorID int64, tagIDs []int64) error {
	start := time.Now()
	err := c.inner.SetMonitorTags(ctx, monitorID, tagIDs)
	c.record(ctx, "set_monitor_tags", start, err)
	return err
}

func (c *instrumentedClient) record(ctx context.Context, op string, start time.Time, err error) {
	outcome := "success"
	if err != nil {
		outcome = "error"
	}
	attrs := metric.WithAttributes(
		attribute.String("operation", op),
		attribute.String("outcome", outcome),
	)
	c.requests.Add(ctx, 1, attrs)
	c.duration.Record(ctx, time.Since(start).Seconds(), attrs)
}

var _ Client = (*instrumentedClient)(nil)
