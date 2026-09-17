package kuma

import (
	"context"
	"sync"
)

// FakeClient is an in-memory Client for use in reconciler tests.
//
// UpsertErr and DeleteErr, when set, make the corresponding call fail without
// mutating Monitors, so tests can exercise reconcilers' Kuma-failure paths.
type FakeClient struct {
	mu       sync.Mutex
	nextID   int64
	Monitors map[int64]MonitorSpec

	UpsertErr        error
	DeleteErr        error
	ExistingSpecsErr error

	// NotificationIDs and GroupIDs are pre-seeded by tests to simulate
	// existing Kuma-side notification channels and groups.
	NotificationIDs map[string]int64
	GroupIDs        map[string]int64

	NotificationsErr error
	FindGroupErr     error

	// UpsertCalls and DeleteCalls count invocations, including ones that
	// returned an error — reconcilers should call Upsert only when the
	// desired Kuma configuration actually changed, since a real Upsert
	// (editMonitor) restarts that monitor's check timer even on an
	// unchanged payload.
	UpsertCalls int
	DeleteCalls int
}

func NewFakeClient() *FakeClient {
	return &FakeClient{Monitors: map[int64]MonitorSpec{}}
}

func (f *FakeClient) Upsert(_ context.Context, id int64, spec MonitorSpec) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.UpsertCalls++
	if f.UpsertErr != nil {
		return 0, f.UpsertErr
	}
	if id == 0 {
		f.nextID++
		id = f.nextID
	}
	f.Monitors[id] = spec
	return id, nil
}

func (f *FakeClient) Delete(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.DeleteCalls++
	if f.DeleteErr != nil {
		return f.DeleteErr
	}
	delete(f.Monitors, id)
	return nil
}

func (f *FakeClient) ExistingSpecs(_ context.Context) (map[int64]MonitorSpec, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ExistingSpecsErr != nil {
		return nil, f.ExistingSpecsErr
	}
	specs := make(map[int64]MonitorSpec, len(f.Monitors))
	for id, spec := range f.Monitors {
		specs[id] = spec
	}
	return specs, nil
}

func (f *FakeClient) Notifications(_ context.Context) (map[string]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.NotificationsErr != nil {
		return nil, f.NotificationsErr
	}
	out := make(map[string]int64, len(f.NotificationIDs))
	for k, v := range f.NotificationIDs {
		out[k] = v
	}
	return out, nil
}

func (f *FakeClient) FindGroup(_ context.Context, name string) (int64, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.FindGroupErr != nil {
		return 0, false, f.FindGroupErr
	}
	id, ok := f.GroupIDs[name]
	return id, ok, nil
}

var _ Client = (*FakeClient)(nil)
