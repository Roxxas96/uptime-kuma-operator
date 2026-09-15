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

	UpsertErr error
	DeleteErr error

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

var _ Client = (*FakeClient)(nil)
