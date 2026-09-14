package kuma

import (
	"context"
	"testing"
)

func TestFakeClient_UpsertCreatesThenUpdates(t *testing.T) {
	c := NewFakeClient()
	ctx := context.Background()

	id, err := c.Upsert(ctx, 0, MonitorSpec{Type: TypeHTTP, Name: "v1", HTTP: &HTTPSpec{URL: "https://a"}})
	if err != nil {
		t.Fatalf("Upsert create: %v", err)
	}
	if id == 0 {
		t.Fatal("Upsert create returned id 0, want a nonzero assigned id")
	}
	if got := c.Monitors[id].Name; got != "v1" {
		t.Errorf("stored Name = %q, want %q", got, "v1")
	}

	gotID, err := c.Upsert(ctx, id, MonitorSpec{Type: TypeHTTP, Name: "v2", HTTP: &HTTPSpec{URL: "https://a"}})
	if err != nil {
		t.Fatalf("Upsert update: %v", err)
	}
	if gotID != id {
		t.Errorf("Upsert update returned id %d, want unchanged %d", gotID, id)
	}
	if got := c.Monitors[id].Name; got != "v2" {
		t.Errorf("stored Name after update = %q, want %q", got, "v2")
	}
}

func TestFakeClient_Delete(t *testing.T) {
	c := NewFakeClient()
	ctx := context.Background()

	id, _ := c.Upsert(ctx, 0, MonitorSpec{Type: TypeHTTP, Name: "v1", HTTP: &HTTPSpec{URL: "https://a"}})
	if err := c.Delete(ctx, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := c.Monitors[id]; ok {
		t.Errorf("monitor %d still present after Delete", id)
	}
}

func TestFakeClient_DeleteUnknownIDIsNoop(t *testing.T) {
	c := NewFakeClient()
	if err := c.Delete(context.Background(), 999); err != nil {
		t.Errorf("Delete of unknown id: %v, want nil", err)
	}
}
