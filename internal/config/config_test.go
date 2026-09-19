package config

import (
	"reflect"
	"testing"
	"time"
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
	want := Config{
		KumaURL: "https://kuma.example.com", KumaUsername: "admin", KumaPassword: "secret",
		WatchNamespaces: []string{"default"}, DriftCheckInterval: DefaultDriftCheckInterval, LogLevel: "info",
	}
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

func TestLoad_OptInByDefaultIsIndependentOfWatchAll(t *testing.T) {
	cfg, err := Load(envMap(map[string]string{
		"KUMA_URL":      "https://kuma.example.com",
		"KUMA_USERNAME": "admin",
		"KUMA_PASSWORD": "secret",
		"WATCH_ALL":     "true",
		// OPT_IN_BY_DEFAULT deliberately left unset.
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.WatchAll {
		t.Error("WatchAll = false, want true")
	}
	if cfg.OptInByDefault {
		t.Error("OptInByDefault = true, want false — WATCH_ALL must not imply it")
	}
}

func TestLoad_OptInByDefaultTrue(t *testing.T) {
	cfg, err := Load(envMap(map[string]string{
		"KUMA_URL":          "https://kuma.example.com",
		"KUMA_USERNAME":     "admin",
		"KUMA_PASSWORD":     "secret",
		"POD_NAMESPACE":     "default",
		"OPT_IN_BY_DEFAULT": "true",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.OptInByDefault {
		t.Error("OptInByDefault = false, want true")
	}
}

func TestLoad_DriftCheckIntervalDefaultsWhenUnset(t *testing.T) {
	cfg, err := Load(envMap(map[string]string{
		"KUMA_URL":      "https://kuma.example.com",
		"KUMA_USERNAME": "admin",
		"KUMA_PASSWORD": "secret",
		"POD_NAMESPACE": "default",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DriftCheckInterval != DefaultDriftCheckInterval {
		t.Errorf("DriftCheckInterval = %v, want default %v", cfg.DriftCheckInterval, DefaultDriftCheckInterval)
	}
}

func TestLoad_DriftCheckIntervalParsesDuration(t *testing.T) {
	cfg, err := Load(envMap(map[string]string{
		"KUMA_URL":             "https://kuma.example.com",
		"KUMA_USERNAME":        "admin",
		"KUMA_PASSWORD":        "secret",
		"POD_NAMESPACE":        "default",
		"DRIFT_CHECK_INTERVAL": "30s",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DriftCheckInterval != 30*time.Second {
		t.Errorf("DriftCheckInterval = %v, want 30s", cfg.DriftCheckInterval)
	}
}

func TestLoad_DriftCheckIntervalRejectsInvalidDuration(t *testing.T) {
	_, err := Load(envMap(map[string]string{
		"KUMA_URL":             "https://kuma.example.com",
		"KUMA_USERNAME":        "admin",
		"KUMA_PASSWORD":        "secret",
		"POD_NAMESPACE":        "default",
		"DRIFT_CHECK_INTERVAL": "not-a-duration",
	}))
	if err == nil {
		t.Fatal("expected error for invalid DRIFT_CHECK_INTERVAL, got nil")
	}
}

func TestLoad_DriftCheckIntervalRejectsNonPositive(t *testing.T) {
	_, err := Load(envMap(map[string]string{
		"KUMA_URL":             "https://kuma.example.com",
		"KUMA_USERNAME":        "admin",
		"KUMA_PASSWORD":        "secret",
		"POD_NAMESPACE":        "default",
		"DRIFT_CHECK_INTERVAL": "0s",
	}))
	if err == nil {
		t.Fatal("expected error for non-positive DRIFT_CHECK_INTERVAL, got nil")
	}
}

func TestLoad_LogLevelDefaultsToInfo(t *testing.T) {
	cfg, err := Load(envMap(map[string]string{
		"KUMA_URL":      "https://kuma.example.com",
		"KUMA_USERNAME": "admin",
		"KUMA_PASSWORD": "secret",
		"POD_NAMESPACE": "default",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want default %q", cfg.LogLevel, "info")
	}
}

func TestLoad_LogLevelAcceptsValidValuesCaseInsensitively(t *testing.T) {
	cfg, err := Load(envMap(map[string]string{
		"KUMA_URL":      "https://kuma.example.com",
		"KUMA_USERNAME": "admin",
		"KUMA_PASSWORD": "secret",
		"POD_NAMESPACE": "default",
		"LOG_LEVEL":     "DEBUG",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "debug")
	}
}

func TestLoad_LogLevelRejectsInvalidValue(t *testing.T) {
	_, err := Load(envMap(map[string]string{
		"KUMA_URL":      "https://kuma.example.com",
		"KUMA_USERNAME": "admin",
		"KUMA_PASSWORD": "secret",
		"POD_NAMESPACE": "default",
		"LOG_LEVEL":     "verbose",
	}))
	if err == nil {
		t.Fatal("expected error for invalid LOG_LEVEL, got nil")
	}
}

func TestLoad_DefaultTagsEmptyWhenUnset(t *testing.T) {
	cfg, err := Load(envMap(map[string]string{
		"KUMA_URL":      "https://kuma.example.com",
		"KUMA_USERNAME": "admin",
		"KUMA_PASSWORD": "secret",
		"POD_NAMESPACE": "default",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.DefaultTags) != 0 {
		t.Errorf("DefaultTags = %v, want empty", cfg.DefaultTags)
	}
}

func TestLoad_DefaultTagsParsesAndTrimsList(t *testing.T) {
	cfg, err := Load(envMap(map[string]string{
		"KUMA_URL":      "https://kuma.example.com",
		"KUMA_USERNAME": "admin",
		"KUMA_PASSWORD": "secret",
		"POD_NAMESPACE": "default",
		"DEFAULT_TAGS":  "k8s, managed,, production",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"k8s", "managed", "production"}
	if !reflect.DeepEqual(cfg.DefaultTags, want) {
		t.Errorf("DefaultTags = %v, want %v (blank entries trimmed)", cfg.DefaultTags, want)
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

func TestLoad_LabelTagPatternsEmptyWhenUnset(t *testing.T) {
	cfg, err := Load(envMap(map[string]string{
		"KUMA_URL":      "https://kuma.example.com",
		"KUMA_USERNAME": "admin",
		"KUMA_PASSWORD": "secret",
		"POD_NAMESPACE": "default",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.LabelTagPatterns) != 0 {
		t.Errorf("LabelTagPatterns = %v, want empty", cfg.LabelTagPatterns)
	}
}

func TestLoad_LabelTagPatternsParsesAndAnchorsPatterns(t *testing.T) {
	cfg, err := Load(envMap(map[string]string{
		"KUMA_URL":           "https://kuma.example.com",
		"KUMA_USERNAME":      "admin",
		"KUMA_PASSWORD":      "secret",
		"POD_NAMESPACE":      "default",
		"LABEL_TAG_PATTERNS": "team, env-.*",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.LabelTagPatterns) != 2 {
		t.Fatalf("LabelTagPatterns = %v, want 2 compiled patterns", cfg.LabelTagPatterns)
	}
	if !cfg.LabelTagPatterns[0].MatchString("team") {
		t.Error(`LabelTagPatterns[0] ("team") should match "team"`)
	}
	if cfg.LabelTagPatterns[0].MatchString("my-team") {
		t.Error(`LabelTagPatterns[0] ("team") should NOT match "my-team" — patterns must be anchored`)
	}
	if !cfg.LabelTagPatterns[1].MatchString("env-prod") {
		t.Error(`LabelTagPatterns[1] ("env-.*") should match "env-prod"`)
	}
}

func TestLoad_LabelTagPatternsInvalidRegexFails(t *testing.T) {
	_, err := Load(envMap(map[string]string{
		"KUMA_URL":           "https://kuma.example.com",
		"KUMA_USERNAME":      "admin",
		"KUMA_PASSWORD":      "secret",
		"POD_NAMESPACE":      "default",
		"LABEL_TAG_PATTERNS": "team-(",
	}))
	if err == nil {
		t.Fatal("expected error for invalid regex, got nil")
	}
}

func TestLoad_LabelTagPatternsSkipsBlankEntries(t *testing.T) {
	cfg, err := Load(envMap(map[string]string{
		"KUMA_URL":           "https://kuma.example.com",
		"KUMA_USERNAME":      "admin",
		"KUMA_PASSWORD":      "secret",
		"POD_NAMESPACE":      "default",
		"LABEL_TAG_PATTERNS": "team, , env",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.LabelTagPatterns) != 2 {
		t.Errorf("LabelTagPatterns = %v, want 2 patterns (blank entry skipped)", cfg.LabelTagPatterns)
	}
}
