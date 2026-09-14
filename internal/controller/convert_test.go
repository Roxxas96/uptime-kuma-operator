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
