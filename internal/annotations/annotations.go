package annotations

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

const (
	Enabled             = "uptime-kuma.io/enabled"
	Name                = "uptime-kuma.io/name"
	Scheme              = "uptime-kuma.io/scheme"
	Interval            = "uptime-kuma.io/interval"
	RetryInterval       = "uptime-kuma.io/retry-interval"
	MaxRetries          = "uptime-kuma.io/max-retries"
	AcceptedStatusCodes = "uptime-kuma.io/accepted-statuscodes"

	Description              = "uptime-kuma.io/description"
	ResendInterval           = "uptime-kuma.io/resend-interval"
	UpsideDown               = "uptime-kuma.io/upside-down"
	Timeout                  = "uptime-kuma.io/timeout"
	MaxRedirects             = "uptime-kuma.io/max-redirects"
	IgnoreTLS                = "uptime-kuma.io/ignore-tls"
	CacheBust                = "uptime-kuma.io/cache-bust"
	ExpiryNotification       = "uptime-kuma.io/expiry-notification"
	DomainExpiryNotification = "uptime-kuma.io/domain-expiry-notification"
	Headers                  = "uptime-kuma.io/headers"
	Body                     = "uptime-kuma.io/body"
	Path                     = "uptime-kuma.io/path"

	Tags          = "uptime-kuma.io/tags"
	Notifications = "uptime-kuma.io/notifications"
	Proxy         = "uptime-kuma.io/proxy"
	Group         = "uptime-kuma.io/group"

	MonitorIDs = "uptime-kuma.io/monitor-ids"
	SyncedHash = "uptime-kuma.io/synced-hash"
	Finalizer  = "uptime-kuma.io/finalizer"
)

// ShouldSync reports whether a resource carrying ann should be synced to
// Kuma, given the operator-wide optInByDefault setting. This is independent
// of which namespaces the operator watches (config.Config.WatchAll) — a
// resource still needs an explicit uptime-kuma.io/enabled=true annotation
// even when the operator is watching every namespace, unless optInByDefault
// is also set.
func ShouldSync(optInByDefault bool, ann map[string]string) bool {
	v, ok := ann[Enabled]
	if optInByDefault {
		return !ok || v != "false"
	}
	return ok && v == "true"
}

// Overrides holds the optional per-resource monitor field overrides parsed
// from annotations. Zero values mean "not set, caller picks a default".
type Overrides struct {
	Name                string
	Scheme              string // "" | "http" | "https"
	Interval            int64
	RetryInterval       int64
	MaxRetries          int64
	AcceptedStatusCodes []string

	Description              string
	ResendInterval           int64
	UpsideDown               bool
	Timeout                  int64
	MaxRedirects             int64
	IgnoreTLS                bool
	CacheBust                bool
	ExpiryNotification       bool
	DomainExpiryNotification bool
	Headers                  string
	Body                     string
	Path                     string // "" | "/..." — must start with "/" if set

	Notifications []string
	Proxy         int64
	Group         string

	Tags []string
}

