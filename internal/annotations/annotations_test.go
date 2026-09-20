package annotations

import (
	"reflect"
	"testing"
)

func TestShouldSync(t *testing.T) {
	cases := []struct {
		name           string
		optInByDefault bool
		ann            map[string]string
		want           bool
	}{
		{"opt-in required, no annotation", false, nil, false},
		{"opt-in required, enabled=true", false, map[string]string{Enabled: "true"}, true},
		{"opt-in required, enabled=false", false, map[string]string{Enabled: "false"}, false},
		{"opt-in by default, no annotation", true, nil, true},
		{"opt-in by default, enabled=false opts out", true, map[string]string{Enabled: "false"}, false},
		{"opt-in by default, enabled=true", true, map[string]string{Enabled: "true"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ShouldSync(c.optInByDefault, c.ann); got != c.want {
				t.Errorf("ShouldSync(%v, %v) = %v, want %v", c.optInByDefault, c.ann, got, c.want)
			}
		})
	}
}

func TestParseOverrides_TopLevelFields(t *testing.T) {
	ov, err := ParseOverrides(map[string]string{
		Name:           "friendly-name",
		Type:           "TCP",
		Interval:       "30",
		RetryInterval:  "10",
		MaxRetries:     "5",
		Description:    "a friendly description",
		ResendInterval: "3",
		UpsideDown:     "true",
		Notifications:  "slack-prod, email-oncall",
		Group:          "prod-services",
		Proxy:          "7",
		Tags:           "env-prod, team-platform",
	})
	if err != nil {
		t.Fatalf("ParseOverrides: %v", err)
	}
	want := Overrides{
		Name: "friendly-name", Type: "TCP",
		Interval: 30, RetryInterval: 10, MaxRetries: 5,
		Description: "a friendly description", ResendInterval: 3, UpsideDown: true,
		Notifications: []string{"slack-prod", "email-oncall"},
		Group:         "prod-services",
		Proxy:         7,
		Tags:          []string{"env-prod", "team-platform"},
	}
	if !reflect.DeepEqual(ov, want) {
		t.Errorf("ParseOverrides = %+v, want %+v", ov, want)
	}
}

func TestParseOverrides_InvalidInteger(t *testing.T) {
	if _, err := ParseOverrides(map[string]string{Interval: "not-a-number"}); err == nil {
		t.Fatal("expected error for non-integer interval, got nil")
	}
}

func TestParseOverrides_InvalidBoolean(t *testing.T) {
	if _, err := ParseOverrides(map[string]string{UpsideDown: "not-a-bool"}); err == nil {
		t.Fatal("expected error for non-boolean upside-down, got nil")
	}
}

func TestParseOverrides_InvalidProxy(t *testing.T) {
	if _, err := ParseOverrides(map[string]string{Proxy: "not-a-number"}); err == nil {
		t.Fatal("expected error for non-integer proxy, got nil")
	}
}

// A trailing or doubled comma must not yield an empty-string entry: for tags
// that would create and attach a blank tag in Kuma, and for notifications it
// fails the reconcile with a confusing `channel "" not found` error.
func TestParseOverrides_SkipsEmptyCommaSeparatedEntries(t *testing.T) {
	ov, err := ParseOverrides(map[string]string{
		Tags:          "env-prod,,team-platform, ",
		Notifications: "slack-prod,,email-oncall, ",
	})
	if err != nil {
		t.Fatalf("ParseOverrides: %v", err)
	}
	if !reflect.DeepEqual(ov.Tags, []string{"env-prod", "team-platform"}) {
		t.Errorf("Tags = %v, want [env-prod team-platform] with no empty entries", ov.Tags)
	}
	if !reflect.DeepEqual(ov.Notifications, []string{"slack-prod", "email-oncall"}) {
		t.Errorf("Notifications = %v, want [slack-prod email-oncall] with no empty entries", ov.Notifications)
	}
}

