//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	bremlkuma "github.com/breml/go-uptime-kuma-client"

	"uptime-kuma-operator/internal/kuma"
)

// TestRealClient_CreateUpdateDelete exercises internal/kuma.NewClient against
// a real Uptime Kuma instance. Run with:
//
//	docker compose -f test/integration/docker-compose.yaml up -d
//	KUMA_URL=http://localhost:3001 KUMA_USERNAME=admin KUMA_PASSWORD=password \
//	  go test -tags=integration ./test/integration/... -v
//
// No manual setup wizard needed: bootstrapKuma below completes it via
// breml's WithAutosetup, which is safe to call whether the instance is
// fresh or already configured with these same credentials.
func TestRealClient_CreateUpdateDelete(t *testing.T) {
	url := envOrSkip(t, "KUMA_URL")
	user := envOrSkip(t, "KUMA_USERNAME")
	pass := envOrSkip(t, "KUMA_PASSWORD")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	bootstrapKuma(t, ctx, url, user, pass)

	client, err := kuma.NewClient(ctx, url, user, pass)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	id, err := client.Upsert(ctx, 0, kuma.MonitorSpec{
		Type: kuma.TypeHTTP, Name: "integration-test-http",
		HTTP: &kuma.HTTPSpec{URL: "https://example.com/"},
	})
	if err != nil {
		t.Fatalf("Upsert create: %v", err)
	}

	if _, err := client.Upsert(ctx, id, kuma.MonitorSpec{
		Type: kuma.TypeHTTP, Name: "integration-test-http-renamed",
		HTTP: &kuma.HTTPSpec{URL: "https://example.com/"},
	}); err != nil {
		t.Fatalf("Upsert update: %v", err)
	}

	dnsID, err := client.Upsert(ctx, 0, kuma.MonitorSpec{
		Type: kuma.TypeDNS, Name: "integration-test-dns",
		DNS: &kuma.DNSSpec{Host: "example.com", ResolverServer: "1.1.1.1", ResolveType: "A"},
	})
	if err != nil {
		t.Fatalf("Upsert DNS create: %v", err)
	}

	existingIDs, err := client.ExistingIDs(ctx)
	if err != nil {
		t.Fatalf("ExistingIDs: %v", err)
	}
	if !existingIDs[id] || !existingIDs[dnsID] {
		t.Errorf("ExistingIDs = %v, want both %d and %d present", existingIDs, id, dnsID)
	}

	if err := client.Delete(ctx, id); err != nil {
		t.Errorf("Delete HTTP monitor: %v", err)
	}
	if err := client.Delete(ctx, dnsID); err != nil {
		t.Errorf("Delete DNS monitor: %v", err)
	}

	existingIDs, err = client.ExistingIDs(ctx)
	if err != nil {
		t.Fatalf("ExistingIDs after delete: %v", err)
	}
	if existingIDs[id] || existingIDs[dnsID] {
		t.Errorf("ExistingIDs = %v, want neither %d nor %d present after deletion", existingIDs, id, dnsID)
	}
}

// bootstrapKuma completes Kuma's first-run database and admin-account setup
// via breml's own client (not internal/kuma.Client, which deliberately has
// no autosetup — a running operator should never auto-provision credentials
// against an arbitrary configured URL). Safe to call against an
// already-configured instance: it just logs in normally in that case.
func bootstrapKuma(t *testing.T, ctx context.Context, url, user, pass string) {
	t.Helper()
	c, err := bremlkuma.New(ctx, url, user, pass, bremlkuma.WithAutosetup())
	if err != nil {
		t.Fatalf("bootstrapKuma: %v", err)
	}
	defer c.Disconnect() //nolint:errcheck // best-effort cleanup of a throwaway bootstrap connection
}

func envOrSkip(t *testing.T, key string) string {
	t.Helper()
	v := getenv(key)
	if v == "" {
		t.Skipf("%s not set, skipping integration test", key)
	}
	return v
}
