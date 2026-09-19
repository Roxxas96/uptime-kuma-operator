package config

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// DefaultDriftCheckInterval is used when DRIFT_CHECK_INTERVAL is unset.
const DefaultDriftCheckInterval = 30 * time.Second

type Config struct {
	KumaURL         string
	KumaUsername    string
	KumaPassword    string
	WatchNamespaces []string
	// WatchAll controls namespace scope only — whether the manager watches
	// every namespace or just WatchNamespaces. It has no effect on whether
	// Ingress/HTTPRoute resources need the uptime-kuma.io/enabled annotation;
	// that's OptInByDefault, a deliberately separate setting.
	WatchAll bool
	// OptInByDefault, when true, syncs every Ingress/HTTPRoute unless
	// explicitly annotated uptime-kuma.io/enabled=false. When false (the
	// default), a resource is synced only when explicitly annotated
	// uptime-kuma.io/enabled=true — regardless of WatchAll.
	OptInByDefault bool
	// DriftCheckInterval is how often a reconciler that found nothing to
	// sync re-checks that Kuma still has the monitors it's supposed to,
	// recreating any that were deleted out-of-band (e.g. manually in the
	// Kuma UI). Defaults to DefaultDriftCheckInterval.
	DriftCheckInterval time.Duration
	// LogLevel controls log verbosity: "debug", "info" (default), "warn",
	// or "error". "debug" additionally surfaces per-reconcile detail (what
	// triggered a reconcile, which decision branch was taken) beyond the
	// default create/update/delete action logs.
	LogLevel string
	// DefaultTags lists tag names applied to every monitor the operator
	// manages, in addition to whatever tags that monitor's own
	// Ingress/HTTPRoute/Monitor resource specifies. Empty by default —
	// this is opt-in, cluster-operator-level configuration, not something
	// any single managed resource controls.
	DefaultTags []string
	// LabelTagPatterns, when non-empty, turns on operator-wide label-tag
	// inference: for every monitor the operator manages, a "key=value" Kuma
	// tag is added for each label on the label-owning resource
	// (Ingress/HTTPRoute/Monitor) whose key fully matches at least one of
	// these patterns. Each raw pattern from LABEL_TAG_PATTERNS is compiled
	// anchored (^(?:pattern)$), so a plain prefix filter is expressed as
	// e.g. "team-.*". Patterns must not themselves contain a literal comma
	// — LABEL_TAG_PATTERNS is comma-separated with no escaping, the same
	// limitation DEFAULT_TAGS already has. Empty by default: the feature is
	// entirely off unless at least one pattern is configured.
	LabelTagPatterns []*regexp.Regexp
}

// validLogLevels are the accepted values for LOG_LEVEL, matching what
// cmd/main.go's zap logger can be configured with.
var validLogLevels = map[string]bool{"debug": true, "info": true, "warn": true, "error": true}

// Load builds a Config from environment variables, read via getenv so tests
// don't need to mutate real process environment.
func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		KumaURL:            getenv("KUMA_URL"),
		KumaUsername:       getenv("KUMA_USERNAME"),
		KumaPassword:       getenv("KUMA_PASSWORD"),
		WatchAll:           getenv("WATCH_ALL") == "true",
		OptInByDefault:     getenv("OPT_IN_BY_DEFAULT") == "true",
		DriftCheckInterval: DefaultDriftCheckInterval,
		LogLevel:           "info",
	}

	if raw := getenv("LOG_LEVEL"); raw != "" {
		lvl := strings.ToLower(raw)
		if !validLogLevels[lvl] {
			return Config{}, fmt.Errorf("config: LOG_LEVEL must be one of debug, info, warn, error, got %q", raw)
		}
		cfg.LogLevel = lvl
	}

	if raw := getenv("DRIFT_CHECK_INTERVAL"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return Config{}, fmt.Errorf("config: DRIFT_CHECK_INTERVAL is not a valid duration: %w", err)
		}
		if d <= 0 {
			return Config{}, fmt.Errorf("config: DRIFT_CHECK_INTERVAL must be positive, got %q", raw)
		}
		cfg.DriftCheckInterval = d
	}

	if cfg.KumaURL == "" {
		return Config{}, fmt.Errorf("config: KUMA_URL is required")
	}
	if cfg.KumaUsername == "" || cfg.KumaPassword == "" {
		return Config{}, fmt.Errorf("config: KUMA_USERNAME and KUMA_PASSWORD are required")
	}

	if raw := getenv("WATCH_NAMESPACES"); raw != "" {
		for _, ns := range strings.Split(raw, ",") {
			ns = strings.TrimSpace(ns)
			if ns != "" {
				cfg.WatchNamespaces = append(cfg.WatchNamespaces, ns)
			}
		}
	}

	if raw := getenv("DEFAULT_TAGS"); raw != "" {
		for _, tag := range strings.Split(raw, ",") {
			tag = strings.TrimSpace(tag)
			if tag != "" {
				cfg.DefaultTags = append(cfg.DefaultTags, tag)
			}
		}
	}

	if raw := getenv("LABEL_TAG_PATTERNS"); raw != "" {
		for _, pattern := range strings.Split(raw, ",") {
			pattern = strings.TrimSpace(pattern)
			if pattern == "" {
				continue
			}
			re, err := regexp.Compile("^(?:" + pattern + ")$")
			if err != nil {
				return Config{}, fmt.Errorf("config: LABEL_TAG_PATTERNS pattern %q is not a valid regex: %w", pattern, err)
			}
			cfg.LabelTagPatterns = append(cfg.LabelTagPatterns, re)
		}
	}

	if !cfg.WatchAll && len(cfg.WatchNamespaces) == 0 {
		if podNamespace := getenv("POD_NAMESPACE"); podNamespace != "" {
			cfg.WatchNamespaces = []string{podNamespace}
		} else {
			return Config{}, fmt.Errorf("config: WATCH_NAMESPACES or POD_NAMESPACE must be set when WATCH_ALL is not \"true\"")
		}
	}

	return cfg, nil
}
