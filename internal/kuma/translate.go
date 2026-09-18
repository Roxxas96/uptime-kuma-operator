package kuma

import (
	"fmt"
	"reflect"

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

func equalInt64Ptr(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func equalInt64Sets(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[int64]int, len(a))
	for _, v := range a {
		seen[v]++
	}
	for _, v := range b {
		seen[v]--
	}
	for _, count := range seen {
		if count != 0 {
			return false
		}
	}
	return true
}

// oauthAuthMethodFor returns the OAuth2 client-authentication method Kuma
// requires alongside AuthMethod "oauth2-cc". Kuma exposes at most two
// values here and the operator doesn't let users choose between them today
// (no request for it exists) — client_secret_basic matches what a freshly
// created Kuma monitor defaults to. breml's HTTP.MarshalJSON always emits
// the oauth_auth_method key, so leaving it unset would send "", which
// Kuma's OAuth2-CC form rejects.
func oauthAuthMethodFor(authMethod string) string {
	if authMethod == "oauth2-cc" {
		return "client_secret_basic"
	}
	return ""
}

// ToBremlMonitor converts spec into the concrete breml monitor.Monitor
// implementation for its type. id is the existing Kuma monitor ID (0 for a
// not-yet-created monitor).
func ToBremlMonitor(id int64, spec MonitorSpec) (bremlmonitor.Monitor, error) {
	spec = normalizeSpec(spec)
	base := bremlmonitor.Base{
		ID:              id,
		Name:            spec.Name,
		Interval:        spec.Interval,
		RetryInterval:   spec.RetryInterval,
		MaxRetries:      spec.MaxRetries,
		ResendInterval:  spec.ResendInterval,
		UpsideDown:      spec.UpsideDown,
		NotificationIDs: spec.NotificationIDs,
		Parent:          spec.GroupID,
		ProxyID:         spec.ProxyID,
		IsActive:        true,
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
				URL:                      spec.HTTP.URL,
				Method:                   spec.HTTP.Method,
				AcceptedStatusCodes:      spec.HTTP.AcceptedStatusCodes,
				Timeout:                  spec.HTTP.Timeout,
				MaxRedirects:             spec.HTTP.MaxRedirects,
				IgnoreTLS:                spec.HTTP.IgnoreTLS,
				CacheBust:                spec.HTTP.CacheBust,
				ExpiryNotification:       spec.HTTP.ExpiryNotification,
				DomainExpiryNotification: spec.HTTP.DomainExpiryNotification,
				Headers:                  spec.HTTP.Headers,
				Body:                     spec.HTTP.Body,
				AuthMethod:               bremlmonitor.AuthMethod(spec.HTTP.AuthMethod),
				BasicAuthUser:            spec.HTTP.BasicAuthUsername,
				BasicAuthPass:            spec.HTTP.BasicAuthPassword,
				BearerToken:              spec.HTTP.BearerToken,
				OAuthClientID:            spec.HTTP.OAuthClientID,
				OAuthClientSecret:        spec.HTTP.OAuthClientSecret,
				OAuthTokenURL:            spec.HTTP.OAuthTokenURL,
				OAuthScopes:              spec.HTTP.OAuthScopes,
				OAuthAudience:            spec.HTTP.OAuthAudience,
				OAuthAuthMethod:          oauthAuthMethodFor(spec.HTTP.AuthMethod),
			},
		}, nil

	case TypeTCP:
		if spec.TCP == nil {
			return nil, fmt.Errorf("kuma: monitor type %s requires the TCP field to be set", TypeTCP)
		}
		tlsMode := spec.TCP.TLSMode
		expectedAlert := spec.TCP.ExpectedSSLAlert
		details := bremlmonitor.TCPPortDetails{
			Hostname:                 spec.TCP.Host,
			Port:                     spec.TCP.Port,
			ExpiryNotification:       spec.TCP.ExpiryNotification,
			DomainExpiryNotification: spec.TCP.DomainExpiryNotification,
		}
		if tlsMode != "" {
			details.SMTPSecurity = &tlsMode
		}
		if expectedAlert != "" {
			details.ExpectedTLSAlert = &expectedAlert
		}
		return &bremlmonitor.TCPPort{Base: base, TCPPortDetails: details}, nil

	case TypePing:
		if spec.Ping == nil {
			return nil, fmt.Errorf("kuma: monitor type %s requires the Ping field to be set", TypePing)
		}
		timeout := spec.Ping.Timeout
		details := bremlmonitor.PingDetails{
			Hostname:                 spec.Ping.Host,
			PacketSize:               spec.Ping.PacketSize,
			DomainExpiryNotification: spec.Ping.DomainExpiryNotification,
		}
		if timeout != 0 {
			details.Timeout = &timeout
		}
		return &bremlmonitor.Ping{Base: base, PingDetails: details}, nil

	case TypeDNS:
		if spec.DNS == nil {
			return nil, fmt.Errorf("kuma: monitor type %s requires the DNS field to be set", TypeDNS)
		}
		return &bremlmonitor.DNS{
			Base: base,
			DNSDetails: bremlmonitor.DNSDetails{
				Hostname:                 spec.DNS.Host,
				ResolverServer:           spec.DNS.ResolverServer,
				ResolveType:              bremlmonitor.DNSResolveType(spec.DNS.ResolveType),
				Port:                     spec.DNS.Port,
				DomainExpiryNotification: spec.DNS.DomainExpiryNotification,
			},
		}, nil

	case TypeGamedig:
		if spec.Gamedig == nil {
			return nil, fmt.Errorf("kuma: monitor type %s requires the Gamedig field to be set", TypeGamedig)
		}
		details := bremlmonitor.GameDigDetails{
			Hostname:                 spec.Gamedig.Host,
			Port:                     spec.Gamedig.Port,
			Game:                     spec.Gamedig.Game,
			GameDigGivenPortOnly:     spec.Gamedig.GivenPortOnly,
			DomainExpiryNotification: spec.Gamedig.DomainExpiryNotification,
		}
		if spec.Gamedig.Token != "" {
			token := spec.Gamedig.Token
			details.GameDigToken = &token
		}
		return &bremlmonitor.GameDig{Base: base, GameDigDetails: details}, nil

	default:
		return nil, fmt.Errorf("kuma: unknown monitor type %q", spec.Type)
	}
}

