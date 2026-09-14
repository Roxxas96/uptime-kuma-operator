package config

import (
	"reflect"
	"testing"
)

func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoad_MinimalValid(t *testing.T) {
	cfg, err := Load(envMap(map[string]string{
		"KUMA_URL":      "https://kuma.example.com",
		"KUMA_USERNAME": "admin",
		"KUMA_PASSWORD": "secret",
		"POD_NAMESPACE": "default",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := Config{KumaURL: "https://kuma.example.com", KumaUsername: "admin", KumaPassword: "secret", WatchNamespaces: []string{"default"}}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("Load = %+v, want %+v", cfg, want)
	}
}

func TestLoad_FallsBackToPodNamespace(t *testing.T) {
	cfg, err := Load(envMap(map[string]string{
		"KUMA_URL":      "https://kuma.example.com",
		"KUMA_USERNAME": "admin",
		"KUMA_PASSWORD": "secret",
		"POD_NAMESPACE": "operator-ns",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"operator-ns"}
	if !reflect.DeepEqual(cfg.WatchNamespaces, want) {
		t.Errorf("WatchNamespaces = %v, want %v", cfg.WatchNamespaces, want)
	}
}

func TestLoad_ErrorsWithNoNamespaceSource(t *testing.T) {
	_, err := Load(envMap(map[string]string{
		"KUMA_URL":      "https://kuma.example.com",
		"KUMA_USERNAME": "admin",
		"KUMA_PASSWORD": "secret",
	}))
	if err == nil {
		t.Fatal("expected error when WATCH_NAMESPACES and POD_NAMESPACE are both unset, got nil")
	}
}

func TestLoad_WatchAllNeedsNoNamespace(t *testing.T) {
	cfg, err := Load(envMap(map[string]string{
		"KUMA_URL":      "https://kuma.example.com",
		"KUMA_USERNAME": "admin",
		"KUMA_PASSWORD": "secret",
		"WATCH_ALL":     "true",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.WatchAll {
		t.Error("WatchAll = false, want true")
	}
	if len(cfg.WatchNamespaces) != 0 {
		t.Errorf("WatchNamespaces = %v, want empty", cfg.WatchNamespaces)
	}
}

func TestLoad_ParsesNamespaceListAndWatchAll(t *testing.T) {
	cfg, err := Load(envMap(map[string]string{
		"KUMA_URL":         "https://kuma.example.com",
		"KUMA_USERNAME":    "admin",
		"KUMA_PASSWORD":    "secret",
		"WATCH_NAMESPACES": "prod, staging,, dev",
		"WATCH_ALL":        "true",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.WatchAll {
		t.Error("WatchAll = false, want true")
	}
	want := []string{"prod", "staging", "dev"}
	if !reflect.DeepEqual(cfg.WatchNamespaces, want) {
		t.Errorf("WatchNamespaces = %v, want %v (blank entries trimmed)", cfg.WatchNamespaces, want)
	}
}

func TestLoad_MissingKumaURL(t *testing.T) {
	_, err := Load(envMap(map[string]string{"KUMA_USERNAME": "a", "KUMA_PASSWORD": "b"}))
	if err == nil {
		t.Fatal("expected error for missing KUMA_URL, got nil")
	}
}

func TestLoad_MissingCredentials(t *testing.T) {
	_, err := Load(envMap(map[string]string{"KUMA_URL": "https://kuma.example.com"}))
	if err == nil {
		t.Fatal("expected error for missing credentials, got nil")
	}
}
