package kuma

import (
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
