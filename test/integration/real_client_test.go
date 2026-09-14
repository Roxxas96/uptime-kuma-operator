//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"uptime-kuma-operator/internal/kuma"
)

// TestRealClient_CreateUpdateDelete exercises internal/kuma.NewClient against
// a real Uptime Kuma instance. Run with:
//
//	docker compose -f test/integration/docker-compose.yaml up -d
//	# then, after completing Kuma's one-time setup wizard at http://localhost:3001
//	# with username/password matching the env vars below:
//	KUMA_URL=http://localhost:3001 KUMA_USERNAME=admin KUMA_PASSWORD=password \
//	  go test -tags=integration ./test/integration/... -v
func TestRealClient_CreateUpdateDelete(t *testing.T) {
	url := envOrSkip(t, "KUMA_URL")
	user := envOrSkip(t, "KUMA_USERNAME")
	pass := envOrSkip(t, "KUMA_PASSWORD")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

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

	if err := client.Delete(ctx, id); err != nil {
		t.Errorf("Delete HTTP monitor: %v", err)
	}
	if err := client.Delete(ctx, dnsID); err != nil {
		t.Errorf("Delete DNS monitor: %v", err)
	}
}

func envOrSkip(t *testing.T, key string) string {
	t.Helper()
	v := getenv(key)
	if v == "" {
		t.Skipf("%s not set, skipping integration test", key)
	}
	return v
}
