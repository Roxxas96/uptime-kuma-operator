package controller

import (
	"testing"

	uptimekumaiov1alpha1 "uptime-kuma-operator/api/v1alpha1"
	"uptime-kuma-operator/internal/kuma"
)

func TestToKumaSpec_Gamedig(t *testing.T) {
	crd := uptimekumaiov1alpha1.MonitorSpec{
		Type: uptimekumaiov1alpha1.MonitorTypeGamedig,
		Name: "game-server",
		Gamedig: &uptimekumaiov1alpha1.GamedigMonitorSpec{
			Host: "game.example.com", Port: 27015, Game: "csgo",
		},
	}

	got := toKumaSpec(crd)
	if got.Type != kuma.TypeGamedig {
		t.Errorf("Type = %q, want %q", got.Type, kuma.TypeGamedig)
	}
	if got.Gamedig == nil || got.Gamedig.Host != "game.example.com" || got.Gamedig.Port != 27015 || got.Gamedig.Game != "csgo" {
		t.Errorf("Gamedig = %+v, want Host=game.example.com Port=27015 Game=csgo", got.Gamedig)
	}
}

func TestToKumaSpec_DNS(t *testing.T) {
	crd := uptimekumaiov1alpha1.MonitorSpec{
		Type: uptimekumaiov1alpha1.MonitorTypeDNS,
		Name: "dns-check",
		DNS: &uptimekumaiov1alpha1.DNSMonitorSpec{
			Host: "example.com", ResolverServer: "1.1.1.1", ResolveType: "A",
		},
	}

	got := toKumaSpec(crd)
	if got.DNS == nil || got.DNS.Host != "example.com" || got.DNS.ResolverServer != "1.1.1.1" || got.DNS.ResolveType != "A" {
		t.Errorf("DNS = %+v, want Host=example.com ResolverServer=1.1.1.1 ResolveType=A", got.DNS)
	}
}

func TestToKumaSpec_Phase1CommonFields(t *testing.T) {
	crd := uptimekumaiov1alpha1.MonitorSpec{
		Type:        uptimekumaiov1alpha1.MonitorTypePing,
		Name:        "ping-check",
		Description: "a friendly description", ResendInterval: 3, UpsideDown: true,
		Ping: &uptimekumaiov1alpha1.PingMonitorSpec{Host: "10.0.0.1"},
	}

	got := toKumaSpec(crd)
	if got.Description != "a friendly description" || got.ResendInterval != 3 || !got.UpsideDown {
		t.Errorf("common fields = %+v, want Description/ResendInterval/UpsideDown applied", got)
	}
}

func TestToKumaSpec_HTTPFields(t *testing.T) {
	crd := uptimekumaiov1alpha1.MonitorSpec{
		Type: uptimekumaiov1alpha1.MonitorTypeHTTP,
		Name: "web",
		HTTP: &uptimekumaiov1alpha1.HTTPMonitorSpec{
			URL: "https://example.com/", Timeout: 30, MaxRedirects: 5, IgnoreTLS: true,
			CacheBust: true, ExpiryNotification: true, DomainExpiryNotification: true,
			Headers: `{"X-Custom":"value"}`, Body: `{"key":"value"}`,
		},
	}

	got := toKumaSpec(crd)
	if got.HTTP == nil {
		t.Fatal("HTTP is nil")
	}
	if got.HTTP.Timeout != 30 || got.HTTP.MaxRedirects != 5 || !got.HTTP.IgnoreTLS || !got.HTTP.CacheBust ||
		!got.HTTP.ExpiryNotification || !got.HTTP.DomainExpiryNotification ||
		got.HTTP.Headers != `{"X-Custom":"value"}` || got.HTTP.Body != `{"key":"value"}` {
		t.Errorf("HTTP fields = %+v, want all Phase 1 overrides applied", got.HTTP)
	}
}

func TestToKumaSpec_TCPFields(t *testing.T) {
	crd := uptimekumaiov1alpha1.MonitorSpec{
		Type: uptimekumaiov1alpha1.MonitorTypeTCP,
		Name: "port-check",
		TCP: &uptimekumaiov1alpha1.TCPMonitorSpec{
			Host: "example.com", Port: 443,
			TLSMode: "secure", ExpectedSSLAlert: "certificate_required",
			ExpiryNotification: true, DomainExpiryNotification: true,
		},
	}

	got := toKumaSpec(crd)
	if got.TCP == nil {
		t.Fatal("TCP is nil")
	}
	if got.TCP.TLSMode != "secure" || got.TCP.ExpectedSSLAlert != "certificate_required" ||
		!got.TCP.ExpiryNotification || !got.TCP.DomainExpiryNotification {
		t.Errorf("TCP fields = %+v, want all Phase 1 overrides applied", got.TCP)
	}
}

func TestToKumaSpec_PingFields(t *testing.T) {
	crd := uptimekumaiov1alpha1.MonitorSpec{
		Type: uptimekumaiov1alpha1.MonitorTypePing,
		Name: "ping-check",
		Ping: &uptimekumaiov1alpha1.PingMonitorSpec{
			Host: "10.0.0.1", Timeout: 5, PacketSize: 64, DomainExpiryNotification: true,
		},
	}

	got := toKumaSpec(crd)
	if got.Ping == nil {
		t.Fatal("Ping is nil")
	}
	if got.Ping.Timeout != 5 || got.Ping.PacketSize != 64 || !got.Ping.DomainExpiryNotification {
		t.Errorf("Ping fields = %+v, want all Phase 1 overrides applied", got.Ping)
	}
}

func TestToKumaSpec_DNSFields(t *testing.T) {
	crd := uptimekumaiov1alpha1.MonitorSpec{
		Type: uptimekumaiov1alpha1.MonitorTypeDNS,
		Name: "dns-check",
		DNS: &uptimekumaiov1alpha1.DNSMonitorSpec{
			Host: "example.com", DomainExpiryNotification: true,
		},
	}

	got := toKumaSpec(crd)
	if got.DNS == nil {
		t.Fatal("DNS is nil")
	}
	if !got.DNS.DomainExpiryNotification {
		t.Errorf("DNS fields = %+v, want DomainExpiryNotification applied", got.DNS)
	}
}

func TestToKumaSpec_GamedigFields(t *testing.T) {
	crd := uptimekumaiov1alpha1.MonitorSpec{
		Type: uptimekumaiov1alpha1.MonitorTypeGamedig,
		Name: "game-server",
		Gamedig: &uptimekumaiov1alpha1.GamedigMonitorSpec{
			Host: "game.example.com", Port: 27015, Game: "csgo",
			GivenPortOnly: true, DomainExpiryNotification: true,
		},
	}

	got := toKumaSpec(crd)
	if got.Gamedig == nil {
		t.Fatal("Gamedig is nil")
	}
	if !got.Gamedig.GivenPortOnly || !got.Gamedig.DomainExpiryNotification {
		t.Errorf("Gamedig fields = %+v, want GivenPortOnly/DomainExpiryNotification applied", got.Gamedig)
	}
}
