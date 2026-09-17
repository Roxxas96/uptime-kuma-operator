package kuma

import (
	"context"
	"errors"
	"fmt"
	"reflect"
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

func TestFakeClient_ExistingSpecs(t *testing.T) {
	c := NewFakeClient()
	ctx := context.Background()

	specA := MonitorSpec{Type: TypeHTTP, Name: "a", HTTP: &HTTPSpec{URL: "https://a"}}
	specB := MonitorSpec{Type: TypeHTTP, Name: "b", HTTP: &HTTPSpec{URL: "https://b"}}
	id1, _ := c.Upsert(ctx, 0, specA)
	id2, _ := c.Upsert(ctx, 0, specB)

	specs, err := c.ExistingSpecs(ctx)
	if err != nil {
		t.Fatalf("ExistingSpecs: %v", err)
	}
	if !reflect.DeepEqual(specs[id1], specA) || !reflect.DeepEqual(specs[id2], specB) {
		t.Errorf("ExistingSpecs = %v, want %d=%v and %d=%v", specs, id1, specA, id2, specB)
	}

	if err := c.Delete(ctx, id1); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	specs, err = c.ExistingSpecs(ctx)
	if err != nil {
		t.Fatalf("ExistingSpecs after delete: %v", err)
	}
	if _, ok := specs[id1]; ok {
		t.Errorf("ExistingSpecs = %v, want %d absent after delete", specs, id1)
	}
	if !reflect.DeepEqual(specs[id2], specB) {
		t.Errorf("ExistingSpecs = %v, want %d still present", specs, id2)
	}
}

func TestFakeClient_ExistingSpecsErr(t *testing.T) {
	c := NewFakeClient()
	sentinel := fmt.Errorf("boom")
	c.ExistingSpecsErr = sentinel

	if _, err := c.ExistingSpecs(context.Background()); !errors.Is(err, sentinel) {
		t.Errorf("ExistingSpecs error = %v, want %v", err, sentinel)
	}
}

func TestFakeClient_Notifications(t *testing.T) {
	c := NewFakeClient()
	c.NotificationIDs = map[string]int64{"slack-prod": 1, "email-oncall": 2}

	got, err := c.Notifications(context.Background())
	if err != nil {
		t.Fatalf("Notifications: %v", err)
	}
	if got["slack-prod"] != 1 || got["email-oncall"] != 2 {
		t.Errorf("Notifications = %v, want {slack-prod:1 email-oncall:2}", got)
	}
}

func TestFakeClient_FindGroup(t *testing.T) {
	c := NewFakeClient()
	c.GroupIDs = map[string]int64{"prod-services": 5}

	id, found, err := c.FindGroup(context.Background(), "prod-services")
	if err != nil || !found || id != 5 {
		t.Errorf("FindGroup(prod-services) = (%d, %v, %v), want (5, true, nil)", id, found, err)
	}

	_, found, err = c.FindGroup(context.Background(), "nonexistent")
	if err != nil || found {
		t.Errorf("FindGroup(nonexistent) = (_, %v, %v), want (false, nil)", found, err)
	}
}
