package derive

import (
	"fmt"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"uptime-kuma-operator/internal/annotations"
	"uptime-kuma-operator/internal/kuma"
)

// HTTPRouteMonitors derives one DesiredMonitor per hostname in
// route.Spec.Hostnames. Scheme defaults to "https" (HTTPRoute carries no
// TLS info of its own — see design spec); ov.Scheme overrides it.
func HTTPRouteMonitors(route *gatewayv1.HTTPRoute, ov annotations.Overrides) []DesiredMonitor {
	scheme := "https"
	if ov.Scheme != "" {
		scheme = ov.Scheme
	}

	var out []DesiredMonitor
	for _, hostname := range route.Spec.Hostnames {
		host := string(hostname)

		name := ov.Name
		if name == "" {
			name = fmt.Sprintf("%s/%s/%s", route.Namespace, route.Name, host)
		}

		out = append(out, DesiredMonitor{
			Host: host,
			Spec: kuma.MonitorSpec{
				Type:          kuma.TypeHTTP,
				Name:          name,
				Interval:      ov.Interval,
				RetryInterval: ov.RetryInterval,
				MaxRetries:    ov.MaxRetries,
				HTTP: &kuma.HTTPSpec{
					URL:                 fmt.Sprintf("%s://%s/", scheme, host),
					AcceptedStatusCodes: ov.AcceptedStatusCodes,
				},
			},
		})
	}
	return out
}
