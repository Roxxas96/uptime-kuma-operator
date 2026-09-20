package derive

import (
	"fmt"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"uptime-kuma-operator/internal/annotations"
)

// HTTPRouteMonitors derives one DesiredMonitor per distinct, non-empty
// hostname in route.Spec.Hostnames. Scheme defaults to "https" (HTTPRoute
// carries no TLS info of its own — see design spec); ov.HTTP.Scheme overrides it.
func HTTPRouteMonitors(route *gatewayv1.HTTPRoute, ov annotations.Overrides) []DesiredMonitor {
	scheme := "https"
	if ov.HTTP.Scheme != "" {
		scheme = ov.HTTP.Scheme
	}

	// The Gateway API schema does not enforce uniqueness of spec.hostnames, so
	// dedupe here: a repeated host would otherwise be upserted twice per
	// reconcile, and since only one entry survives in the host -> id map the
	// extra Kuma monitor would be leaked on every pass.
	seen := map[string]bool{}
	var out []DesiredMonitor
	for _, hostname := range route.Spec.Hostnames {
		host := string(hostname)
		if host == "" || seen[host] {
			continue
		}
		seen[host] = true

		name := ov.Name
		if name == "" {
			name = fmt.Sprintf("%s/%s/%s", route.Namespace, route.Name, host)
		}

		out = append(out, DesiredMonitor{
			Host: host,
			Spec: httpMonitorSpec(name, scheme, host, ov),
		})
	}
	return out
}
