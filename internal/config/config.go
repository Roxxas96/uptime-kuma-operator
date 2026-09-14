package config

import (
	"fmt"
	"strings"
)

type Config struct {
	KumaURL         string
	KumaUsername    string
	KumaPassword    string
	WatchNamespaces []string
	WatchAll        bool
}

// Load builds a Config from environment variables, read via getenv so tests
// don't need to mutate real process environment.
func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		KumaURL:      getenv("KUMA_URL"),
		KumaUsername: getenv("KUMA_USERNAME"),
		KumaPassword: getenv("KUMA_PASSWORD"),
		WatchAll:     getenv("WATCH_ALL") == "true",
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

	if !cfg.WatchAll && len(cfg.WatchNamespaces) == 0 {
		if podNamespace := getenv("POD_NAMESPACE"); podNamespace != "" {
			cfg.WatchNamespaces = []string{podNamespace}
		} else {
			return Config{}, fmt.Errorf("config: WATCH_NAMESPACES or POD_NAMESPACE must be set when WATCH_ALL is not \"true\"")
		}
	}

	return cfg, nil
}
