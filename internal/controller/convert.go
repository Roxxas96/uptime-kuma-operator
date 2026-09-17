package controller

import (
	uptimekumaiov1alpha1 "uptime-kuma-operator/api/v1alpha1"
	"uptime-kuma-operator/internal/kuma"
)

// toKumaSpec converts the Monitor CRD's typed spec into the operator's
// internal kuma.MonitorSpec, which is what kuma.Client operates on.
func toKumaSpec(spec uptimekumaiov1alpha1.MonitorSpec) kuma.MonitorSpec {
	out := kuma.MonitorSpec{
		Type:           kuma.MonitorType(spec.Type),
		Name:           spec.Name,
		Interval:       spec.Interval,
		RetryInterval:  spec.RetryInterval,
		MaxRetries:     spec.Retries,
		Description:    spec.Description,
		ResendInterval: spec.ResendInterval,
		UpsideDown:     spec.UpsideDown,
	}
	if spec.Proxy != 0 {
		out.ProxyID = &spec.Proxy
	}

	if spec.HTTP != nil {
		out.HTTP = &kuma.HTTPSpec{
			URL:                      spec.HTTP.URL,
			Method:                   spec.HTTP.Method,
			AcceptedStatusCodes:      spec.HTTP.AcceptedStatusCodes,
			Timeout:                  spec.HTTP.Timeout,
			MaxRedirects:             int(spec.HTTP.MaxRedirects),
			IgnoreTLS:                spec.HTTP.IgnoreTLS,
			CacheBust:                spec.HTTP.CacheBust,
			ExpiryNotification:       spec.HTTP.ExpiryNotification,
			DomainExpiryNotification: spec.HTTP.DomainExpiryNotification,
			Headers:                  spec.HTTP.Headers,
			Body:                     spec.HTTP.Body,
		}
	}
	if spec.TCP != nil {
		out.TCP = &kuma.TCPSpec{
			Host: spec.TCP.Host, Port: int(spec.TCP.Port),
			TLSMode: spec.TCP.TLSMode, ExpectedSSLAlert: spec.TCP.ExpectedSSLAlert,
			ExpiryNotification:       spec.TCP.ExpiryNotification,
			DomainExpiryNotification: spec.TCP.DomainExpiryNotification,
		}
	}
	if spec.Ping != nil {
		out.Ping = &kuma.PingSpec{
			Host: spec.Ping.Host, Timeout: spec.Ping.Timeout, PacketSize: int(spec.Ping.PacketSize),
			DomainExpiryNotification: spec.Ping.DomainExpiryNotification,
		}
	}
	if spec.DNS != nil {
		out.DNS = &kuma.DNSSpec{
			Host: spec.DNS.Host, ResolverServer: spec.DNS.ResolverServer,
			ResolveType: spec.DNS.ResolveType, Port: int(spec.DNS.Port),
			DomainExpiryNotification: spec.DNS.DomainExpiryNotification,
		}
	}
	if spec.Gamedig != nil {
		out.Gamedig = &kuma.GamedigSpec{
			Host: spec.Gamedig.Host, Port: int(spec.Gamedig.Port), Game: spec.Gamedig.Game,
			GivenPortOnly:            spec.Gamedig.GivenPortOnly,
			DomainExpiryNotification: spec.Gamedig.DomainExpiryNotification,
		}
	}
	return out
}
