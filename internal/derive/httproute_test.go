package derive

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"uptime-kuma-operator/internal/annotations"
)

func TestHTTPRouteMonitors_DefaultsToHTTPS(t *testing.T) {
	route := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "web"},
		Spec: gatewayv1.HTTPRouteSpec{
			Hostnames: []gatewayv1.Hostname{"app.example.com"},
		},
	}

	got := HTTPRouteMonitors(route, annotations.Overrides{})
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].Spec.HTTP.URL != "https://app.example.com/" {
		t.Errorf("URL = %q, want %q", got[0].Spec.HTTP.URL, "https://app.example.com/")
	}
	if got[0].Spec.Name != "prod/web/app.example.com" {
		t.Errorf("Name = %q, want default derived name", got[0].Spec.Name)
	}
}

func TestHTTPRouteMonitors_SchemeOverrideToHTTP(t *testing.T) {
	route := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "web"},
		Spec: gatewayv1.HTTPRouteSpec{
			Hostnames: []gatewayv1.Hostname{"app.example.com"},
		},
	}

	got := HTTPRouteMonitors(route, annotations.Overrides{HTTP: annotations.HTTPOverrides{Scheme: "http"}})
	if got[0].Spec.HTTP.URL != "http://app.example.com/" {
		t.Errorf("URL = %q, want %q", got[0].Spec.HTTP.URL, "http://app.example.com/")
	}
}

func TestHTTPRouteMonitors_MultipleHostnames(t *testing.T) {
	route := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "web"},
		Spec: gatewayv1.HTTPRouteSpec{
			Hostnames: []gatewayv1.Hostname{"a.example.com", "b.example.com"},
		},
	}

	got := HTTPRouteMonitors(route, annotations.Overrides{})
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
}

func TestHTTPRouteMonitors_SkipsEmptyAndDuplicateHosts(t *testing.T) {
	route := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "web"},
		Spec: gatewayv1.HTTPRouteSpec{
			Hostnames: []gatewayv1.Hostname{"", "a.example.com", "a.example.com"},
		},
	}

	got := HTTPRouteMonitors(route, annotations.Overrides{})
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1 (empty and duplicate hosts skipped)", len(got))
	}
	if got[0].Host != "a.example.com" {
		t.Errorf("Host = %q, want %q", got[0].Host, "a.example.com")
	}
}

func TestHTTPRouteMonitors_NoHostnamesYieldsNoMonitors(t *testing.T) {
	route := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "web"},
	}

	got := HTTPRouteMonitors(route, annotations.Overrides{})
	if len(got) != 0 {
		t.Fatalf("len(got) = %d, want 0", len(got))
	}
}

func TestHTTPRouteMonitors_PathOverride(t *testing.T) {
	route := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "web"},
		Spec: gatewayv1.HTTPRouteSpec{
			Hostnames: []gatewayv1.Hostname{"app.example.com"},
		},
	}

	got := HTTPRouteMonitors(route, annotations.Overrides{HTTP: annotations.HTTPOverrides{Path: "/healthz"}})
	if got[0].Spec.HTTP.URL != "https://app.example.com/healthz" {
		t.Errorf("URL = %q, want %q", got[0].Spec.HTTP.URL, "https://app.example.com/healthz")
	}
}

func TestHTTPRouteMonitors_Phase1OverridesApplied(t *testing.T) {
	route := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "web"},
		Spec: gatewayv1.HTTPRouteSpec{
			Hostnames: []gatewayv1.Hostname{"app.example.com"},
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

	got := HTTPRouteMonitors(route, ov)
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
