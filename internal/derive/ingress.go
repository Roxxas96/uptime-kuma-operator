package derive

import (
	"fmt"

	networkingv1 "k8s.io/api/networking/v1"

	"uptime-kuma-operator/internal/annotations"
	"uptime-kuma-operator/internal/kuma"
)

// DesiredMonitor pairs a host with the kuma.MonitorSpec it should map to.
type DesiredMonitor struct {
	Host string
	Spec kuma.MonitorSpec
}

// IngressMonitors derives one DesiredMonitor per distinct, non-empty host
// in ing.Spec.Rules. Scheme is "https" if the host is listed under
// ing.Spec.TLS, else "http"; ov.Scheme, when set, overrides that.
func IngressMonitors(ing *networkingv1.Ingress, ov annotations.Overrides) []DesiredMonitor {
	tlsHosts := map[string]bool{}
	for _, t := range ing.Spec.TLS {
		for _, h := range t.Hosts {
			tlsHosts[h] = true
		}
	}

	seen := map[string]bool{}
	var out []DesiredMonitor
	for _, rule := range ing.Spec.Rules {
		if rule.Host == "" || seen[rule.Host] {
			continue
		}
		seen[rule.Host] = true

		scheme := "http"
		if tlsHosts[rule.Host] {
			scheme = "https"
		}
		if ov.Scheme != "" {
			scheme = ov.Scheme
		}

		path := "/"
		if ov.Path != "" {
			path = ov.Path
		}

		name := ov.Name
		if name == "" {
			name = fmt.Sprintf("%s/%s/%s", ing.Namespace, ing.Name, rule.Host)
		}

		out = append(out, DesiredMonitor{
			Host: rule.Host,
			Spec: kuma.MonitorSpec{
				Type:           kuma.TypeHTTP,
				Name:           name,
				Interval:       ov.Interval,
				RetryInterval:  ov.RetryInterval,
				MaxRetries:     ov.MaxRetries,
				Description:    ov.Description,
				ResendInterval: ov.ResendInterval,
				UpsideDown:     ov.UpsideDown,
				HTTP: &kuma.HTTPSpec{
					URL:                      fmt.Sprintf("%s://%s%s", scheme, rule.Host, path),
					AcceptedStatusCodes:      ov.AcceptedStatusCodes,
					Timeout:                  ov.Timeout,
					MaxRedirects:             int(ov.MaxRedirects),
					IgnoreTLS:                ov.IgnoreTLS,
					CacheBust:                ov.CacheBust,
					ExpiryNotification:       ov.ExpiryNotification,
					DomainExpiryNotification: ov.DomainExpiryNotification,
					Headers:                  ov.Headers,
					Body:                     ov.Body,
				},
			},
		})
	}
	return out
}
