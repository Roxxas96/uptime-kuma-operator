package kuma

import (
	"encoding/json"
	"testing"

	bremlmonitor "github.com/breml/go-uptime-kuma-client/monitor"
)

func TestToBremlMonitor_HTTP(t *testing.T) {
	spec := MonitorSpec{
		Type: TypeHTTP,
		Name: "example",
		HTTP: &HTTPSpec{URL: "https://example.com/"},
	}

	got, err := ToBremlMonitor(0, spec)
	if err != nil {
		t.Fatalf("ToBremlMonitor: %v", err)
	}

	http, ok := got.(*bremlmonitor.HTTP)
	if !ok {
		t.Fatalf("got %T, want *monitor.HTTP", got)
	}
	if http.Name != "example" {
		t.Errorf("Name = %q, want %q", http.Name, "example")
	}
	if http.URL != "https://example.com/" {
		t.Errorf("URL = %q, want %q", http.URL, "https://example.com/")
	}
	if http.Method != "GET" {
		t.Errorf("Method = %q, want default %q", http.Method, "GET")
	}
	if len(http.AcceptedStatusCodes) != 1 || http.AcceptedStatusCodes[0] != "200-299" {
		t.Errorf("AcceptedStatusCodes = %v, want default [200-299]", http.AcceptedStatusCodes)
	}
	if http.Interval != 60 {
		t.Errorf("Interval = %d, want default 60", http.Interval)
	}
	if !http.IsActive {
		t.Error("IsActive = false, want true")
	}
}

func TestToBremlMonitor_DNS(t *testing.T) {
	spec := MonitorSpec{
		Type: TypeDNS,
		Name: "dns-check",
		DNS:  &DNSSpec{Host: "example.com", ResolverServer: "1.1.1.1", ResolveType: "A"},
	}

	got, err := ToBremlMonitor(42, spec)
	if err != nil {
		t.Fatalf("ToBremlMonitor: %v", err)
	}

	dns, ok := got.(*bremlmonitor.DNS)
	if !ok {
		t.Fatalf("got %T, want *monitor.DNS", got)
	}
	if dns.ID != 42 {
		t.Errorf("ID = %d, want 42", dns.ID)
	}
	if dns.Hostname != "example.com" || dns.ResolverServer != "1.1.1.1" || dns.ResolveType != bremlmonitor.DNSResolveTypeA {
		t.Errorf("DNS fields = %+v, want Hostname=example.com ResolverServer=1.1.1.1 ResolveType=A", dns.DNSDetails)
	}
}

