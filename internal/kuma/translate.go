package kuma

import (
	"fmt"
	"slices"

	bremlmonitor "github.com/breml/go-uptime-kuma-client/monitor"
)

// normalizeSpec fills in the defaults Kuma applies when a field is left
// unset, so a desired spec that omits optional fields compares equal to
// Kuma's live configuration — which always reflects the default actually
// applied, never "unset". ToBremlMonitor and Equivalent both build on this
// so the default values live in exactly one place.
func normalizeSpec(spec MonitorSpec) MonitorSpec {
	spec.Interval = orDefault(spec.Interval, 60)
	spec.RetryInterval = orDefault(spec.RetryInterval, 60)

	switch spec.Type {
	case TypeHTTP:
		if spec.HTTP != nil {
			h := *spec.HTTP
			if len(h.AcceptedStatusCodes) == 0 {
				h.AcceptedStatusCodes = []string{"200-299"}
			}
			if h.Method == "" {
				h.Method = "GET"
			}
			spec.HTTP = &h
		}
	case TypeDNS:
		if spec.DNS != nil {
			d := *spec.DNS
			if d.ResolveType == "" {
				d.ResolveType = "A"
			}
			spec.DNS = &d
		}
	}
	return spec
}

func orDefault(v, def int64) int64 {
	if v == 0 {
		return def
	}
	return v
}

// ToBremlMonitor converts spec into the concrete breml monitor.Monitor
// implementation for its type. id is the existing Kuma monitor ID (0 for a
// not-yet-created monitor).
func ToBremlMonitor(id int64, spec MonitorSpec) (bremlmonitor.Monitor, error) {
	spec = normalizeSpec(spec)
	base := bremlmonitor.Base{
		ID:             id,
		Name:           spec.Name,
		Interval:       spec.Interval,
		RetryInterval:  spec.RetryInterval,
		MaxRetries:     spec.MaxRetries,
		ResendInterval: spec.ResendInterval,
		UpsideDown:     spec.UpsideDown,
		IsActive:       true,
	}
	if spec.Description != "" {
		base.Description = &spec.Description
	}

	switch spec.Type {
	case TypeHTTP:
		if spec.HTTP == nil {
			return nil, fmt.Errorf("kuma: monitor type %s requires the HTTP field to be set", TypeHTTP)
		}
		return &bremlmonitor.HTTP{
			Base: base,
			HTTPDetails: bremlmonitor.HTTPDetails{
				URL:                 spec.HTTP.URL,
				Method:              spec.HTTP.Method,
				AcceptedStatusCodes: spec.HTTP.AcceptedStatusCodes,
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
		return &bremlmonitor.DNS{
			Base: base,
			DNSDetails: bremlmonitor.DNSDetails{
				Hostname:       spec.DNS.Host,
				ResolverServer: spec.DNS.ResolverServer,
				ResolveType:    bremlmonitor.DNSResolveType(spec.DNS.ResolveType),
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

// FromBremlMonitor converts a monitor fetched from Kuma back into the
// operator's own MonitorSpec representation, for drift comparison against
// desired configuration via Equivalent. It populates only the fields
// ToBremlMonitor sets — tags, notifications, description, active state,
// and every other Kuma-side field are left for the user to manage directly
// in Kuma and are never compared or overwritten by the operator.
func FromBremlMonitor(base bremlmonitor.Base) (MonitorSpec, error) {
	spec := MonitorSpec{
		Name:           base.Name,
		Interval:       base.Interval,
		RetryInterval:  base.RetryInterval,
		MaxRetries:     base.MaxRetries,
		ResendInterval: base.ResendInterval,
		UpsideDown:     base.UpsideDown,
	}
	if base.Description != nil {
		spec.Description = *base.Description
	}

	switch base.Type() {
	case "http":
		spec.Type = TypeHTTP
		var d bremlmonitor.HTTP
		if err := base.As(&d); err != nil {
			return MonitorSpec{}, fmt.Errorf("kuma: decode HTTP monitor %d: %w", base.GetID(), err)
		}
		spec.HTTP = &HTTPSpec{URL: d.URL, Method: d.Method, AcceptedStatusCodes: d.AcceptedStatusCodes}
	case "port":
		spec.Type = TypeTCP
		var d bremlmonitor.TCPPort
		if err := base.As(&d); err != nil {
			return MonitorSpec{}, fmt.Errorf("kuma: decode TCP monitor %d: %w", base.GetID(), err)
		}
		spec.TCP = &TCPSpec{Host: d.Hostname, Port: d.Port}
	case "ping":
		spec.Type = TypePing
		var d bremlmonitor.Ping
		if err := base.As(&d); err != nil {
			return MonitorSpec{}, fmt.Errorf("kuma: decode Ping monitor %d: %w", base.GetID(), err)
		}
		spec.Ping = &PingSpec{Host: d.Hostname}
	case "dns":
		spec.Type = TypeDNS
		var d bremlmonitor.DNS
		if err := base.As(&d); err != nil {
			return MonitorSpec{}, fmt.Errorf("kuma: decode DNS monitor %d: %w", base.GetID(), err)
		}
		spec.DNS = &DNSSpec{Host: d.Hostname, ResolverServer: d.ResolverServer, ResolveType: string(d.ResolveType), Port: d.Port}
	case "gamedig":
		spec.Type = TypeGamedig
		var d bremlmonitor.GameDig
		if err := base.As(&d); err != nil {
			return MonitorSpec{}, fmt.Errorf("kuma: decode Gamedig monitor %d: %w", base.GetID(), err)
		}
		spec.Gamedig = &GamedigSpec{Host: d.Hostname, Port: d.Port, Game: d.Game}
	default:
		return MonitorSpec{}, fmt.Errorf("kuma: unknown live monitor type %q (id %d)", base.Type(), base.GetID())
	}

	return spec, nil
}

// Equivalent reports whether desired and live describe the same monitor
// configuration, considering only the fields the operator manages (Name,
// Interval, RetryInterval, MaxRetries, and the type-specific fields) — not
// every field Kuma tracks (tags, notifications, description, active state,
// etc. are left for the user to manage directly in Kuma). Both sides are
// normalized first, so a spec that leaves optional fields unset still
// compares equal to one that spells out the same default explicitly —
// which is what Kuma's live spec always does, never leaving a field unset.
func Equivalent(desired, live MonitorSpec) bool {
	d := normalizeSpec(desired)
	live = normalizeSpec(live)
	if d.Type != live.Type || d.Name != live.Name || d.Interval != live.Interval ||
		d.RetryInterval != live.RetryInterval || d.MaxRetries != live.MaxRetries ||
		d.Description != live.Description || d.ResendInterval != live.ResendInterval ||
		d.UpsideDown != live.UpsideDown {
		return false
	}
	switch d.Type {
	case TypeHTTP:
		return d.HTTP != nil && live.HTTP != nil &&
			d.HTTP.URL == live.HTTP.URL &&
			d.HTTP.Method == live.HTTP.Method &&
			slices.Equal(d.HTTP.AcceptedStatusCodes, live.HTTP.AcceptedStatusCodes)
	case TypeTCP:
		return d.TCP != nil && live.TCP != nil && *d.TCP == *live.TCP
	case TypePing:
		return d.Ping != nil && live.Ping != nil && *d.Ping == *live.Ping
	case TypeDNS:
		return d.DNS != nil && live.DNS != nil && *d.DNS == *live.DNS
	case TypeGamedig:
		return d.Gamedig != nil && live.Gamedig != nil && *d.Gamedig == *live.Gamedig
	default:
		return false
	}
}
