package annotations

import (
	"encoding/json"
	"fmt"
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
	MonitorIDs          = "uptime-kuma.io/monitor-ids"
	Finalizer           = "uptime-kuma.io/finalizer"
)

// ShouldSync reports whether a resource carrying ann should be synced to
// Kuma, given the operator-wide watchAll setting.
func ShouldSync(watchAll bool, ann map[string]string) bool {
	v, ok := ann[Enabled]
	if watchAll {
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