func TestFromBremlMonitor_RoundTripsThroughToBremlMonitor(t *testing.T) {
	cases := []struct {
		name string
		spec MonitorSpec
	}{
		{"HTTP", MonitorSpec{Type: TypeHTTP, Name: "web", HTTP: &HTTPSpec{URL: "https://example.com/", Method: "GET", AcceptedStatusCodes: []string{"200-299"}}}},
		{"TCP", MonitorSpec{Type: TypeTCP, Name: "port", TCP: &TCPSpec{Host: "example.com", Port: 443}}},
		{"Ping", MonitorSpec{Type: TypePing, Name: "ping", Ping: &PingSpec{Host: "10.0.0.1"}}},
		{"DNS", MonitorSpec{Type: TypeDNS, Name: "dns", DNS: &DNSSpec{Host: "example.com", ResolverServer: "1.1.1.1", ResolveType: "A", Port: 53}}},
		{"Gamedig", MonitorSpec{Type: TypeGamedig, Name: "game", Gamedig: &GamedigSpec{Host: "game.example.com", Port: 27015, Game: "csgo"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mon, err := ToBremlMonitor(7, c.spec)
			if err != nil {
				t.Fatalf("ToBremlMonitor: %v", err)
			}
			// Round-trip through JSON, the same path GetMonitors uses: Base's
			// UnmarshalJSON is what populates the internal type/raw state
			// FromBremlMonitor's As() call depends on.
			data, err := json.Marshal(mon)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var base bremlmonitor.Base
			if err := json.Unmarshal(data, &base); err != nil {
				t.Fatalf("unmarshal into Base: %v", err)
			}

			got, err := FromBremlMonitor(base)
			if err != nil {
				t.Fatalf("FromBremlMonitor: %v", err)
			}
			if !Equivalent(c.spec, got) {
				t.Errorf("FromBremlMonitor(round-tripped ToBremlMonitor(%+v)) = %+v, want an equivalent spec", c.spec, got)
			}
		})
	}
}

func TestEquivalent_DetectsDrift(t *testing.T) {
	base := MonitorSpec{Type: TypeHTTP, Name: "web", HTTP: &HTTPSpec{URL: "https://example.com/", Method: "GET", AcceptedStatusCodes: []string{"200-299"}}}
	changedURL := base
	changedURL.HTTP = &HTTPSpec{URL: "https://changed.example.com/", Method: "GET", AcceptedStatusCodes: []string{"200-299"}}
	changedType := MonitorSpec{Type: TypeTCP, Name: "web", TCP: &TCPSpec{Host: "example.com", Port: 80}}

	if !Equivalent(base, base) {
		t.Error("Equivalent(base, base) = false, want true")
	}
	if Equivalent(base, changedURL) {
		t.Error("Equivalent(base, changedURL) = true, want false")
	}
	if Equivalent(base, changedType) {
		t.Error("Equivalent(base, changedType) = true, want false")
	}
}

func TestEquivalent_UnsetOptionalFieldsMatchAppliedDefaults(t *testing.T) {
	desired := MonitorSpec{Type: TypeHTTP, Name: "web", HTTP: &HTTPSpec{URL: "https://example.com/"}}
	live := MonitorSpec{Type: TypeHTTP, Name: "web", Interval: 60, RetryInterval: 60,
		HTTP: &HTTPSpec{URL: "https://example.com/", Method: "GET", AcceptedStatusCodes: []string{"200-299"}}}

	if !Equivalent(desired, live) {
		t.Error("Equivalent(desired, live) = false, want true — desired's unset optional fields should match Kuma's applied defaults")
	}
}

func TestToBremlMonitor_UnknownType(t *testing.T) {
	_, err := ToBremlMonitor(0, MonitorSpec{Type: "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown monitor type, got nil")
	}
}

func TestToBremlMonitor_MissingSubSpec(t *testing.T) {
	_, err := ToBremlMonitor(0, MonitorSpec{Type: TypeHTTP})
	if err == nil {
		t.Fatal("expected error when HTTP field is nil, got nil")
	}
}

func TestFromBremlMonitor_RoundTripsThroughToBremlMonitor_CommonFields(t *testing.T) {
	spec := MonitorSpec{
		Type: TypeHTTP, Name: "web",
		Description: "a friendly description", ResendInterval: 3, UpsideDown: true,
		HTTP: &HTTPSpec{URL: "https://example.com/", Method: "GET", AcceptedStatusCodes: []string{"200-299"}},
	}

	mon, err := ToBremlMonitor(7, spec)
	if err != nil {
		t.Fatalf("ToBremlMonitor: %v", err)
	}
	data, err := json.Marshal(mon)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var base bremlmonitor.Base
	if err := json.Unmarshal(data, &base); err != nil {
		t.Fatalf("unmarshal into Base: %v", err)
	}

	got, err := FromBremlMonitor(base)
	if err != nil {
		t.Fatalf("FromBremlMonitor: %v", err)
	}
	if !Equivalent(spec, got) {
		t.Errorf("FromBremlMonitor(round-tripped ToBremlMonitor(%+v)) = %+v, want an equivalent spec", spec, got)
	}
	if got.Description != "a friendly description" {
		t.Errorf("Description = %q, want %q", got.Description, "a friendly description")
	}
	if got.ResendInterval != 3 {
		t.Errorf("ResendInterval = %d, want 3", got.ResendInterval)
	}
	if !got.UpsideDown {
		t.Error("UpsideDown = false, want true")
	}
}

func TestEquivalent_DetectsDrift_CommonFields(t *testing.T) {
	base := MonitorSpec{Type: TypeHTTP, Name: "web", HTTP: &HTTPSpec{URL: "https://example.com/", Method: "GET", AcceptedStatusCodes: []string{"200-299"}}}

	changedDescription := base
	changedDescription.Description = "changed"
	if Equivalent(base, changedDescription) {
		t.Error("Equivalent(base, changedDescription) = true, want false")
	}

	changedResendInterval := base
	changedResendInterval.ResendInterval = 5
	if Equivalent(base, changedResendInterval) {
		t.Error("Equivalent(base, changedResendInterval) = true, want false")
	}

	changedUpsideDown := base
	changedUpsideDown.UpsideDown = true
	if Equivalent(base, changedUpsideDown) {
		t.Error("Equivalent(base, changedUpsideDown) = true, want false")
	}
}

func TestFromBremlMonitor_RoundTripsThroughToBremlMonitor_HTTPFields(t *testing.T) {
	spec := MonitorSpec{
		Type: TypeHTTP, Name: "web",
		HTTP: &HTTPSpec{
			URL: "https://example.com/", Method: "POST", AcceptedStatusCodes: []string{"200-299"},
			Timeout: 30, MaxRedirects: 5, IgnoreTLS: true, CacheBust: true,
			ExpiryNotification: true, DomainExpiryNotification: true,
			Headers: `{"X-Custom":"value"}`, Body: `{"key":"value"}`,
		},
	}

	mon, err := ToBremlMonitor(7, spec)
	if err != nil {
		t.Fatalf("ToBremlMonitor: %v", err)
	}
	data, err := json.Marshal(mon)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var base bremlmonitor.Base
	if err := json.Unmarshal(data, &base); err != nil {
		t.Fatalf("unmarshal into Base: %v", err)
	}

	got, err := FromBremlMonitor(base)
	if err != nil {
		t.Fatalf("FromBremlMonitor: %v", err)
	}
	if !Equivalent(spec, got) {
		t.Errorf("FromBremlMonitor(round-tripped ToBremlMonitor(%+v)) = %+v, want an equivalent spec", spec, got)
	}
}

func TestEquivalent_DetectsDrift_HTTPFields(t *testing.T) {
	base := MonitorSpec{Type: TypeHTTP, Name: "web", HTTP: &HTTPSpec{
		URL: "https://example.com/", Method: "GET", AcceptedStatusCodes: []string{"200-299"},
		Timeout: 30, MaxRedirects: 5,
	}}

	changedTimeout := base
	changedTimeout.HTTP = &HTTPSpec{
		URL: base.HTTP.URL, Method: base.HTTP.Method, AcceptedStatusCodes: base.HTTP.AcceptedStatusCodes,
		Timeout: 60, MaxRedirects: base.HTTP.MaxRedirects,
	}
	if Equivalent(base, changedTimeout) {
		t.Error("Equivalent(base, changedTimeout) = true, want false")
	}

	changedIgnoreTLS := base
	changedIgnoreTLS.HTTP = &HTTPSpec{
		URL: base.HTTP.URL, Method: base.HTTP.Method, AcceptedStatusCodes: base.HTTP.AcceptedStatusCodes,
		Timeout: base.HTTP.Timeout, MaxRedirects: base.HTTP.MaxRedirects, IgnoreTLS: true,
	}
	if Equivalent(base, changedIgnoreTLS) {
		t.Error("Equivalent(base, changedIgnoreTLS) = true, want false")
	}

	changedHeaders := base
	changedHeaders.HTTP = &HTTPSpec{
		URL: base.HTTP.URL, Method: base.HTTP.Method, AcceptedStatusCodes: base.HTTP.AcceptedStatusCodes,
		Timeout: base.HTTP.Timeout, MaxRedirects: base.HTTP.MaxRedirects, Headers: `{"X-Custom":"value"}`,
	}
	if Equivalent(base, changedHeaders) {
		t.Error("Equivalent(base, changedHeaders) = true, want false")
	}
}

func TestFromBremlMonitor_RoundTripsThroughToBremlMonitor_TCPFields(t *testing.T) {
	spec := MonitorSpec{
		Type: TypeTCP, Name: "port-check",
		TCP: &TCPSpec{
			Host: "example.com", Port: 443,
			TLSMode: "secure", ExpectedSSLAlert: "certificate_required",
			ExpiryNotification: true, DomainExpiryNotification: true,
		},
	}

	mon, err := ToBremlMonitor(7, spec)
	if err != nil {
		t.Fatalf("ToBremlMonitor: %v", err)
	}
	data, err := json.Marshal(mon)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var base bremlmonitor.Base
	if err := json.Unmarshal(data, &base); err != nil {
		t.Fatalf("unmarshal into Base: %v", err)
	}

	got, err := FromBremlMonitor(base)
	if err != nil {
		t.Fatalf("FromBremlMonitor: %v", err)
	}
	if !Equivalent(spec, got) {
		t.Errorf("FromBremlMonitor(round-tripped ToBremlMonitor(%+v)) = %+v, want an equivalent spec", spec, got)
	}
}

func TestEquivalent_DetectsDrift_TCPFields(t *testing.T) {
	base := MonitorSpec{Type: TypeTCP, Name: "port-check", TCP: &TCPSpec{Host: "example.com", Port: 443, TLSMode: "secure"}}
	changedTLSMode := base
	changedTLSMode.TCP = &TCPSpec{Host: "example.com", Port: 443, TLSMode: "starttls"}
	if Equivalent(base, changedTLSMode) {
		t.Error("Equivalent(base, changedTLSMode) = true, want false")
	}
}

func TestFromBremlMonitor_RoundTripsThroughToBremlMonitor_PingFields(t *testing.T) {
	spec := MonitorSpec{
		Type: TypePing, Name: "ping-check",
		Ping: &PingSpec{Host: "10.0.0.1", Timeout: 5, PacketSize: 64, DomainExpiryNotification: true},
	}

	mon, err := ToBremlMonitor(7, spec)
	if err != nil {
		t.Fatalf("ToBremlMonitor: %v", err)
	}
	data, err := json.Marshal(mon)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var base bremlmonitor.Base
	if err := json.Unmarshal(data, &base); err != nil {
		t.Fatalf("unmarshal into Base: %v", err)
	}

	got, err := FromBremlMonitor(base)
	if err != nil {
		t.Fatalf("FromBremlMonitor: %v", err)
	}
	if !Equivalent(spec, got) {
		t.Errorf("FromBremlMonitor(round-tripped ToBremlMonitor(%+v)) = %+v, want an equivalent spec", spec, got)
	}
}

func TestFromBremlMonitor_RoundTripsThroughToBremlMonitor_DNSFields(t *testing.T) {
	spec := MonitorSpec{
		Type: TypeDNS, Name: "dns-check",
		DNS: &DNSSpec{Host: "example.com", ResolverServer: "1.1.1.1", ResolveType: "A", DomainExpiryNotification: true},
	}

	mon, err := ToBremlMonitor(7, spec)
	if err != nil {
		t.Fatalf("ToBremlMonitor: %v", err)
	}
	data, err := json.Marshal(mon)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var base bremlmonitor.Base
	if err := json.Unmarshal(data, &base); err != nil {
		t.Fatalf("unmarshal into Base: %v", err)
	}

	got, err := FromBremlMonitor(base)
	if err != nil {
		t.Fatalf("FromBremlMonitor: %v", err)
	}
	if !Equivalent(spec, got) {
		t.Errorf("FromBremlMonitor(round-tripped ToBremlMonitor(%+v)) = %+v, want an equivalent spec", spec, got)
	}
}

func TestEquivalent_DetectsDrift_PingAndDNSFields(t *testing.T) {
	basePing := MonitorSpec{Type: TypePing, Name: "ping-check", Ping: &PingSpec{Host: "10.0.0.1", Timeout: 5}}
	changedPingTimeout := basePing
	changedPingTimeout.Ping = &PingSpec{Host: "10.0.0.1", Timeout: 10}
	if Equivalent(basePing, changedPingTimeout) {
		t.Error("Equivalent(basePing, changedPingTimeout) = true, want false")
	}

	baseDNS := MonitorSpec{Type: TypeDNS, Name: "dns-check", DNS: &DNSSpec{Host: "example.com", ResolverServer: "1.1.1.1", ResolveType: "A"}}
	changedDNSExpiry := baseDNS
	changedDNSExpiry.DNS = &DNSSpec{Host: "example.com", ResolverServer: "1.1.1.1", ResolveType: "A", DomainExpiryNotification: true}
	if Equivalent(baseDNS, changedDNSExpiry) {
		t.Error("Equivalent(baseDNS, changedDNSExpiry) = true, want false")
	}
}

func TestFromBremlMonitor_RoundTripsThroughToBremlMonitor_GamedigFields(t *testing.T) {
	spec := MonitorSpec{
		Type: TypeGamedig, Name: "game-server",
		Gamedig: &GamedigSpec{
			Host: "game.example.com", Port: 27015, Game: "csgo",
			GivenPortOnly: true, DomainExpiryNotification: true,
		},
	}

	mon, err := ToBremlMonitor(7, spec)
	if err != nil {
		t.Fatalf("ToBremlMonitor: %v", err)
	}
	data, err := json.Marshal(mon)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var base bremlmonitor.Base
	if err := json.Unmarshal(data, &base); err != nil {
		t.Fatalf("unmarshal into Base: %v", err)
	}

	got, err := FromBremlMonitor(base)
	if err != nil {
		t.Fatalf("FromBremlMonitor: %v", err)
	}
	if !Equivalent(spec, got) {
		t.Errorf("FromBremlMonitor(round-tripped ToBremlMonitor(%+v)) = %+v, want an equivalent spec", spec, got)
	}
}

func TestEquivalent_DetectsDrift_GamedigFields(t *testing.T) {
	base := MonitorSpec{Type: TypeGamedig, Name: "game-server", Gamedig: &GamedigSpec{Host: "game.example.com", Port: 27015, Game: "csgo"}}
	changedGivenPortOnly := base
	changedGivenPortOnly.Gamedig = &GamedigSpec{Host: "game.example.com", Port: 27015, Game: "csgo", GivenPortOnly: true}
	if Equivalent(base, changedGivenPortOnly) {
		t.Error("Equivalent(base, changedGivenPortOnly) = true, want false")
	}
}

func TestFromBremlMonitor_RoundTripsThroughToBremlMonitor_ReferencedFields(t *testing.T) {
	groupID := int64(5)
	proxyID := int64(7)
	spec := MonitorSpec{
		Type: TypeHTTP, Name: "web",
		NotificationIDs: []int64{1, 2},
		GroupID:         &groupID,
		ProxyID:         &proxyID,
		HTTP:            &HTTPSpec{URL: "https://example.com/", Method: "GET", AcceptedStatusCodes: []string{"200-299"}},
	}

	mon, err := ToBremlMonitor(9, spec)
	if err != nil {
		t.Fatalf("ToBremlMonitor: %v", err)
	}
	data, err := json.Marshal(mon)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var base bremlmonitor.Base
	if err := json.Unmarshal(data, &base); err != nil {
		t.Fatalf("unmarshal into Base: %v", err)
	}

	got, err := FromBremlMonitor(base)
	if err != nil {
		t.Fatalf("FromBremlMonitor: %v", err)
	}
	if !Equivalent(spec, got) {
		t.Errorf("FromBremlMonitor(round-tripped ToBremlMonitor(%+v)) = %+v, want an equivalent spec", spec, got)
	}
}

func TestEquivalent_DetectsDrift_ReferencedFields(t *testing.T) {
	base := MonitorSpec{Type: TypeHTTP, Name: "web", HTTP: &HTTPSpec{URL: "https://example.com/", Method: "GET", AcceptedStatusCodes: []string{"200-299"}}}

	changedNotifications := base
	changedNotifications.NotificationIDs = []int64{1}
	if Equivalent(base, changedNotifications) {
		t.Error("Equivalent(base, changedNotifications) = true, want false")
	}

	groupID := int64(5)
	changedGroup := base
	changedGroup.GroupID = &groupID
	if Equivalent(base, changedGroup) {
		t.Error("Equivalent(base, changedGroup) = true, want false")
	}

	proxyID := int64(7)
	changedProxy := base
	changedProxy.ProxyID = &proxyID
	if Equivalent(base, changedProxy) {
		t.Error("Equivalent(base, changedProxy) = true, want false")
	}
}

func TestEquivalent_NotificationIDsOrderIndependent(t *testing.T) {
	a := MonitorSpec{Type: TypeHTTP, Name: "web", NotificationIDs: []int64{1, 2}, HTTP: &HTTPSpec{URL: "https://example.com/", Method: "GET", AcceptedStatusCodes: []string{"200-299"}}}
	b := MonitorSpec{Type: TypeHTTP, Name: "web", NotificationIDs: []int64{2, 1}, HTTP: &HTTPSpec{URL: "https://example.com/", Method: "GET", AcceptedStatusCodes: []string{"200-299"}}}
	if !Equivalent(a, b) {
		t.Error("Equivalent(a, b) = false, want true — NotificationIDs order must not matter")
	}
}

func TestFromBremlMonitor_RoundTripsThroughToBremlMonitor_HTTPAuthFields(t *testing.T) {
	spec := MonitorSpec{
		Type: TypeHTTP, Name: "web",
		HTTP: &HTTPSpec{
			URL: "https://example.com/", Method: "GET", AcceptedStatusCodes: []string{"200-299"},
			AuthMethod: "basic", BasicAuthUsername: "svc-account", BasicAuthPassword: "hunter2",
		},
	}

	mon, err := ToBremlMonitor(9, spec)
	if err != nil {
		t.Fatalf("ToBremlMonitor: %v", err)
	}
	data, err := json.Marshal(mon)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var base bremlmonitor.Base
	if err := json.Unmarshal(data, &base); err != nil {
		t.Fatalf("unmarshal into Base: %v", err)
	}

	got, err := FromBremlMonitor(base)
	if err != nil {
		t.Fatalf("FromBremlMonitor: %v", err)
	}
	if !Equivalent(spec, got) {
		t.Errorf("FromBremlMonitor(round-tripped ToBremlMonitor(%+v)) = %+v, want an equivalent spec", spec, got)
	}
}

func TestFromBremlMonitor_RoundTripsThroughToBremlMonitor_GamedigToken(t *testing.T) {
	spec := MonitorSpec{
		Type: TypeGamedig, Name: "game-server",
		Gamedig: &GamedigSpec{Host: "game.example.com", Port: 27015, Game: "csgo", Token: "s3cr3t"},
	}

	mon, err := ToBremlMonitor(9, spec)
	if err != nil {
		t.Fatalf("ToBremlMonitor: %v", err)
	}
	data, err := json.Marshal(mon)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var base bremlmonitor.Base
	if err := json.Unmarshal(data, &base); err != nil {
		t.Fatalf("unmarshal into Base: %v", err)
	}

	got, err := FromBremlMonitor(base)
	if err != nil {
		t.Fatalf("FromBremlMonitor: %v", err)
	}
	if !Equivalent(spec, got) {
		t.Errorf("FromBremlMonitor(round-tripped ToBremlMonitor(%+v)) = %+v, want an equivalent spec", spec, got)
	}
}

func TestEquivalent_DetectsDrift_HTTPAuthFields(t *testing.T) {
	base := MonitorSpec{Type: TypeHTTP, Name: "web", HTTP: &HTTPSpec{URL: "https://example.com/", Method: "GET", AcceptedStatusCodes: []string{"200-299"}, AuthMethod: "bearer", BearerToken: "abc"}}
	changedToken := base
	changedToken.HTTP = &HTTPSpec{URL: base.HTTP.URL, Method: base.HTTP.Method, AcceptedStatusCodes: base.HTTP.AcceptedStatusCodes, AuthMethod: "bearer", BearerToken: "xyz"}
	if Equivalent(base, changedToken) {
		t.Error("Equivalent(base, changedToken) = true, want false")
	}
}

// breml's HTTP.MarshalJSON always emits oauth_auth_method, so the operator
// has to send the value Kuma's OAuth2-CC form requires — "" is rejected
// there. kuma.HTTPSpec has no field for it (nothing to round-trip or
// compare), it's purely what gets sent on the wire.
func TestToBremlMonitor_HTTP_OAuthAuthMethod(t *testing.T) {
	tests := []struct {
		authMethod string
		want       string
	}{
		{"oauth2-cc", "client_secret_basic"},
		{"basic", ""},
		{"bearer", ""},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.authMethod, func(t *testing.T) {
			got, err := ToBremlMonitor(0, MonitorSpec{
				Type: TypeHTTP,
				Name: "example",
				HTTP: &HTTPSpec{URL: "https://example.com/", AuthMethod: tt.authMethod},
			})
			if err != nil {
				t.Fatalf("ToBremlMonitor: %v", err)
			}
			http, ok := got.(*bremlmonitor.HTTP)
			if !ok {
				t.Fatalf("got %T, want *monitor.HTTP", got)
			}
			if http.OAuthAuthMethod != tt.want {
				t.Errorf("OAuthAuthMethod for AuthMethod %q = %q, want %q", tt.authMethod, http.OAuthAuthMethod, tt.want)
			}
		})
	}
}
