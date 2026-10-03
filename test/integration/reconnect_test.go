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
// operator is restarted, and that the readiness check reports the outage and
// the recovery. It needs the compose file to restart Kuma:
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

	// The first call after the restart finds the connection gone, which
	// marks the operator unready.
	callCtx, callCancel := context.WithTimeout(ctx, 15*time.Second)
	_, err = client.ExistingSpecs(callCtx)
	callCancel()
	if err == nil {
		t.Fatal("ExistingSpecs right after the restart succeeded, want the lost-connection error")
	}
	t.Logf("call after restart: %v", err)
	if err := client.ReadyCheck(nil); err == nil {
		t.Fatal("ReadyCheck after a lost connection = nil, want not ready")
	}

	// With no reconcile calling in, the readiness probe alone (kubelet polls
	// it every 10s) must reconnect once Kuma is back up.
	deadline := time.Now().Add(3 * time.Minute)
	for attempt := 1; ; attempt++ {
		err := client.ReadyCheck(nil)
		if err == nil {
			t.Logf("ready again after %d probe(s)", attempt)
			break
		}
		t.Logf("probe %d: %v", attempt, err)
		if time.Now().After(deadline) {
			t.Fatalf("client did not become ready after the Kuma restart: %v", err)
		}
		time.Sleep(2 * time.Second)
	}

	callCtx, callCancel = context.WithTimeout(ctx, 15*time.Second)
	defer callCancel()
	if _, err := client.ExistingSpecs(callCtx); err != nil {
		t.Fatalf("ExistingSpecs on the reconnected client: %v", err)
	}
}
