//go:build integration

package integration

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"uptime-kuma-operator/internal/kuma"
)

// TestRealClient_ReconnectsAfterKumaRestart restarts the Kuma container under
// a connected client and checks the client recovers on its own, instead of
// failing every call with "use of closed network connection" until the
// operator is restarted. It needs the compose file to restart Kuma:
//
//	KUMA_COMPOSE_FILE=$PWD/test/integration/docker-compose.yaml KUMA_URL=... \
//	  go test -tags=integration ./test/integration/... -run Reconnect -v
func TestRealClient_ReconnectsAfterKumaRestart(t *testing.T) {
	url := envOrSkip(t, "KUMA_URL")
	user := envOrSkip(t, "KUMA_USERNAME")
	pass := envOrSkip(t, "KUMA_PASSWORD")
	composeFile := envOrSkip(t, "KUMA_COMPOSE_FILE")

	// NewClient keeps its context for every later reconnect, as main's
	// process-lifetime context does; calls get their own deadlines.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	setupCtx, setupCancel := context.WithTimeout(ctx, 30*time.Second)
	bootstrapKuma(t, setupCtx, url, user, pass)
	setupCancel()

	client, err := kuma.NewClient(ctx, url, kuma.Credentials{Username: user, Password: pass})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := client.ExistingSpecs(ctx); err != nil {
		t.Fatalf("ExistingSpecs before restart: %v", err)
	}

	out, err := exec.Command("docker", "compose", "-f", composeFile, "restart", "uptime-kuma").CombinedOutput()
	if err != nil {
		t.Fatalf("restart Kuma: %v\n%s", err, out)
	}

	// Like the reconciler's requeue: calls fail while Kuma is down and the
	// dead connection is discarded, then a later call reconnects.
	deadline := time.Now().Add(3 * time.Minute)
	for attempt := 1; ; attempt++ {
		callCtx, callCancel := context.WithTimeout(ctx, 15*time.Second)
		_, err := client.ExistingSpecs(callCtx)
		callCancel()
		if err == nil {
			t.Logf("recovered after %d attempt(s)", attempt)
			return
		}
		t.Logf("attempt %d: %v", attempt, err)
		if time.Now().After(deadline) {
			t.Fatalf("client did not recover from the Kuma restart: %v", err)
		}
		time.Sleep(2 * time.Second)
	}
}
