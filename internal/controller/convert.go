package controller

import (
	uptimekumaiov1alpha1 "uptime-kuma-operator/api/v1alpha1"
	"uptime-kuma-operator/internal/kuma"
)

// toKumaSpec converts the Monitor CRD's typed spec into the operator's
// internal kuma.MonitorSpec, which is what kuma.Client operates on.
func toKumaSpec(spec uptimekumaiov1alpha1.MonitorSpec) kuma.MonitorSpec {
	out := kuma.MonitorSpec{
		Type:          kuma.MonitorType(spec.Type),
		Name:          spec.Name,
		Interval:      spec.Interval,
		RetryInterval: spec.RetryInterval,
		MaxRetries:    spec.Retries,
	}

	if spec.HTTP != nil {
		out.HTTP = &kuma.HTTPSpec{
			URL:                 spec.HTTP.URL,
			Method:              spec.HTTP.Method,
			AcceptedStatusCodes: spec.HTTP.AcceptedStatusCodes,
		}
	}
	if spec.TCP != nil {
		out.TCP = &kuma.TCPSpec{Host: spec.TCP.Host, Port: int(spec.TCP.Port)}
	}
	if spec.Ping != nil {
		out.Ping = &kuma.PingSpec{Host: spec.Ping.Host}
	}
	if spec.DNS != nil {
		out.DNS = &kuma.DNSSpec{
			Host:           spec.DNS.Host,
			ResolverServer: spec.DNS.ResolverServer,
			ResolveType:    spec.DNS.ResolveType,
			Port:           int(spec.DNS.Port),
		}
	}
	if spec.Gamedig != nil {
		out.Gamedig = &kuma.GamedigSpec{
			Host: spec.Gamedig.Host, Port: int(spec.Gamedig.Port), Game: spec.Gamedig.Game,
		}
	}
	return out
}
