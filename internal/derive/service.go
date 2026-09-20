package derive

import (
	"fmt"
	"strconv"

	corev1 "k8s.io/api/core/v1"

	"uptime-kuma-operator/internal/annotations"
	"uptime-kuma-operator/internal/kuma"
)

// ServiceMonitors derives the single DesiredMonitor for svc, of the Kuma
// type ov.Type selects (defaulting to HTTP). Unlike Ingress/HTTPRoute,
// which derive one monitor per routing-rule host, a Service carries no
// routing information of its own — its check target is either the
// in-cluster DNS name or an explicit <type>/host override, and (for
// HTTP/TCP/Gamedig) a Service port resolved via resolveServicePort.
func ServiceMonitors(svc *corev1.Service, ov annotations.Overrides) ([]DesiredMonitor, error) {
	monType := ov.Type
	if monType == "" {
		monType = "HTTP"
	}

	name := ov.Name
	if name == "" {
		name = fmt.Sprintf("%s/%s", svc.Namespace, svc.Name)
	}

	spec := kuma.MonitorSpec{
		Name:           name,
		Interval:       ov.Interval,
		RetryInterval:  ov.RetryInterval,
		MaxRetries:     ov.MaxRetries,
		Description:    ov.Description,
		ResendInterval: ov.ResendInterval,
		UpsideDown:     ov.UpsideDown,
	}
	if ov.Proxy != 0 {
		spec.ProxyID = &ov.Proxy
	}

	var host string
	switch monType {
	case "HTTP":
		spec.Type = kuma.TypeHTTP
		host = defaultHost(svc, ov.HTTP.Host)
		port, err := resolveServicePort(svc, ov.HTTP.Port)
		if err != nil {
			return nil, err
		}
		scheme := "http"
		if ov.HTTP.Scheme != "" {
			scheme = ov.HTTP.Scheme
		}
		path := "/"
		if ov.HTTP.Path != "" {
			path = ov.HTTP.Path
		}
		spec.HTTP = &kuma.HTTPSpec{
			URL:                      fmt.Sprintf("%s://%s:%d%s", scheme, host, port, path),
			AcceptedStatusCodes:      ov.HTTP.AcceptedStatusCodes,
			Timeout:                  ov.HTTP.Timeout,
			MaxRedirects:             int(ov.HTTP.MaxRedirects),
			IgnoreTLS:                ov.HTTP.IgnoreTLS,
			CacheBust:                ov.HTTP.CacheBust,
			ExpiryNotification:       ov.HTTP.ExpiryNotification,
			DomainExpiryNotification: ov.HTTP.DomainExpiryNotification,
			Headers:                  ov.HTTP.Headers,
			Body:                     ov.HTTP.Body,
		}

	case "TCP":
		spec.Type = kuma.TypeTCP
		host = defaultHost(svc, ov.TCP.Host)
		port, err := resolveServicePort(svc, ov.TCP.Port)
		if err != nil {
			return nil, err
		}
		spec.TCP = &kuma.TCPSpec{
			Host: host, Port: int(port),
			TLSMode:                  ov.TCP.TLSMode,
			ExpectedSSLAlert:         ov.TCP.ExpectedSSLAlert,
			ExpiryNotification:       ov.TCP.ExpiryNotification,
			DomainExpiryNotification: ov.TCP.DomainExpiryNotification,
		}

	case "Ping":
		spec.Type = kuma.TypePing
		host = defaultHost(svc, ov.Ping.Host)
		spec.Ping = &kuma.PingSpec{
			Host: host, Timeout: ov.Ping.Timeout, PacketSize: int(ov.Ping.PacketSize),
			DomainExpiryNotification: ov.Ping.DomainExpiryNotification,
		}

	case "DNS":
		spec.Type = kuma.TypeDNS
		host = defaultHost(svc, ov.DNS.Host)
		spec.DNS = &kuma.DNSSpec{
			Host: host, ResolverServer: ov.DNS.ResolverServer, ResolveType: ov.DNS.ResolveType,
			Port:                     int(ov.DNS.Port),
			DomainExpiryNotification: ov.DNS.DomainExpiryNotification,
		}

	case "Gamedig":
		if ov.Gamedig.Game == "" {
			return nil, fmt.Errorf("service: %s is required when type is Gamedig", annotations.GamedigGame)
		}
		spec.Type = kuma.TypeGamedig
		host = defaultHost(svc, ov.Gamedig.Host)
		port, err := resolveServicePort(svc, ov.Gamedig.Port)
		if err != nil {
			return nil, err
		}
		spec.Gamedig = &kuma.GamedigSpec{
			Host: host, Port: int(port), Game: ov.Gamedig.Game,
			GivenPortOnly:            ov.Gamedig.GivenPortOnly,
			DomainExpiryNotification: ov.Gamedig.DomainExpiryNotification,
		}

	default:
		return nil, fmt.Errorf("service: unknown %s %q", annotations.Type, monType)
	}

	return []DesiredMonitor{{Host: host, Spec: spec}}, nil
}

// defaultHost returns override if set, else the Service's in-cluster DNS name.
func defaultHost(svc *corev1.Service, override string) string {
	if override != "" {
		return override
	}
	return fmt.Sprintf("%s.%s.svc.cluster.local", svc.Name, svc.Namespace)
}

// resolveServicePort picks which of svc's ports to check. An empty override
// auto-picks the Service's only port and errors if there is none or more
// than one; a non-empty override is matched first as a literal port
// number, then against a port's .Name.
func resolveServicePort(svc *corev1.Service, override string) (int32, error) {
	if override == "" {
		switch len(svc.Spec.Ports) {
		case 0:
			return 0, fmt.Errorf("service: %s/%s has no ports", svc.Namespace, svc.Name)
		case 1:
			return svc.Spec.Ports[0].Port, nil
		default:
			return 0, fmt.Errorf("service: %s/%s exposes %d ports, a port annotation is required to pick one",
				svc.Namespace, svc.Name, len(svc.Spec.Ports))
		}
	}

	if n, err := strconv.ParseInt(override, 10, 32); err == nil {
		for _, p := range svc.Spec.Ports {
			if int64(p.Port) == n {
				return p.Port, nil
			}
		}
		return 0, fmt.Errorf("service: %s/%s has no port %d", svc.Namespace, svc.Name, n)
	}

	for _, p := range svc.Spec.Ports {
		if p.Name == override {
			return p.Port, nil
		}
	}
	return 0, fmt.Errorf("service: %s/%s has no port named %q", svc.Namespace, svc.Name, override)
}
