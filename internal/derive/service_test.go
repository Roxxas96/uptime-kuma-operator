package derive

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"uptime-kuma-operator/internal/annotations"
	"uptime-kuma-operator/internal/kuma"
)

func svcWithPorts(ports ...corev1.ServicePort) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "web"},
		Spec:       corev1.ServiceSpec{Ports: ports},
	}
}

func TestServiceMonitors_DefaultsToHTTP(t *testing.T) {
	svc := svcWithPorts(corev1.ServicePort{Port: 8080})

	got, err := ServiceMonitors(svc, annotations.Overrides{})
	if err != nil {
		t.Fatalf("ServiceMonitors: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].Spec.Type != kuma.TypeHTTP {
		t.Errorf("Type = %q, want %q", got[0].Spec.Type, kuma.TypeHTTP)
	}
	wantURL := "http://web.prod.svc.cluster.local:8080/"
	if got[0].Spec.HTTP == nil || got[0].Spec.HTTP.URL != wantURL {
		t.Errorf("URL = %v, want %q", got[0].Spec.HTTP, wantURL)
	}
	if got[0].Spec.Name != "prod/web" {
		t.Errorf("Name = %q, want %q", got[0].Spec.Name, "prod/web")
	}
}

func TestServiceMonitors_HTTPHostAndSchemeOverride(t *testing.T) {
	svc := svcWithPorts(corev1.ServicePort{Port: 8080})
	ov := annotations.Overrides{HTTP: annotations.HTTPOverrides{Host: "custom.example.com", Scheme: "https", Path: "/healthz"}}

	got, err := ServiceMonitors(svc, ov)
	if err != nil {
		t.Fatalf("ServiceMonitors: %v", err)
	}
	want := "https://custom.example.com:8080/healthz"
	if got[0].Spec.HTTP.URL != want {
		t.Errorf("URL = %q, want %q", got[0].Spec.HTTP.URL, want)
	}
}

func TestServiceMonitors_SinglePortAutoPicked(t *testing.T) {
	svc := svcWithPorts(corev1.ServicePort{Port: 9090})

	got, err := ServiceMonitors(svc, annotations.Overrides{Type: "TCP"})
	if err != nil {
		t.Fatalf("ServiceMonitors: %v", err)
	}
	if got[0].Spec.TCP == nil || got[0].Spec.TCP.Port != 9090 {
		t.Errorf("TCP = %v, want Port 9090", got[0].Spec.TCP)
	}
	if got[0].Spec.TCP.Host != "web.prod.svc.cluster.local" {
		t.Errorf("Host = %q, want default DNS name", got[0].Spec.TCP.Host)
	}
}

func TestServiceMonitors_MultiplePortsRequiresAnnotation(t *testing.T) {
	svc := svcWithPorts(corev1.ServicePort{Port: 80}, corev1.ServicePort{Port: 443})
	if _, err := ServiceMonitors(svc, annotations.Overrides{Type: "TCP"}); err == nil {
		t.Fatal("expected error for ambiguous port, got nil")
	}
}

func TestServiceMonitors_PortByName(t *testing.T) {
	svc := svcWithPorts(
		corev1.ServicePort{Name: "http", Port: 80},
		corev1.ServicePort{Name: "metrics", Port: 9100},
	)
	ov := annotations.Overrides{Type: "TCP", TCP: annotations.TCPOverrides{Port: "metrics"}}

	got, err := ServiceMonitors(svc, ov)
	if err != nil {
		t.Fatalf("ServiceMonitors: %v", err)
	}
	if got[0].Spec.TCP.Port != 9100 {
		t.Errorf("Port = %d, want 9100", got[0].Spec.TCP.Port)
	}
}

func TestServiceMonitors_PortByNumber(t *testing.T) {
	svc := svcWithPorts(
		corev1.ServicePort{Name: "http", Port: 80},
		corev1.ServicePort{Name: "metrics", Port: 9100},
	)
	ov := annotations.Overrides{Type: "TCP", TCP: annotations.TCPOverrides{Port: "80"}}

	got, err := ServiceMonitors(svc, ov)
	if err != nil {
		t.Fatalf("ServiceMonitors: %v", err)
	}
	if got[0].Spec.TCP.Port != 80 {
		t.Errorf("Port = %d, want 80", got[0].Spec.TCP.Port)
	}
}

func TestServiceMonitors_UnresolvablePortOverride(t *testing.T) {
	svc := svcWithPorts(corev1.ServicePort{Name: "http", Port: 80})
	ov := annotations.Overrides{Type: "TCP", TCP: annotations.TCPOverrides{Port: "does-not-exist"}}
	if _, err := ServiceMonitors(svc, ov); err == nil {
		t.Fatal("expected error for unresolvable port override, got nil")
	}
}

func TestServiceMonitors_ServiceWithNoPortsErrorsForHTTP(t *testing.T) {
	svc := svcWithPorts()
	if _, err := ServiceMonitors(svc, annotations.Overrides{}); err == nil {
		t.Fatal("expected error for a Service with no ports, got nil")
	}
}

func TestServiceMonitors_Ping(t *testing.T) {
	// Multiple ports on the Service must not matter: Ping never needs one.
	svc := svcWithPorts(corev1.ServicePort{Port: 80}, corev1.ServicePort{Port: 443})
	ov := annotations.Overrides{Type: "Ping", Ping: annotations.PingOverrides{Timeout: 5, PacketSize: 56}}

	got, err := ServiceMonitors(svc, ov)
	if err != nil {
		t.Fatalf("ServiceMonitors: %v (Ping must not require a port)", err)
	}
	if got[0].Spec.Ping == nil || got[0].Spec.Ping.Host != "web.prod.svc.cluster.local" {
		t.Errorf("Ping.Host = %v, want default DNS name", got[0].Spec.Ping)
	}
	if got[0].Spec.Ping.Timeout != 5 || got[0].Spec.Ping.PacketSize != 56 {
		t.Errorf("Ping spec = %+v, want Timeout=5 PacketSize=56", got[0].Spec.Ping)
	}
}

func TestServiceMonitors_DNSPortIsNotResolvedAgainstServicePorts(t *testing.T) {
	// Multiple ports on the Service must not matter: DNS's port annotation
	// is the resolver's query port, unrelated to the Service's own ports.
	svc := svcWithPorts(corev1.ServicePort{Port: 80}, corev1.ServicePort{Port: 443})
	ov := annotations.Overrides{Type: "DNS", DNS: annotations.DNSOverrides{
		ResolverServer: "1.1.1.1", ResolveType: "A", Port: 5353,
	}}

	got, err := ServiceMonitors(svc, ov)
	if err != nil {
		t.Fatalf("ServiceMonitors: %v (DNS port must not require Service-port disambiguation)", err)
	}
	if got[0].Spec.DNS == nil || got[0].Spec.DNS.Port != 5353 {
		t.Errorf("DNS.Port = %v, want 5353 (the resolver port, not a Service port)", got[0].Spec.DNS)
	}
	if got[0].Spec.DNS.ResolverServer != "1.1.1.1" || got[0].Spec.DNS.ResolveType != "A" {
		t.Errorf("DNS spec = %+v, want ResolverServer=1.1.1.1 ResolveType=A", got[0].Spec.DNS)
	}
}

func TestServiceMonitors_GamedigRequiresGame(t *testing.T) {
	svc := svcWithPorts(corev1.ServicePort{Port: 27015})
	if _, err := ServiceMonitors(svc, annotations.Overrides{Type: "Gamedig"}); err == nil {
		t.Fatal("expected error for missing gamedig/game, got nil")
	}
}

func TestServiceMonitors_Gamedig(t *testing.T) {
	svc := svcWithPorts(corev1.ServicePort{Port: 27015})
	ov := annotations.Overrides{Type: "Gamedig", Gamedig: annotations.GamedigOverrides{Game: "csgo", GivenPortOnly: true}}

	got, err := ServiceMonitors(svc, ov)
	if err != nil {
		t.Fatalf("ServiceMonitors: %v", err)
	}
	if got[0].Spec.Gamedig == nil || got[0].Spec.Gamedig.Game != "csgo" ||
		got[0].Spec.Gamedig.Port != 27015 || !got[0].Spec.Gamedig.GivenPortOnly {
		t.Errorf("Gamedig spec = %+v, want Game=csgo Port=27015 GivenPortOnly=true", got[0].Spec.Gamedig)
	}
}

func TestServiceMonitors_UnknownTypeErrors(t *testing.T) {
	svc := svcWithPorts(corev1.ServicePort{Port: 80})
	if _, err := ServiceMonitors(svc, annotations.Overrides{Type: "Bogus"}); err == nil {
		t.Fatal("expected error for unknown type, got nil")
	}
}
