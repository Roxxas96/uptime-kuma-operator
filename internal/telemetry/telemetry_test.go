package telemetry_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"k8s.io/apimachinery/pkg/types"

	"uptime-kuma-operator/internal/kuma"
	"uptime-kuma-operator/internal/telemetry"
)

func noEnv(string) string { return "" }

// setup returns a registry wired to a fresh MeterProvider, an instrumented
// fake Kuma client and a ManagedMonitors tracker.
func setup(t *testing.T) (*prometheus.Registry, *kuma.FakeClient, kuma.Client, *telemetry.ManagedMonitors) {
	t.Helper()
	reg := prometheus.NewRegistry()
	mp, shutdown, err := telemetry.Setup(context.Background(), reg, noEnv)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = shutdown(context.Background()) })

	fake := kuma.NewFakeClient()
	kc, err := kuma.Instrument(fake, mp.Meter(telemetry.MeterName))
	if err != nil {
		t.Fatal(err)
	}
	managed, err := telemetry.NewManagedMonitors(mp)
	if err != nil {
		t.Fatal(err)
	}
	return reg, fake, kc, managed
}

func TestKumaRequestMetrics(t *testing.T) {
	reg, fake, kc, _ := setup(t)
	ctx := context.Background()

	id, err := kc.Upsert(ctx, 0, kuma.MonitorSpec{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kc.Upsert(ctx, id, kuma.MonitorSpec{}); err != nil {
		t.Fatal(err)
	}
	fake.DeleteErr = errors.New("boom")
	if err := kc.Delete(ctx, id); err == nil {
		t.Fatal("expected delete error to propagate")
	}

	want := `
# HELP uptime_kuma_operator_kuma_requests_total Number of requests made to Uptime Kuma.
# TYPE uptime_kuma_operator_kuma_requests_total counter
uptime_kuma_operator_kuma_requests_total{operation="create",outcome="success"} 1
uptime_kuma_operator_kuma_requests_total{operation="delete",outcome="error"} 1
uptime_kuma_operator_kuma_requests_total{operation="update",outcome="success"} 1
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want), "uptime_kuma_operator_kuma_requests_total"); err != nil {
		t.Fatal(err)
	}

	n, err := testutil.GatherAndCount(reg, "uptime_kuma_operator_kuma_request_duration_seconds")
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("want 3 duration series, got %d", n)
	}
}

func TestManagedMonitorsGauge(t *testing.T) {
	reg, _, _, managed := setup(t)

	managed.Set(telemetry.SourceIngress, types.NamespacedName{Namespace: "a", Name: "x"}, 2)
	managed.Set(telemetry.SourceIngress, types.NamespacedName{Namespace: "a", Name: "y"}, 3)
	managed.Set(telemetry.SourceMonitor, types.NamespacedName{Namespace: "b", Name: "z"}, 1)
	managed.Set(telemetry.SourceHTTPRoute, types.NamespacedName{Namespace: "a", Name: "gone"}, 4)
	managed.Set(telemetry.SourceHTTPRoute, types.NamespacedName{Namespace: "a", Name: "gone"}, 0)

	want := `
# HELP uptime_kuma_operator_managed_monitors Number of Uptime Kuma monitors managed by the operator.
# TYPE uptime_kuma_operator_managed_monitors gauge
uptime_kuma_operator_managed_monitors{resource_namespace="a",source="ingress"} 5
uptime_kuma_operator_managed_monitors{resource_namespace="b",source="monitor"} 1
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want), "uptime_kuma_operator_managed_monitors"); err != nil {
		t.Fatal(err)
	}
}

func TestNilManagedMonitorsIsNoop(t *testing.T) {
	var m *telemetry.ManagedMonitors
	m.Set(telemetry.SourceIngress, types.NamespacedName{Name: "x"}, 1)
}

func TestOTLPEnabled(t *testing.T) {
	env := func(kv map[string]string) func(string) string {
		return func(k string) string { return kv[k] }
	}
	cases := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"unset", nil, false},
		{"endpoint", map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://c:4317"}, true},
		{"metrics endpoint", map[string]string{"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "http://c:4317"}, true},
		{"exporter none", map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://c:4317", "OTEL_METRICS_EXPORTER": "none"}, false},
	}
	for _, tc := range cases {
		if got := telemetry.OTLPEnabled(env(tc.env)); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}