func TestParseOverrides_HTTPFields(t *testing.T) {
	ov, err := ParseOverrides(map[string]string{
		HTTPHost:                     "custom.example.com",
		HTTPPort:                     "8080",
		HTTPScheme:                   "https",
		HTTPPath:                     "/healthz",
		HTTPAcceptedStatusCodes:      "200-299, 301",
		HTTPTimeout:                  "10",
		HTTPMaxRedirects:             "5",
		HTTPIgnoreTLS:                "true",
		HTTPCacheBust:                "true",
		HTTPExpiryNotification:       "true",
		HTTPDomainExpiryNotification: "true",
		HTTPHeaders:                  `{"X-Custom":"value"}`,
		HTTPBody:                     `{"key":"value"}`,
	})
	if err != nil {
		t.Fatalf("ParseOverrides: %v", err)
	}
	want := HTTPOverrides{
		Host: "custom.example.com", Port: "8080",
		Scheme: "https", Path: "/healthz",
		AcceptedStatusCodes:      []string{"200-299", "301"},
		Timeout:                  10,
		MaxRedirects:             5,
		IgnoreTLS:                true,
		CacheBust:                true,
		ExpiryNotification:       true,
		DomainExpiryNotification: true,
		Headers:                  `{"X-Custom":"value"}`,
		Body:                     `{"key":"value"}`,
	}
	if !reflect.DeepEqual(ov.HTTP, want) {
		t.Errorf("ov.HTTP = %+v, want %+v", ov.HTTP, want)
	}
}

func TestParseOverrides_InvalidHTTPScheme(t *testing.T) {
	if _, err := ParseOverrides(map[string]string{HTTPScheme: "ftp"}); err == nil {
		t.Fatal("expected error for invalid scheme, got nil")
	}
}

func TestParseOverrides_InvalidHTTPPath(t *testing.T) {
	if _, err := ParseOverrides(map[string]string{HTTPPath: "no-leading-slash"}); err == nil {
		t.Fatal("expected error for path without leading slash, got nil")
	}
}

func TestParseOverrides_HTTPPathUnsetDefaultsEmpty(t *testing.T) {
	ov, err := ParseOverrides(nil)
	if err != nil {
		t.Fatalf("ParseOverrides: %v", err)
	}
	if ov.HTTP.Path != "" {
		t.Errorf("HTTP.Path = %q, want empty string when unset", ov.HTTP.Path)
	}
}

func TestParseOverrides_TCPFields(t *testing.T) {
	ov, err := ParseOverrides(map[string]string{
		TCPHost:                     "tcp.example.com",
		TCPPort:                     "5432",
		TCPTLSMode:                  "secure",
		TCPExpectedSSLAlert:         "unrecognized_name",
		TCPExpiryNotification:       "true",
		TCPDomainExpiryNotification: "true",
	})
	if err != nil {
		t.Fatalf("ParseOverrides: %v", err)
	}
	want := TCPOverrides{
		Host: "tcp.example.com", Port: "5432",
		TLSMode: "secure", ExpectedSSLAlert: "unrecognized_name",
		ExpiryNotification: true, DomainExpiryNotification: true,
	}
	if !reflect.DeepEqual(ov.TCP, want) {
		t.Errorf("ov.TCP = %+v, want %+v", ov.TCP, want)
	}
}

func TestParseOverrides_InvalidTCPTLSMode(t *testing.T) {
	if _, err := ParseOverrides(map[string]string{TCPTLSMode: "bogus"}); err == nil {
		t.Fatal("expected error for invalid tls-mode, got nil")
	}
}

func TestParseOverrides_PingFields(t *testing.T) {
	ov, err := ParseOverrides(map[string]string{
		PingHost:                     "ping.example.com",
		PingTimeout:                  "5",
		PingPacketSize:               "56",
		PingDomainExpiryNotification: "true",
	})
	if err != nil {
		t.Fatalf("ParseOverrides: %v", err)
	}
	want := PingOverrides{
		Host: "ping.example.com", Timeout: 5, PacketSize: 56, DomainExpiryNotification: true,
	}
	if !reflect.DeepEqual(ov.Ping, want) {
		t.Errorf("ov.Ping = %+v, want %+v", ov.Ping, want)
	}
}

