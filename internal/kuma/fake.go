package kuma

import (
	"context"
	"sync"
)

// FakeClient is an in-memory Client for use in reconciler tests.
type FakeClient struct {
	mu       sync.Mutex
	nextID   int64
	Monitors map[int64]MonitorSpec
}

func NewFakeClient() *FakeClient {
	return &FakeClient{Monitors: map[int64]MonitorSpec{}}
}

func (f *FakeClient) Upsert(_ context.Context, id int64, spec MonitorSpec) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
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
	delete(f.Monitors, id)
	return nil
}

var _ Client = (*FakeClient)(nil)
