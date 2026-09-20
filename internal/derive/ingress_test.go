package derive

import (
	"testing"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"uptime-kuma-operator/internal/annotations"
	"uptime-kuma-operator/internal/kuma"
)

func TestIngressMonitors_HTTPHostUsesHTTP(t *testing.T) {
	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "web"},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{{Host: "app.example.com"}},
		},
	}

	got := IngressMonitors(ing, annotations.Overrides{})
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].Host != "app.example.com" {
		t.Errorf("Host = %q, want %q", got[0].Host, "app.example.com")
	}
	if got[0].Spec.Type != kuma.TypeHTTP {
		t.Errorf("Type = %q, want %q", got[0].Spec.Type, kuma.TypeHTTP)
	}
	if got[0].Spec.HTTP.URL != "http://app.example.com/" {
		t.Errorf("URL = %q, want %q", got[0].Spec.HTTP.URL, "http://app.example.com/")
	}
	if got[0].Spec.Name != "prod/web/app.example.com" {
		t.Errorf("Name = %q, want default derived name", got[0].Spec.Name)
	}
}

func TestIngressMonitors_TLSHostUsesHTTPS(t *testing.T) {
	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "web"},
		Spec: networkingv1.IngressSpec{
			TLS:   []networkingv1.IngressTLS{{Hosts: []string{"app.example.com"}}},
			Rules: []networkingv1.IngressRule{{Host: "app.example.com"}},
		},
	}

	got := IngressMonitors(ing, annotations.Overrides{})
	if got[0].Spec.HTTP.URL != "https://app.example.com/" {
		t.Errorf("URL = %q, want %q", got[0].Spec.HTTP.URL, "https://app.example.com/")
	}
}

func TestIngressMonitors_MultipleHosts(t *testing.T) {
	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "web"},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{{Host: "a.example.com"}, {Host: "b.example.com"}},
		},
	}

	got := IngressMonitors(ing, annotations.Overrides{})
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
}

func TestIngressMonitors_OverridesApplied(t *testing.T) {
	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "web"},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{{Host: "app.example.com"}},
		},
	}
	ov := annotations.Overrides{
		Name:          "custom-name",
		Interval:      30,
		RetryInterval: 10,
		MaxRetries:    2,
		HTTP:          annotations.HTTPOverrides{Scheme: "https", AcceptedStatusCodes: []string{"200-299"}},
	}

	got := IngressMonitors(ing, ov)
	spec := got[0].Spec
	if spec.Name != "custom-name" {
		t.Errorf("Name = %q, want %q", spec.Name, "custom-name")
	}
	if spec.HTTP.URL != "https://app.example.com/" {
		t.Errorf("URL = %q, want https override applied", spec.HTTP.URL)
	}
	if spec.Interval != 30 || spec.RetryInterval != 10 || spec.MaxRetries != 2 {
		t.Errorf("interval fields = %+v, want overrides applied", spec)
	}
}

func TestIngressMonitors_SkipsEmptyAndDuplicateHosts(t *testing.T) {
	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "web"},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{{Host: ""}, {Host: "a.example.com"}, {Host: "a.example.com"}},
		},
	}

	got := IngressMonitors(ing, annotations.Overrides{})
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1 (empty and duplicate hosts skipped)", len(got))
	}
}

func TestIngressMonitors_Phase1OverridesApplied(t *testing.T) {
	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "web"},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{{Host: "app.example.com"}},
		},
	}
	ov := annotations.Overrides{
		Description: "a friendly description", ResendInterval: 3, UpsideDown: true,
		HTTP: annotations.HTTPOverrides{
			Timeout: 30, MaxRedirects: 5, IgnoreTLS: true, CacheBust: true,
			ExpiryNotification: true, DomainExpiryNotification: true,
			Headers: `{"X-Custom":"value"}`, Body: `{"key":"value"}`,
		},
	}

	got := IngressMonitors(ing, ov)
	spec := got[0].Spec
	if spec.Description != "a friendly description" || spec.ResendInterval != 3 || !spec.UpsideDown {
		t.Errorf("common fields = %+v, want Phase 1 overrides applied", spec)
	}
	if spec.HTTP.Timeout != 30 || spec.HTTP.MaxRedirects != 5 || !spec.HTTP.IgnoreTLS || !spec.HTTP.CacheBust ||
		!spec.HTTP.ExpiryNotification || !spec.HTTP.DomainExpiryNotification ||
		spec.HTTP.Headers != `{"X-Custom":"value"}` || spec.HTTP.Body != `{"key":"value"}` {
		t.Errorf("HTTP fields = %+v, want Phase 1 overrides applied", spec.HTTP)
	}
}

func TestIngressMonitors_PathOverride(t *testing.T) {
	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "web"},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{{Host: "app.example.com"}},
		},
	}

	got := IngressMonitors(ing, annotations.Overrides{HTTP: annotations.HTTPOverrides{Path: "/healthz"}})
	if got[0].Spec.HTTP.URL != "http://app.example.com/healthz" {
		t.Errorf("URL = %q, want %q", got[0].Spec.HTTP.URL, "http://app.example.com/healthz")
	}
}

func TestIngressMonitors_NoPathOverrideDefaultsToSlash(t *testing.T) {
	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "web"},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{{Host: "app.example.com"}},
		},
	}

	got := IngressMonitors(ing, annotations.Overrides{})
	if got[0].Spec.HTTP.URL != "http://app.example.com/" {
		t.Errorf("URL = %q, want %q", got[0].Spec.HTTP.URL, "http://app.example.com/")
	}
}
