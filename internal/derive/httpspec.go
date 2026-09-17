package derive

import (
	"fmt"

	"uptime-kuma-operator/internal/annotations"
	"uptime-kuma-operator/internal/kuma"
)

// httpMonitorSpec builds the kuma.MonitorSpec common to both Ingress- and
// HTTPRoute-derived monitors: the common MonitorSpec fields plus a fully
// populated HTTPSpec (including the derived URL), given the resolved
// scheme and host and the resource's annotation overrides.
func httpMonitorSpec(name, scheme, host string, ov annotations.Overrides) kuma.MonitorSpec {
	path := "/"
	if ov.Path != "" {
		path = ov.Path
	}
	spec := kuma.MonitorSpec{
		Type:           kuma.TypeHTTP,
		Name:           name,
		Interval:       ov.Interval,
		RetryInterval:  ov.RetryInterval,
		MaxRetries:     ov.MaxRetries,
		Description:    ov.Description,
		ResendInterval: ov.ResendInterval,
		UpsideDown:     ov.UpsideDown,
		HTTP: &kuma.HTTPSpec{
			URL:                      fmt.Sprintf("%s://%s%s", scheme, host, path),
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
	}
	if ov.Proxy != 0 {
		spec.ProxyID = &ov.Proxy
	}
	return spec
}