func ParseOverrides(ann map[string]string) (Overrides, error) {
	var o Overrides
	o.Name = ann[Name]
	o.Scheme = ann[Scheme]
	if o.Scheme != "" && o.Scheme != "http" && o.Scheme != "https" {
		return Overrides{}, fmt.Errorf("annotations: %s must be %q or %q, got %q", Scheme, "http", "https", o.Scheme)
	}

	var err error
	if o.Interval, err = parseIntAnnotation(ann, Interval); err != nil {
		return Overrides{}, err
	}
	if o.RetryInterval, err = parseIntAnnotation(ann, RetryInterval); err != nil {
		return Overrides{}, err
	}
	if o.MaxRetries, err = parseIntAnnotation(ann, MaxRetries); err != nil {
		return Overrides{}, err
	}

	if v := ann[AcceptedStatusCodes]; v != "" {
		for _, code := range strings.Split(v, ",") {
			o.AcceptedStatusCodes = append(o.AcceptedStatusCodes, strings.TrimSpace(code))
		}
	}

	o.Description = ann[Description]
	o.Headers = ann[Headers]
	o.Body = ann[Body]
	o.Path = ann[Path]
	if o.Path != "" && !strings.HasPrefix(o.Path, "/") {
		return Overrides{}, fmt.Errorf("annotations: %s must start with \"/\", got %q", Path, o.Path)
	}

	if o.ResendInterval, err = parseIntAnnotation(ann, ResendInterval); err != nil {
		return Overrides{}, err
	}
	if o.Timeout, err = parseIntAnnotation(ann, Timeout); err != nil {
		return Overrides{}, err
	}
	if o.MaxRedirects, err = parseIntAnnotationForIntRange(ann, MaxRedirects); err != nil {
		return Overrides{}, err
	}
	if o.UpsideDown, err = parseBoolAnnotation(ann, UpsideDown); err != nil {
		return Overrides{}, err
	}
	if o.IgnoreTLS, err = parseBoolAnnotation(ann, IgnoreTLS); err != nil {
		return Overrides{}, err
	}
	if o.CacheBust, err = parseBoolAnnotation(ann, CacheBust); err != nil {
		return Overrides{}, err
	}
	if o.ExpiryNotification, err = parseBoolAnnotation(ann, ExpiryNotification); err != nil {
		return Overrides{}, err
	}
	if o.DomainExpiryNotification, err = parseBoolAnnotation(ann, DomainExpiryNotification); err != nil {
		return Overrides{}, err
	}
	if v := ann[Notifications]; v != "" {
		for _, name := range strings.Split(v, ",") {
			// Skip empties: a trailing or doubled comma would otherwise
			// resolve as a notification channel named "".
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			o.Notifications = append(o.Notifications, name)
		}
	}
	o.Group = ann[Group]
	if o.Proxy, err = parseIntAnnotation(ann, Proxy); err != nil {
		return Overrides{}, err
	}
	if v := ann[Tags]; v != "" {
		for _, name := range strings.Split(v, ",") {
			// Skip empties: a trailing or doubled comma would otherwise
			// create and attach a blank tag in Kuma.
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			o.Tags = append(o.Tags, name)
		}
	}
	return o, nil
}

func parseIntAnnotation(ann map[string]string, key string) (int64, error) {
	v, ok := ann[key]
	if !ok || v == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("annotations: %s must be an integer, got %q", key, v)
	}
	return n, nil
}

func parseIntAnnotationForIntRange(ann map[string]string, key string) (int64, error) {
	n, err := parseIntAnnotation(ann, key)
	if err != nil {
		return 0, err
	}
	if n < math.MinInt || n > math.MaxInt {
		return 0, fmt.Errorf("annotations: %s must fit in platform int range [%d, %d], got %d", key, math.MinInt, math.MaxInt, n)
	}
	return n, nil
}

// parseBoolAnnotation returns ann[key] parsed as a bool, or false if the
// annotation is absent or empty. false already matches every boolean
// field's Kuma-side default used in this codebase, so "unset" and
// "explicitly false" don't need to be distinguished.
func parseBoolAnnotation(ann map[string]string, key string) (bool, error) {
	v, ok := ann[key]
	if !ok || v == "" {
		return false, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("annotations: %s must be a boolean, got %q", key, v)
	}
	return b, nil
}

// ParseMonitorIDs decodes the operator-written MonitorIDs annotation (a
// JSON object of host -> Kuma monitor ID string). A missing or empty
// annotation decodes to an empty, non-nil map.
func ParseMonitorIDs(ann map[string]string) (map[string]string, error) {
	v := ann[MonitorIDs]
	if v == "" {
		return map[string]string{}, nil
	}
	var ids map[string]string
	if err := json.Unmarshal([]byte(v), &ids); err != nil {
		return nil, fmt.Errorf("annotations: %s is not valid JSON: %w", MonitorIDs, err)
	}
	return ids, nil
}

// SetMonitorIDs encodes ids into ann under MonitorIDs, removing the
// annotation entirely if ids is empty. It returns ann for convenient
// chaining; ann must be non-nil.
func SetMonitorIDs(ann map[string]string, ids map[string]string) map[string]string {
	if len(ids) == 0 {
		delete(ann, MonitorIDs)
		return ann
	}
	b, _ := json.Marshal(ids) // map[string]string always marshals cleanly
	ann[MonitorIDs] = string(b)
	return ann
}

// ParseSyncedHash returns the operator-written SyncedHash annotation
// (a fingerprint of the desired monitor set as of the last successful
// sync), or "" if absent.
func ParseSyncedHash(ann map[string]string) string {
	return ann[SyncedHash]
}

// SetSyncedHash sets hash into ann under SyncedHash, removing the
// annotation entirely if hash is empty. It returns ann for convenient
// chaining; ann must be non-nil.
func SetSyncedHash(ann map[string]string, hash string) map[string]string {
	if hash == "" {
		delete(ann, SyncedHash)
		return ann
	}
	ann[SyncedHash] = hash
	return ann
}
