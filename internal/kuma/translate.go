package kuma

import (
	"fmt"

	bremlmonitor "github.com/breml/go-uptime-kuma-client/monitor"
)

// ToBremlMonitor converts spec into the concrete breml monitor.Monitor
// implementation for its type. id is the existing Kuma monitor ID (0 for a
// not-yet-created monitor).
func ToBremlMonitor(id int64, spec MonitorSpec) (bremlmonitor.Monitor, error) {
	base := bremlmonitor.Base{
		ID:            id,
		Name:          spec.Name,
		Interval:      orDefault(spec.Interval, 60),
		RetryInterval: orDefault(spec.RetryInterval, 60),
		MaxRetries:    spec.MaxRetries,
		IsActive:      true,
	}

	switch spec.Type {
	case TypeHTTP:
		if spec.HTTP == nil {
			return nil, fmt.Errorf("kuma: monitor type %s requires the HTTP field to be set", TypeHTTP)
		}
		codes := spec.HTTP.AcceptedStatusCodes
		if len(codes) == 0 {
			codes = []string{"200-299"}
		}
		method := spec.HTTP.Method
		if method == "" {
			method = "GET"
		}
		return &bremlmonitor.HTTP{
			Base: base,
			HTTPDetails: bremlmonitor.HTTPDetails{
				URL:                 spec.HTTP.URL,
				Method:              method,
				AcceptedStatusCodes: codes,
			},
		}, nil

	case TypeTCP:
		if spec.TCP == nil {
			return nil, fmt.Errorf("kuma: monitor type %s requires the TCP field to be set", TypeTCP)
		}
		return &bremlmonitor.TCPPort{
			Base: base,
			TCPPortDetails: bremlmonitor.TCPPortDetails{
				Hostname: spec.TCP.Host,
				Port:     spec.TCP.Port,
			},
		}, nil

	case TypePing:
		if spec.Ping == nil {
			return nil, fmt.Errorf("kuma: monitor type %s requires the Ping field to be set", TypePing)
		}
		return &bremlmonitor.Ping{
			Base: base,
			PingDetails: bremlmonitor.PingDetails{
				Hostname: spec.Ping.Host,
			},
		}, nil

	case TypeDNS:
		if spec.DNS == nil {
			return nil, fmt.Errorf("kuma: monitor type %s requires the DNS field to be set", TypeDNS)
		}
		resolveType := spec.DNS.ResolveType
		if resolveType == "" {
			resolveType = "A"
		}
		return &bremlmonitor.DNS{
			Base: base,
			DNSDetails: bremlmonitor.DNSDetails{
				Hostname:       spec.DNS.Host,
				ResolverServer: spec.DNS.ResolverServer,
				ResolveType:    bremlmonitor.DNSResolveType(resolveType),
				Port:           spec.DNS.Port,
			},
		}, nil

	case TypeGamedig:
		if spec.Gamedig == nil {
			return nil, fmt.Errorf("kuma: monitor type %s requires the Gamedig field to be set", TypeGamedig)
		}
		return &bremlmonitor.GameDig{
			Base: base,
			GameDigDetails: bremlmonitor.GameDigDetails{
				Hostname: spec.Gamedig.Host,
				Port:     spec.Gamedig.Port,
				Game:     spec.Gamedig.Game,
			},
		}, nil

	default:
		return nil, fmt.Errorf("kuma: unknown monitor type %q", spec.Type)
	}
}

func orDefault(v, def int64) int64 {
	if v == 0 {
		return def
	}
	return v
}