// FromBremlMonitor converts a monitor fetched from Kuma back into the
// operator's own MonitorSpec representation, for drift comparison against
// desired configuration via Equivalent. It populates only the fields
// ToBremlMonitor sets. Tags are the one operator-managed field NOT
// represented here or in Equivalent — Kuma manages monitor-tag
// associations via separate API calls outside a monitor's own
// create/update payload, so they're reconciled by syncTags
// (internal/controller/sync.go) instead, on every successful sync.
func FromBremlMonitor(base bremlmonitor.Base) (MonitorSpec, error) {
	spec := MonitorSpec{
		Name:            base.Name,
		Interval:        base.Interval,
		RetryInterval:   base.RetryInterval,
		MaxRetries:      base.MaxRetries,
		ResendInterval:  base.ResendInterval,
		UpsideDown:      base.UpsideDown,
		NotificationIDs: base.NotificationIDs,
		GroupID:         base.Parent,
		ProxyID:         base.ProxyID,
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
		spec.HTTP = &HTTPSpec{
			URL: d.URL, Method: d.Method, AcceptedStatusCodes: d.AcceptedStatusCodes,
			Timeout: d.Timeout, MaxRedirects: d.MaxRedirects, IgnoreTLS: d.IgnoreTLS,
			CacheBust: d.CacheBust, ExpiryNotification: d.ExpiryNotification,
			DomainExpiryNotification: d.DomainExpiryNotification, Headers: d.Headers, Body: d.Body,
			AuthMethod: string(d.AuthMethod), BasicAuthUsername: d.BasicAuthUser, BasicAuthPassword: d.BasicAuthPass,
			BearerToken: d.BearerToken, OAuthClientID: d.OAuthClientID, OAuthClientSecret: d.OAuthClientSecret,
			OAuthTokenURL: d.OAuthTokenURL, OAuthScopes: d.OAuthScopes, OAuthAudience: d.OAuthAudience,
		}
	case "port":
		spec.Type = TypeTCP
		var d bremlmonitor.TCPPort
		if err := base.As(&d); err != nil {
			return MonitorSpec{}, fmt.Errorf("kuma: decode TCP monitor %d: %w", base.GetID(), err)
		}
		tcp := &TCPSpec{
			Host: d.Hostname, Port: d.Port,
			ExpiryNotification: d.ExpiryNotification, DomainExpiryNotification: d.DomainExpiryNotification,
		}
		if d.SMTPSecurity != nil {
			tcp.TLSMode = *d.SMTPSecurity
		}
		if d.ExpectedTLSAlert != nil {
			tcp.ExpectedSSLAlert = *d.ExpectedTLSAlert
		}
		spec.TCP = tcp
	case "ping":
		spec.Type = TypePing
		var d bremlmonitor.Ping
		if err := base.As(&d); err != nil {
			return MonitorSpec{}, fmt.Errorf("kuma: decode Ping monitor %d: %w", base.GetID(), err)
		}
		ping := &PingSpec{
			Host: d.Hostname, PacketSize: d.PacketSize, DomainExpiryNotification: d.DomainExpiryNotification,
		}
		if d.Timeout != nil {
			ping.Timeout = *d.Timeout
		}
		spec.Ping = ping
	case "dns":
		spec.Type = TypeDNS
		var d bremlmonitor.DNS
		if err := base.As(&d); err != nil {
			return MonitorSpec{}, fmt.Errorf("kuma: decode DNS monitor %d: %w", base.GetID(), err)
		}
		spec.DNS = &DNSSpec{
			Host: d.Hostname, ResolverServer: d.ResolverServer, ResolveType: string(d.ResolveType), Port: d.Port,
			DomainExpiryNotification: d.DomainExpiryNotification,
		}
	case "gamedig":
		spec.Type = TypeGamedig
		var d bremlmonitor.GameDig
		if err := base.As(&d); err != nil {
			return MonitorSpec{}, fmt.Errorf("kuma: decode Gamedig monitor %d: %w", base.GetID(), err)
		}
		gamedig := &GamedigSpec{
			Host: d.Hostname, Port: d.Port, Game: d.Game,
			GivenPortOnly: d.GameDigGivenPortOnly, DomainExpiryNotification: d.DomainExpiryNotification,
		}
		if d.GameDigToken != nil {
			gamedig.Token = *d.GameDigToken
		}
		spec.Gamedig = gamedig
	default:
		return MonitorSpec{}, fmt.Errorf("kuma: unknown live monitor type %q (id %d)", base.Type(), base.GetID())
	}

	return spec, nil
}

