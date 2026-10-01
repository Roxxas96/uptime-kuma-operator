package telemetry

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"k8s.io/apimachinery/pkg/types"
)

// Sources of managed Kuma monitors, used as the "source" metric attribute.
const (
	SourceIngress   = "ingress"
	SourceHTTPRoute = "httproute"
	SourceService   = "service"
	SourceMonitor   = "monitor"
)

// ManagedMonitors tracks how many Kuma monitors each Kubernetes object owns
// and reports the totals, per source and namespace, as an observable gauge.
// The namespace attribute is "resource_namespace", not "namespace", so it
// doesn't collide with the target label Prometheus adds when scraping.
//
// Reconcilers call Set after every successful sync (and with 0 once an object
// is gone or opted out). The counts rebuild on restart because the manager
// reconciles every watched object on startup.
//
// A nil *ManagedMonitors is valid and records nothing, so reconcilers stay
// usable in tests without telemetry.
type ManagedMonitors struct {
	mu     sync.Mutex
	counts map[string]map[types.NamespacedName]int
}

// NewManagedMonitors registers the uptime_kuma_operator.managed_monitors gauge
// on mp and returns the tracker that feeds it.
func NewManagedMonitors(mp metric.MeterProvider) (*ManagedMonitors, error) {
	m := &ManagedMonitors{counts: map[string]map[types.NamespacedName]int{}}
	_, err := mp.Meter(MeterName).Int64ObservableGauge(
		"uptime_kuma_operator.managed_monitors",
		metric.WithDescription("Number of Uptime Kuma monitors managed by the operator."),
		metric.WithUnit("{monitor}"),
		metric.WithInt64Callback(m.observe),
	)
	if err != nil {
		return nil, err
	}
	return m, nil
}

// Set records that the object key, of the given source, currently owns n Kuma
// monitors. n <= 0 forgets the object.
func (m *ManagedMonitors) Set(source string, key types.NamespacedName, n int) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if n <= 0 {
		delete(m.counts[source], key)
		return
	}
	if m.counts[source] == nil {
		m.counts[source] = map[types.NamespacedName]int{}
	}
	m.counts[source][key] = n
}

func (m *ManagedMonitors) observe(_ context.Context, o metric.Int64Observer) error {
	type bucket struct{ source, namespace string }
	totals := map[bucket]int64{}

	m.mu.Lock()
	for source, objs := range m.counts {
		for key, n := range objs {
			totals[bucket{source, key.Namespace}] += int64(n)
		}
	}
	m.mu.Unlock()

	for b, n := range totals {
		o.Observe(n, metric.WithAttributes(
			attribute.String("source", b.source),
			attribute.String("resource_namespace", b.namespace),
		))
	}
	return nil
}