func TestParseOverrides_DNSFields(t *testing.T) {
	ov, err := ParseOverrides(map[string]string{
		DNSHost:                     "dns.example.com",
		DNSPort:                     "5353",
		DNSResolverServer:           "1.1.1.1",
		DNSResolveType:              "A",
		DNSDomainExpiryNotification: "true",
	})
	if err != nil {
		t.Fatalf("ParseOverrides: %v", err)
	}
	want := DNSOverrides{
		Host: "dns.example.com", Port: 5353,
		ResolverServer: "1.1.1.1", ResolveType: "A",
		DomainExpiryNotification: true,
	}
	if !reflect.DeepEqual(ov.DNS, want) {
		t.Errorf("ov.DNS = %+v, want %+v", ov.DNS, want)
	}
}

func TestParseOverrides_GamedigFields(t *testing.T) {
	ov, err := ParseOverrides(map[string]string{
		GamedigHost:                     "game.example.com",
		GamedigPort:                     "27015",
		GamedigGame:                     "csgo",
		GamedigGivenPortOnly:            "true",
		GamedigDomainExpiryNotification: "true",
	})
	if err != nil {
		t.Fatalf("ParseOverrides: %v", err)
	}
	want := GamedigOverrides{
		Host: "game.example.com", Port: "27015",
		Game: "csgo", GivenPortOnly: true, DomainExpiryNotification: true,
	}
	if !reflect.DeepEqual(ov.Gamedig, want) {
		t.Errorf("ov.Gamedig = %+v, want %+v", ov.Gamedig, want)
	}
}

func TestMonitorIDsRoundTrip(t *testing.T) {
	ann := map[string]string{}
	ann = SetMonitorIDs(ann, map[string]string{"a.example.com": "1", "b.example.com": "2"})

	got, err := ParseMonitorIDs(ann)
	if err != nil {
		t.Fatalf("ParseMonitorIDs: %v", err)
	}
	if got["a.example.com"] != "1" || got["b.example.com"] != "2" {
		t.Errorf("ParseMonitorIDs = %v, want {a.example.com:1 b.example.com:2}", got)
	}
}

func TestSetMonitorIDs_EmptyRemovesAnnotation(t *testing.T) {
	ann := map[string]string{MonitorIDs: `{"a":"1"}`}
	ann = SetMonitorIDs(ann, map[string]string{})
	if _, ok := ann[MonitorIDs]; ok {
		t.Error("MonitorIDs annotation still present after clearing to empty map")
	}
}

func TestSyncedHashRoundTrip(t *testing.T) {
	ann := map[string]string{}
	ann = SetSyncedHash(ann, "deadbeef")
	if got := ParseSyncedHash(ann); got != "deadbeef" {
		t.Errorf("ParseSyncedHash = %q, want %q", got, "deadbeef")
	}
}

func TestSetSyncedHash_EmptyRemovesAnnotation(t *testing.T) {
	ann := map[string]string{SyncedHash: "deadbeef"}
	ann = SetSyncedHash(ann, "")
	if _, ok := ann[SyncedHash]; ok {
		t.Error("SyncedHash annotation still present after clearing to empty string")
	}
}

func TestParseSyncedHash_MissingAnnotationIsEmptyString(t *testing.T) {
	if got := ParseSyncedHash(nil); got != "" {
		t.Errorf("ParseSyncedHash(nil) = %q, want empty string", got)
	}
}

func TestParseMonitorIDs_MissingAnnotationIsEmptyMap(t *testing.T) {
	got, err := ParseMonitorIDs(nil)
	if err != nil {
		t.Fatalf("ParseMonitorIDs: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ParseMonitorIDs(nil) = %v, want empty map", got)
	}
}