// Equivalent reports whether desired and live describe the same monitor
// configuration, considering only the fields the operator manages (Name,
// Interval, RetryInterval, MaxRetries, Description, ResendInterval, UpsideDown,
// NotificationIDs, GroupID, ProxyID, and the type-specific fields). Tags are
// the one operator-managed field intentionally excluded here — see
// FromBremlMonitor's doc comment for why. Both sides are normalized first,
// so a spec that leaves optional fields unset still compares equal to one
// that spells out the same default explicitly — which is what Kuma's live
// spec always does, never leaving a field unset.
func Equivalent(desired, live MonitorSpec) bool {
	d := normalizeSpec(desired)
	live = normalizeSpec(live)
	if d.Type != live.Type || d.Name != live.Name || d.Interval != live.Interval ||
		d.RetryInterval != live.RetryInterval || d.MaxRetries != live.MaxRetries ||
		d.Description != live.Description || d.ResendInterval != live.ResendInterval ||
		d.UpsideDown != live.UpsideDown ||
		!equalInt64Sets(d.NotificationIDs, live.NotificationIDs) ||
		!equalInt64Ptr(d.GroupID, live.GroupID) ||
		!equalInt64Ptr(d.ProxyID, live.ProxyID) {
		return false
	}
	switch d.Type {
	case TypeHTTP:
		// HTTPSpec contains a slice field (AcceptedStatusCodes), so the
		// struct type is never comparable with == (a static Go restriction,
		// regardless of the slice's runtime value) — reflect.DeepEqual is
		// used instead so a future field added to HTTPSpec is automatically
		// covered here rather than requiring a hand-maintained && chain.
		return d.HTTP != nil && live.HTTP != nil && reflect.DeepEqual(*d.HTTP, *live.HTTP)
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
