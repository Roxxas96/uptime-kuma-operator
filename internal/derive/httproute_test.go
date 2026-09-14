package derive

import (
	"testing"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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

	got := HTTPRouteMonitors(route, annotations.Overrides{Scheme: "http"})
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

func TestHTTPRouteMonitors_NoHostnamesYieldsNoMonitors(t *testing.T) {
	route := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "web"},
	}

	got := HTTPRouteMonitors(route, annotations.Overrides{})
	if len(got) != 0 {
		t.Fatalf("len(got) = %d, want 0", len(got))
	}
}
