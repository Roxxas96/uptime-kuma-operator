package derive

import (
	"fmt"

	"uptime-kuma-operator/internal/annotations"
	"uptime-kuma-operator/internal/kuma"
)

// httpMonitorSpec builds the kuma.MonitorSpec common to both Ingress- and
// HTTPRoute-derived monitors: the common MonitorSpec fields plus a fully
// populated HTTPSpec (including the derived URL), given the resolved
// scheme and host and the resource's annotation overrides. host may
// already include a ":<port>" suffix (as Service-derived HTTP monitors
// need) — it is interpolated directly into the URL.
func httpMonitorSpec(name, scheme, host string, ov annotations.Overrides) kuma.MonitorSpec {
	path := "/"
	if ov.HTTP.Path != "" {
		path = ov.HTTP.Path
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
			AcceptedStatusCodes:      ov.HTTP.AcceptedStatusCodes,
			Timeout:                  ov.HTTP.Timeout,
			MaxRedirects:             int(ov.HTTP.MaxRedirects),
			IgnoreTLS:                ov.HTTP.IgnoreTLS,
			CacheBust:                ov.HTTP.CacheBust,
			ExpiryNotification:       ov.HTTP.ExpiryNotification,
			DomainExpiryNotification: ov.HTTP.DomainExpiryNotification,
			Headers:                  ov.HTTP.Headers,
			Body:                     ov.HTTP.Body,
		},
	}
	if ov.Proxy != 0 {
		spec.ProxyID = &ov.Proxy
	}
	return spec
}
