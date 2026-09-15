# Monitor Configuration Expansion — Phase 1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expose the direct-value (non-secret, non-referential) Kuma monitor configuration fields requested for HTTP/TCP/Ping/DNS/Gamedig monitors, through both the Monitor CRD and Ingress/HTTPRoute annotations.

**Architecture:** Extends the existing five-layer pipeline with new fields at every layer, following the exact pattern already established for `Interval`/`RetryInterval`/`MaxRetries`/`AcceptedStatusCodes`: annotations (`internal/annotations`) → derived `kuma.MonitorSpec` (`internal/derive`) → CRD `MonitorSpec` (`api/v1alpha1`) → `toKumaSpec` conversion (`internal/controller/convert.go`) → breml translation (`internal/kuma/translate.go`, both directions, plus `Equivalent` for drift detection).

**Tech Stack:** Go, controller-runtime, `github.com/breml/go-uptime-kuma-client` v0.4.2.

**Spec:** `docs/superpowers/specs/2026-09-15-monitor-config-expansion-phase1-design.md`

## Global Constraints

- Every new field MUST flow through `ToBremlMonitor`, `FromBremlMonitor`, AND `Equivalent` — a field wired into only `ToBremlMonitor` is a silent drift-detection gap (the operator would never notice or correct that field drifting out-of-band).
- All new boolean fields are plain `bool`, never `*bool` — confirmed per-field that Go's zero value (`false`) already equals breml/Kuma's own default for every boolean in this phase.
- Annotations exist ONLY for common fields and HTTP-specific fields. Ingress/HTTPRoute always derive HTTP-type monitors, so TCP/Ping/DNS/Gamedig-specific fields get CRD fields only, never annotations.
- CRD JSON tags use `lowerCamelCase` with `omitempty` on every new (optional) field, matching existing convention.
- Out of scope for this plan, do not implement: Gamedig `Token`, HTTP auth (`AuthMethod`/`BearerToken`/`BasicAuthUser`/`BasicAuthPassword`/OAuth fields), tags, notification channels, proxy, monitor groups. These are deferred to a later phase per the spec.
- Excluded entirely, not supported by breml v0.4.2 — do not attempt: IP Family selector, HTTP response-saving-for-notification, response max length, Ping "numeric output", Ping "max packets" (only packet *size* exists).
- After any change to `api/v1alpha1/monitor_types.go`, run `make generate manifests` (regenerates `zz_generated.deepcopy.go`, `config/crd/bases/uptime-kuma.io_monitors.yaml`, and syncs the Helm chart's CRD copy).
- No field in this phase needs `normalizeSpec` default-substitution — every zero value already equals what breml/Kuma defaults to (confirmed per-field in the spec). Do not add substitution logic unless you find a genuine counterexample; if you do, stop and flag it rather than guessing.
- Run `make test` (the full envtest-backed suite), `gofmt -l .`, and `go vet ./...` clean before considering any task done.

---

### Task 1: Annotation support for common + HTTP-specific fields

**Files:**
- Modify: `internal/annotations/annotations.go`
- Test: `internal/annotations/annotations_test.go`

**Interfaces:**
- Produces: `Overrides` struct gains `Description string`, `ResendInterval int64`, `UpsideDown bool`, `Timeout int64`, `MaxRedirects int64`, `IgnoreTLS bool`, `CacheBust bool`, `ExpiryNotification bool`, `DomainExpiryNotification bool`, `Headers string`, `Body string`, `Path string`. `ParseOverrides(ann map[string]string) (Overrides, error)` populates all of them. New unexported helper `parseBoolAnnotation(ann map[string]string, key string) (bool, error)`.
- Consumed by: Task 9 (`internal/derive`).

- [ ] **Step 1: Write the failing tests**

Append to `internal/annotations/annotations_test.go` (add `"reflect"` to the import block, which currently only imports `"testing"`):

```go
func TestParseOverrides_Phase1Fields(t *testing.T) {
	ov, err := ParseOverrides(map[string]string{
		Description:              "a friendly description",
		ResendInterval:           "3",
		UpsideDown:               "true",
		Timeout:                  "10",
		MaxRedirects:             "5",
		IgnoreTLS:                "true",
		CacheBust:                "true",
		ExpiryNotification:       "true",
		DomainExpiryNotification: "true",
		Headers:                  `{"X-Custom":"value"}`,
		Body:                     `{"key":"value"}`,
		Path:                     "/healthz",
	})
	if err != nil {
		t.Fatalf("ParseOverrides: %v", err)
	}
	want := Overrides{
		Description: "a friendly description", ResendInterval: 3, UpsideDown: true,
		Timeout: 10, MaxRedirects: 5, IgnoreTLS: true, CacheBust: true,
		ExpiryNotification: true, DomainExpiryNotification: true,
		Headers: `{"X-Custom":"value"}`, Body: `{"key":"value"}`, Path: "/healthz",
	}
	if !reflect.DeepEqual(ov, want) {
		t.Errorf("ParseOverrides = %+v, want %+v", ov, want)
	}
}

func TestParseOverrides_InvalidBoolean(t *testing.T) {
	if _, err := ParseOverrides(map[string]string{UpsideDown: "not-a-bool"}); err == nil {
		t.Fatal("expected error for non-boolean upside-down, got nil")
	}
}

func TestParseOverrides_InvalidPath(t *testing.T) {
	if _, err := ParseOverrides(map[string]string{Path: "no-leading-slash"}); err == nil {
		t.Fatal("expected error for path without leading slash, got nil")
	}
}

func TestParseOverrides_PathUnsetDefaultsEmpty(t *testing.T) {
	ov, err := ParseOverrides(nil)
	if err != nil {
		t.Fatalf("ParseOverrides: %v", err)
	}
	if ov.Path != "" {
		t.Errorf("Path = %q, want empty string when unset", ov.Path)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/annotations/... -run 'Phase1|InvalidBoolean|InvalidPath|PathUnset' -v`
Expected: compile error (`Overrides` has no field `Description` etc., and the new annotation constants don't exist).

- [ ] **Step 3: Add the new annotation constants**

In `internal/annotations/annotations.go`, extend the `const` block:

```go
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

	MonitorIDs = "uptime-kuma.io/monitor-ids"
	SyncedHash = "uptime-kuma.io/synced-hash"
	Finalizer  = "uptime-kuma.io/finalizer"
)
```

- [ ] **Step 4: Extend the `Overrides` struct**

```go
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
}
```

- [ ] **Step 5: Add the `parseBoolAnnotation` helper**

Immediately after `parseIntAnnotation`:

```go
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
```

- [ ] **Step 6: Extend `ParseOverrides`**

Insert before the final `return o, nil` in `ParseOverrides`:

```go
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
	if o.MaxRedirects, err = parseIntAnnotation(ann, MaxRedirects); err != nil {
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
```

(`strings` is already imported in this file.)

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test ./internal/annotations/... -v`
Expected: PASS, all tests including the pre-existing ones.

- [ ] **Step 8: Commit**

```bash
git add internal/annotations/annotations.go internal/annotations/annotations_test.go
git commit -m "feat: add annotation support for Phase 1 monitor config fields"
```

---

### Task 2: kuma package — common fields (Description, ResendInterval, UpsideDown)

**Files:**
- Modify: `internal/kuma/spec.go`
- Modify: `internal/kuma/translate.go`
- Test: `internal/kuma/translate_test.go`

**Interfaces:**
- Produces: `kuma.MonitorSpec` gains `Description string`, `ResendInterval int64`, `UpsideDown bool`. `ToBremlMonitor`, `FromBremlMonitor`, and `Equivalent` all handle them.
- Consumed by: Task 8 (`internal/controller/convert.go`), Task 9 (`internal/derive`).

- [ ] **Step 1: Write the failing tests**

Append to `internal/kuma/translate_test.go`:

```go
func TestFromBremlMonitor_RoundTripsThroughToBremlMonitor_CommonFields(t *testing.T) {
	spec := MonitorSpec{
		Type: TypeHTTP, Name: "web",
		Description: "a friendly description", ResendInterval: 3, UpsideDown: true,
		HTTP: &HTTPSpec{URL: "https://example.com/", Method: "GET", AcceptedStatusCodes: []string{"200-299"}},
	}

	mon, err := ToBremlMonitor(7, spec)
	if err != nil {
		t.Fatalf("ToBremlMonitor: %v", err)
	}
	data, err := json.Marshal(mon)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var base bremlmonitor.Base
	if err := json.Unmarshal(data, &base); err != nil {
		t.Fatalf("unmarshal into Base: %v", err)
	}

	got, err := FromBremlMonitor(base)
	if err != nil {
		t.Fatalf("FromBremlMonitor: %v", err)
	}
	if !Equivalent(spec, got) {
		t.Errorf("FromBremlMonitor(round-tripped ToBremlMonitor(%+v)) = %+v, want an equivalent spec", spec, got)
	}
	if got.Description != "a friendly description" {
		t.Errorf("Description = %q, want %q", got.Description, "a friendly description")
	}
	if got.ResendInterval != 3 {
		t.Errorf("ResendInterval = %d, want 3", got.ResendInterval)
	}
	if !got.UpsideDown {
		t.Error("UpsideDown = false, want true")
	}
}

func TestEquivalent_DetectsDrift_CommonFields(t *testing.T) {
	base := MonitorSpec{Type: TypeHTTP, Name: "web", HTTP: &HTTPSpec{URL: "https://example.com/", Method: "GET", AcceptedStatusCodes: []string{"200-299"}}}

	changedDescription := base
	changedDescription.Description = "changed"
	if Equivalent(base, changedDescription) {
		t.Error("Equivalent(base, changedDescription) = true, want false")
	}

	changedResendInterval := base
	changedResendInterval.ResendInterval = 5
	if Equivalent(base, changedResendInterval) {
		t.Error("Equivalent(base, changedResendInterval) = true, want false")
	}

	changedUpsideDown := base
	changedUpsideDown.UpsideDown = true
	if Equivalent(base, changedUpsideDown) {
		t.Error("Equivalent(base, changedUpsideDown) = true, want false")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/kuma/... -run 'CommonFields' -v`
Expected: compile error (`MonitorSpec` has no field `Description` etc.).

- [ ] **Step 3: Extend `kuma.MonitorSpec`**

In `internal/kuma/spec.go`:

```go
// MonitorSpec is the operator's own monitor representation, shared by the
// Monitor CRD reconciler and the Ingress/HTTPRoute derivation packages. It
// is translated to breml's monitor.Monitor only inside this package.
type MonitorSpec struct {
	Type          MonitorType
	Name          string
	Interval      int64 // seconds; 0 = use Kuma default
	RetryInterval int64 // seconds; 0 = use Kuma default
	MaxRetries    int64 // 0 = use Kuma default

	// Description is shown alongside the monitor in the Kuma UI. "" = unset.
	Description string
	// ResendInterval is how many consecutive failed checks pass between
	// repeated down notifications. 0 = disabled (Kuma default).
	ResendInterval int64
	// UpsideDown inverts up/down: the monitor is reported "up" when the
	// underlying check fails and "down" when it succeeds.
	UpsideDown bool

	HTTP    *HTTPSpec
	TCP     *TCPSpec
	Ping    *PingSpec
	DNS     *DNSSpec
	Gamedig *GamedigSpec
}
```

- [ ] **Step 4: Wire the fields through `translate.go`**

In `ToBremlMonitor`, replace the `base := bremlmonitor.Base{...}` construction:

```go
	spec = normalizeSpec(spec)
	base := bremlmonitor.Base{
		ID:             id,
		Name:           spec.Name,
		Interval:       spec.Interval,
		RetryInterval:  spec.RetryInterval,
		MaxRetries:     spec.MaxRetries,
		ResendInterval: spec.ResendInterval,
		UpsideDown:     spec.UpsideDown,
		IsActive:       true,
	}
	if spec.Description != "" {
		base.Description = &spec.Description
	}
```

In `FromBremlMonitor`, replace the initial `spec := MonitorSpec{...}`:

```go
	spec := MonitorSpec{
		Name:           base.Name,
		Interval:       base.Interval,
		RetryInterval:  base.RetryInterval,
		MaxRetries:     base.MaxRetries,
		ResendInterval: base.ResendInterval,
		UpsideDown:     base.UpsideDown,
	}
	if base.Description != nil {
		spec.Description = *base.Description
	}
```

In `Equivalent`, extend the top-level comparison:

```go
	d := normalizeSpec(desired)
	live = normalizeSpec(live)
	if d.Type != live.Type || d.Name != live.Name || d.Interval != live.Interval ||
		d.RetryInterval != live.RetryInterval || d.MaxRetries != live.MaxRetries ||
		d.Description != live.Description || d.ResendInterval != live.ResendInterval ||
		d.UpsideDown != live.UpsideDown {
		return false
	}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/kuma/... -v`
Expected: PASS, all tests including the pre-existing ones.

- [ ] **Step 6: Commit**

```bash
git add internal/kuma/spec.go internal/kuma/translate.go internal/kuma/translate_test.go
git commit -m "feat: add Description/ResendInterval/UpsideDown to kuma.MonitorSpec"
```

---

### Task 3: kuma package — HTTP-specific fields

**Files:**
- Modify: `internal/kuma/spec.go`
- Modify: `internal/kuma/translate.go`
- Test: `internal/kuma/translate_test.go`

**Interfaces:**
- Consumes: `kuma.MonitorSpec` common fields from Task 2.
- Produces: `kuma.HTTPSpec` gains `Timeout int64`, `MaxRedirects int`, `IgnoreTLS bool`, `CacheBust bool`, `ExpiryNotification bool`, `DomainExpiryNotification bool`, `Headers string`, `Body string`.
- Consumed by: Task 8, Task 9.

- [ ] **Step 1: Write the failing tests**

Append to `internal/kuma/translate_test.go`:

```go
func TestFromBremlMonitor_RoundTripsThroughToBremlMonitor_HTTPFields(t *testing.T) {
	spec := MonitorSpec{
		Type: TypeHTTP, Name: "web",
		HTTP: &HTTPSpec{
			URL: "https://example.com/", Method: "POST", AcceptedStatusCodes: []string{"200-299"},
			Timeout: 30, MaxRedirects: 5, IgnoreTLS: true, CacheBust: true,
			ExpiryNotification: true, DomainExpiryNotification: true,
			Headers: `{"X-Custom":"value"}`, Body: `{"key":"value"}`,
		},
	}

	mon, err := ToBremlMonitor(7, spec)
	if err != nil {
		t.Fatalf("ToBremlMonitor: %v", err)
	}
	data, err := json.Marshal(mon)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var base bremlmonitor.Base
	if err := json.Unmarshal(data, &base); err != nil {
		t.Fatalf("unmarshal into Base: %v", err)
	}

	got, err := FromBremlMonitor(base)
	if err != nil {
		t.Fatalf("FromBremlMonitor: %v", err)
	}
	if !Equivalent(spec, got) {
		t.Errorf("FromBremlMonitor(round-tripped ToBremlMonitor(%+v)) = %+v, want an equivalent spec", spec, got)
	}
}

func TestEquivalent_DetectsDrift_HTTPFields(t *testing.T) {
	base := MonitorSpec{Type: TypeHTTP, Name: "web", HTTP: &HTTPSpec{
		URL: "https://example.com/", Method: "GET", AcceptedStatusCodes: []string{"200-299"},
		Timeout: 30, MaxRedirects: 5,
	}}

	changedTimeout := base
	changedTimeout.HTTP = &HTTPSpec{
		URL: base.HTTP.URL, Method: base.HTTP.Method, AcceptedStatusCodes: base.HTTP.AcceptedStatusCodes,
		Timeout: 60, MaxRedirects: base.HTTP.MaxRedirects,
	}
	if Equivalent(base, changedTimeout) {
		t.Error("Equivalent(base, changedTimeout) = true, want false")
	}

	changedIgnoreTLS := base
	changedIgnoreTLS.HTTP = &HTTPSpec{
		URL: base.HTTP.URL, Method: base.HTTP.Method, AcceptedStatusCodes: base.HTTP.AcceptedStatusCodes,
		Timeout: base.HTTP.Timeout, MaxRedirects: base.HTTP.MaxRedirects, IgnoreTLS: true,
	}
	if Equivalent(base, changedIgnoreTLS) {
		t.Error("Equivalent(base, changedIgnoreTLS) = true, want false")
	}

	changedHeaders := base
	changedHeaders.HTTP = &HTTPSpec{
		URL: base.HTTP.URL, Method: base.HTTP.Method, AcceptedStatusCodes: base.HTTP.AcceptedStatusCodes,
		Timeout: base.HTTP.Timeout, MaxRedirects: base.HTTP.MaxRedirects, Headers: `{"X-Custom":"value"}`,
	}
	if Equivalent(base, changedHeaders) {
		t.Error("Equivalent(base, changedHeaders) = true, want false")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/kuma/... -run 'HTTPFields' -v`
Expected: compile error (`HTTPSpec` has no field `Timeout` etc.).

- [ ] **Step 3: Extend `kuma.HTTPSpec`**

In `internal/kuma/spec.go`:

```go
type HTTPSpec struct {
	URL                 string
	Method              string
	AcceptedStatusCodes []string

	// Timeout is the request timeout in seconds. 0 = use Kuma default.
	Timeout int64
	// MaxRedirects caps how many redirects the check follows. 0 = use Kuma default.
	MaxRedirects int
	// IgnoreTLS skips TLS certificate validation.
	IgnoreTLS bool
	// CacheBust appends a cache-busting query parameter to the request URL.
	CacheBust bool
	// ExpiryNotification enables TLS certificate expiry notifications.
	ExpiryNotification bool
	// DomainExpiryNotification enables domain expiry notifications.
	DomainExpiryNotification bool
	// Headers is an opaque, unvalidated passthrough to Kuma (the same raw
	// text the Kuma UI's "Headers" field accepts). "" = unset.
	Headers string
	// Body is an opaque, unvalidated passthrough to Kuma. "" = unset.
	Body string
}
```

- [ ] **Step 4: Wire the fields through `translate.go`**

In `ToBremlMonitor`'s `TypeHTTP` case, replace the returned `HTTPDetails`:

```go
		return &bremlmonitor.HTTP{
			Base: base,
			HTTPDetails: bremlmonitor.HTTPDetails{
				URL:                       spec.HTTP.URL,
				Method:                    spec.HTTP.Method,
				AcceptedStatusCodes:       spec.HTTP.AcceptedStatusCodes,
				Timeout:                   spec.HTTP.Timeout,
				MaxRedirects:              spec.HTTP.MaxRedirects,
				IgnoreTLS:                 spec.HTTP.IgnoreTLS,
				CacheBust:                 spec.HTTP.CacheBust,
				ExpiryNotification:        spec.HTTP.ExpiryNotification,
				DomainExpiryNotification:  spec.HTTP.DomainExpiryNotification,
				Headers:                   spec.HTTP.Headers,
				Body:                      spec.HTTP.Body,
			},
		}, nil
```

In `FromBremlMonitor`'s `"http"` case:

```go
	case "http":
		spec.Type = TypeHTTP
		var d bremlmonitor.HTTP
		if err := base.As(&d); err != nil {
			return MonitorSpec{}, fmt.Errorf("kuma: decode HTTP monitor %d: %w", base.GetID(), err)
		}
		spec.HTTP = &HTTPSpec{
			URL: d.URL, Method: d.Method, AcceptedStatusCodes: d.AcceptedStatusCodes,
			Timeout: d.Timeout, MaxRedirects: d.MaxRedirects, IgnoreTLS: d.IgnoreTLS,
			CacheBust: d.CacheBust, ExpiryNotification: d.ExpiryNotification,
			DomainExpiryNotification: d.DomainExpiryNotification, Headers: d.Headers, Body: d.Body,
		}
```

In `Equivalent`'s `TypeHTTP` case:

```go
	case TypeHTTP:
		return d.HTTP != nil && live.HTTP != nil &&
			d.HTTP.URL == live.HTTP.URL &&
			d.HTTP.Method == live.HTTP.Method &&
			slices.Equal(d.HTTP.AcceptedStatusCodes, live.HTTP.AcceptedStatusCodes) &&
			d.HTTP.Timeout == live.HTTP.Timeout &&
			d.HTTP.MaxRedirects == live.HTTP.MaxRedirects &&
			d.HTTP.IgnoreTLS == live.HTTP.IgnoreTLS &&
			d.HTTP.CacheBust == live.HTTP.CacheBust &&
			d.HTTP.ExpiryNotification == live.HTTP.ExpiryNotification &&
			d.HTTP.DomainExpiryNotification == live.HTTP.DomainExpiryNotification &&
			d.HTTP.Headers == live.HTTP.Headers &&
			d.HTTP.Body == live.HTTP.Body
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/kuma/... -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/kuma/spec.go internal/kuma/translate.go internal/kuma/translate_test.go
git commit -m "feat: add HTTP-specific Phase 1 fields to kuma.HTTPSpec"
```

---

### Task 4: kuma package — TCP-specific fields

**Files:**
- Modify: `internal/kuma/spec.go`
- Modify: `internal/kuma/translate.go`
- Test: `internal/kuma/translate_test.go`

**Interfaces:**
- Produces: `kuma.TCPSpec` gains `TLSMode string`, `ExpectedSSLAlert string`, `ExpiryNotification bool`, `DomainExpiryNotification bool`.
- Consumed by: Task 8.
- Note: `Equivalent`'s `TypeTCP` case (`*d.TCP == *live.TCP`) compares the whole struct by value and needs NO code change — every field being added is a plain scalar (string/bool), so `TCPSpec` stays comparable via `==`. The test below exists specifically to prove this assumption holds; if it ever breaks (e.g. a future field is a slice), `Equivalent` must switch to explicit field comparison like the HTTP case.

- [ ] **Step 1: Write the failing tests**

Append to `internal/kuma/translate_test.go`:

```go
func TestFromBremlMonitor_RoundTripsThroughToBremlMonitor_TCPFields(t *testing.T) {
	spec := MonitorSpec{
		Type: TypeTCP, Name: "port-check",
		TCP: &TCPSpec{
			Host: "example.com", Port: 443,
			TLSMode: "secure", ExpectedSSLAlert: "certificate_required",
			ExpiryNotification: true, DomainExpiryNotification: true,
		},
	}

	mon, err := ToBremlMonitor(7, spec)
	if err != nil {
		t.Fatalf("ToBremlMonitor: %v", err)
	}
	data, err := json.Marshal(mon)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var base bremlmonitor.Base
	if err := json.Unmarshal(data, &base); err != nil {
		t.Fatalf("unmarshal into Base: %v", err)
	}

	got, err := FromBremlMonitor(base)
	if err != nil {
		t.Fatalf("FromBremlMonitor: %v", err)
	}
	if !Equivalent(spec, got) {
		t.Errorf("FromBremlMonitor(round-tripped ToBremlMonitor(%+v)) = %+v, want an equivalent spec", spec, got)
	}
}

func TestEquivalent_DetectsDrift_TCPFields(t *testing.T) {
	base := MonitorSpec{Type: TypeTCP, Name: "port-check", TCP: &TCPSpec{Host: "example.com", Port: 443, TLSMode: "secure"}}
	changedTLSMode := base
	changedTLSMode.TCP = &TCPSpec{Host: "example.com", Port: 443, TLSMode: "starttls"}
	if Equivalent(base, changedTLSMode) {
		t.Error("Equivalent(base, changedTLSMode) = true, want false")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/kuma/... -run 'TCPFields' -v`
Expected: compile error (`TCPSpec` has no field `TLSMode` etc.).

- [ ] **Step 3: Extend `kuma.TCPSpec`**

In `internal/kuma/spec.go`:

```go
type TCPSpec struct {
	Host string
	Port int

	// TLSMode selects the TLS handshake mode: "" (plain TCP), "nostarttls",
	// "secure", or "starttls".
	TLSMode string
	// ExpectedSSLAlert is the TLS alert name expected during the handshake
	// (e.g. for mTLS verification).
	ExpectedSSLAlert string
	// ExpiryNotification enables TLS certificate expiry notifications. Only
	// honoured by Kuma when TLSMode is "secure" or "starttls".
	ExpiryNotification bool
	// DomainExpiryNotification enables domain expiry notifications.
	DomainExpiryNotification bool
}
```

- [ ] **Step 4: Wire the fields through `translate.go`**

In `ToBremlMonitor`'s `TypeTCP` case:

```go
	case TypeTCP:
		if spec.TCP == nil {
			return nil, fmt.Errorf("kuma: monitor type %s requires the TCP field to be set", TypeTCP)
		}
		details := bremlmonitor.TCPPortDetails{
			Hostname:                 spec.TCP.Host,
			Port:                     spec.TCP.Port,
			ExpiryNotification:       spec.TCP.ExpiryNotification,
			DomainExpiryNotification: spec.TCP.DomainExpiryNotification,
		}
		if spec.TCP.TLSMode != "" {
			details.SMTPSecurity = &spec.TCP.TLSMode
		}
		if spec.TCP.ExpectedSSLAlert != "" {
			details.ExpectedTLSAlert = &spec.TCP.ExpectedSSLAlert
		}
		return &bremlmonitor.TCPPort{Base: base, TCPPortDetails: details}, nil
```

In `FromBremlMonitor`'s `"port"` case:

```go
	case "port":
		spec.Type = TypeTCP
		var d bremlmonitor.TCPPort
		if err := base.As(&d); err != nil {
			return MonitorSpec{}, fmt.Errorf("kuma: decode TCP monitor %d: %w", base.GetID(), err)
		}
		tcp := &TCPSpec{
			Host: d.Hostname, Port: d.Port,
			ExpiryNotification: d.ExpiryNotification, DomainExpiryNotification: d.DomainExpiryNotification,
		}
		if d.SMTPSecurity != nil {
			tcp.TLSMode = *d.SMTPSecurity
		}
		if d.ExpectedTLSAlert != nil {
			tcp.ExpectedSSLAlert = *d.ExpectedTLSAlert
		}
		spec.TCP = tcp
```

`Equivalent`'s `TypeTCP` case is unchanged (see the Interfaces note above).

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/kuma/... -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/kuma/spec.go internal/kuma/translate.go internal/kuma/translate_test.go
git commit -m "feat: add TCP-specific Phase 1 fields to kuma.TCPSpec"
```

---

### Task 5: kuma package — Ping + DNS-specific fields

**Files:**
- Modify: `internal/kuma/spec.go`
- Modify: `internal/kuma/translate.go`
- Test: `internal/kuma/translate_test.go`

**Interfaces:**
- Produces: `kuma.PingSpec` gains `Timeout int64`, `PacketSize int`, `DomainExpiryNotification bool`. `kuma.DNSSpec` gains `DomainExpiryNotification bool`.
- Consumed by: Task 8.
- Note: same as Task 4 — `Equivalent`'s `TypePing`/`TypeDNS` cases need no code change (both specs stay comparable via `==`).

- [ ] **Step 1: Write the failing tests**

Append to `internal/kuma/translate_test.go`:

```go
func TestFromBremlMonitor_RoundTripsThroughToBremlMonitor_PingFields(t *testing.T) {
	spec := MonitorSpec{
		Type: TypePing, Name: "ping-check",
		Ping: &PingSpec{Host: "10.0.0.1", Timeout: 5, PacketSize: 64, DomainExpiryNotification: true},
	}

	mon, err := ToBremlMonitor(7, spec)
	if err != nil {
		t.Fatalf("ToBremlMonitor: %v", err)
	}
	data, err := json.Marshal(mon)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var base bremlmonitor.Base
	if err := json.Unmarshal(data, &base); err != nil {
		t.Fatalf("unmarshal into Base: %v", err)
	}

	got, err := FromBremlMonitor(base)
	if err != nil {
		t.Fatalf("FromBremlMonitor: %v", err)
	}
	if !Equivalent(spec, got) {
		t.Errorf("FromBremlMonitor(round-tripped ToBremlMonitor(%+v)) = %+v, want an equivalent spec", spec, got)
	}
}

func TestFromBremlMonitor_RoundTripsThroughToBremlMonitor_DNSFields(t *testing.T) {
	spec := MonitorSpec{
		Type: TypeDNS, Name: "dns-check",
		DNS: &DNSSpec{Host: "example.com", ResolverServer: "1.1.1.1", ResolveType: "A", DomainExpiryNotification: true},
	}

	mon, err := ToBremlMonitor(7, spec)
	if err != nil {
		t.Fatalf("ToBremlMonitor: %v", err)
	}
	data, err := json.Marshal(mon)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var base bremlmonitor.Base
	if err := json.Unmarshal(data, &base); err != nil {
		t.Fatalf("unmarshal into Base: %v", err)
	}

	got, err := FromBremlMonitor(base)
	if err != nil {
		t.Fatalf("FromBremlMonitor: %v", err)
	}
	if !Equivalent(spec, got) {
		t.Errorf("FromBremlMonitor(round-tripped ToBremlMonitor(%+v)) = %+v, want an equivalent spec", spec, got)
	}
}

func TestEquivalent_DetectsDrift_PingAndDNSFields(t *testing.T) {
	basePing := MonitorSpec{Type: TypePing, Name: "ping-check", Ping: &PingSpec{Host: "10.0.0.1", Timeout: 5}}
	changedPingTimeout := basePing
	changedPingTimeout.Ping = &PingSpec{Host: "10.0.0.1", Timeout: 10}
	if Equivalent(basePing, changedPingTimeout) {
		t.Error("Equivalent(basePing, changedPingTimeout) = true, want false")
	}

	baseDNS := MonitorSpec{Type: TypeDNS, Name: "dns-check", DNS: &DNSSpec{Host: "example.com", ResolverServer: "1.1.1.1", ResolveType: "A"}}
	changedDNSExpiry := baseDNS
	changedDNSExpiry.DNS = &DNSSpec{Host: "example.com", ResolverServer: "1.1.1.1", ResolveType: "A", DomainExpiryNotification: true}
	if Equivalent(baseDNS, changedDNSExpiry) {
		t.Error("Equivalent(baseDNS, changedDNSExpiry) = true, want false")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/kuma/... -run 'PingFields|DNSFields|PingAndDNS' -v`
Expected: compile error (`PingSpec`/`DNSSpec` have no such fields yet).

- [ ] **Step 3: Extend `kuma.PingSpec` and `kuma.DNSSpec`**

In `internal/kuma/spec.go`:

```go
type PingSpec struct {
	Host string

	// Timeout is the per-ping timeout in seconds. 0 = use Kuma default.
	Timeout int64
	// PacketSize is the ICMP packet size in bytes. 0 = use Kuma default.
	PacketSize int
	// DomainExpiryNotification enables domain expiry notifications.
	DomainExpiryNotification bool
}
```

```go
type DNSSpec struct {
	Host           string
	ResolverServer string
	ResolveType    string
	Port           int

	// DomainExpiryNotification enables domain expiry notifications.
	DomainExpiryNotification bool
}
```

- [ ] **Step 4: Wire the fields through `translate.go`**

In `ToBremlMonitor`'s `TypePing` case:

```go
	case TypePing:
		if spec.Ping == nil {
			return nil, fmt.Errorf("kuma: monitor type %s requires the Ping field to be set", TypePing)
		}
		details := bremlmonitor.PingDetails{
			Hostname:                 spec.Ping.Host,
			PacketSize:               spec.Ping.PacketSize,
			DomainExpiryNotification: spec.Ping.DomainExpiryNotification,
		}
		if spec.Ping.Timeout != 0 {
			details.Timeout = &spec.Ping.Timeout
		}
		return &bremlmonitor.Ping{Base: base, PingDetails: details}, nil
```

In `ToBremlMonitor`'s `TypeDNS` case, add `DomainExpiryNotification` to the returned `DNSDetails`:

```go
	case TypeDNS:
		if spec.DNS == nil {
			return nil, fmt.Errorf("kuma: monitor type %s requires the DNS field to be set", TypeDNS)
		}
		return &bremlmonitor.DNS{
			Base: base,
			DNSDetails: bremlmonitor.DNSDetails{
				Hostname:                 spec.DNS.Host,
				ResolverServer:           spec.DNS.ResolverServer,
				ResolveType:              bremlmonitor.DNSResolveType(spec.DNS.ResolveType),
				Port:                     spec.DNS.Port,
				DomainExpiryNotification: spec.DNS.DomainExpiryNotification,
			},
		}, nil
```

In `FromBremlMonitor`'s `"ping"` case:

```go
	case "ping":
		spec.Type = TypePing
		var d bremlmonitor.Ping
		if err := base.As(&d); err != nil {
			return MonitorSpec{}, fmt.Errorf("kuma: decode Ping monitor %d: %w", base.GetID(), err)
		}
		ping := &PingSpec{
			Host: d.Hostname, PacketSize: d.PacketSize, DomainExpiryNotification: d.DomainExpiryNotification,
		}
		if d.Timeout != nil {
			ping.Timeout = *d.Timeout
		}
		spec.Ping = ping
```

In `FromBremlMonitor`'s `"dns"` case, add `DomainExpiryNotification`:

```go
	case "dns":
		spec.Type = TypeDNS
		var d bremlmonitor.DNS
		if err := base.As(&d); err != nil {
			return MonitorSpec{}, fmt.Errorf("kuma: decode DNS monitor %d: %w", base.GetID(), err)
		}
		spec.DNS = &DNSSpec{
			Host: d.Hostname, ResolverServer: d.ResolverServer, ResolveType: string(d.ResolveType), Port: d.Port,
			DomainExpiryNotification: d.DomainExpiryNotification,
		}
```

`Equivalent`'s `TypePing`/`TypeDNS` cases are unchanged (see the Interfaces note above).

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/kuma/... -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/kuma/spec.go internal/kuma/translate.go internal/kuma/translate_test.go
git commit -m "feat: add Ping/DNS-specific Phase 1 fields to kuma package"
```

---

### Task 6: kuma package — Gamedig-specific fields

**Files:**
- Modify: `internal/kuma/spec.go`
- Modify: `internal/kuma/translate.go`
- Test: `internal/kuma/translate_test.go`

**Interfaces:**
- Produces: `kuma.GamedigSpec` gains `GivenPortOnly bool`, `DomainExpiryNotification bool`. Note the deliberate name: it mirrors breml's `GameDigGivenPortOnly` polarity (zero value `false` = Kuma's real default, "guess the port"), not the "GuessPort" phrasing from the original request — see the design spec's "Key decisions" for why the naive inverted name would be a bug.
- Consumed by: Task 8.
- Note: same as Task 4/5 — `Equivalent`'s `TypeGamedig` case needs no code change.

- [ ] **Step 1: Write the failing tests**

Append to `internal/kuma/translate_test.go`:

```go
func TestFromBremlMonitor_RoundTripsThroughToBremlMonitor_GamedigFields(t *testing.T) {
	spec := MonitorSpec{
		Type: TypeGamedig, Name: "game-server",
		Gamedig: &GamedigSpec{
			Host: "game.example.com", Port: 27015, Game: "csgo",
			GivenPortOnly: true, DomainExpiryNotification: true,
		},
	}

	mon, err := ToBremlMonitor(7, spec)
	if err != nil {
		t.Fatalf("ToBremlMonitor: %v", err)
	}
	data, err := json.Marshal(mon)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var base bremlmonitor.Base
	if err := json.Unmarshal(data, &base); err != nil {
		t.Fatalf("unmarshal into Base: %v", err)
	}

	got, err := FromBremlMonitor(base)
	if err != nil {
		t.Fatalf("FromBremlMonitor: %v", err)
	}
	if !Equivalent(spec, got) {
		t.Errorf("FromBremlMonitor(round-tripped ToBremlMonitor(%+v)) = %+v, want an equivalent spec", spec, got)
	}
}

func TestEquivalent_DetectsDrift_GamedigFields(t *testing.T) {
	base := MonitorSpec{Type: TypeGamedig, Name: "game-server", Gamedig: &GamedigSpec{Host: "game.example.com", Port: 27015, Game: "csgo"}}
	changedGivenPortOnly := base
	changedGivenPortOnly.Gamedig = &GamedigSpec{Host: "game.example.com", Port: 27015, Game: "csgo", GivenPortOnly: true}
	if Equivalent(base, changedGivenPortOnly) {
		t.Error("Equivalent(base, changedGivenPortOnly) = true, want false")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/kuma/... -run 'GamedigFields' -v`
Expected: compile error (`GamedigSpec` has no field `GivenPortOnly` etc.).

- [ ] **Step 3: Extend `kuma.GamedigSpec`**

In `internal/kuma/spec.go`:

```go
type GamedigSpec struct {
	Host string
	Port int
	Game string

	// GivenPortOnly, when true, probes only the given port instead of
	// letting Kuma guess it. false (default) matches Kuma's "Guess Port"
	// checked in its UI.
	GivenPortOnly bool
	// DomainExpiryNotification enables domain expiry notifications.
	DomainExpiryNotification bool
}
```

- [ ] **Step 4: Wire the fields through `translate.go`**

In `ToBremlMonitor`'s `TypeGamedig` case:

```go
	case TypeGamedig:
		if spec.Gamedig == nil {
			return nil, fmt.Errorf("kuma: monitor type %s requires the Gamedig field to be set", TypeGamedig)
		}
		return &bremlmonitor.GameDig{
			Base: base,
			GameDigDetails: bremlmonitor.GameDigDetails{
				Hostname:                 spec.Gamedig.Host,
				Port:                     spec.Gamedig.Port,
				Game:                     spec.Gamedig.Game,
				GameDigGivenPortOnly:     spec.Gamedig.GivenPortOnly,
				DomainExpiryNotification: spec.Gamedig.DomainExpiryNotification,
			},
		}, nil
```

In `FromBremlMonitor`'s `"gamedig"` case:

```go
	case "gamedig":
		spec.Type = TypeGamedig
		var d bremlmonitor.GameDig
		if err := base.As(&d); err != nil {
			return MonitorSpec{}, fmt.Errorf("kuma: decode Gamedig monitor %d: %w", base.GetID(), err)
		}
		spec.Gamedig = &GamedigSpec{
			Host: d.Hostname, Port: d.Port, Game: d.Game,
			GivenPortOnly: d.GameDigGivenPortOnly, DomainExpiryNotification: d.DomainExpiryNotification,
		}
```

`Equivalent`'s `TypeGamedig` case is unchanged (see the Interfaces note above).

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/kuma/... -v`
Expected: PASS — this closes out all `internal/kuma` package changes for Phase 1.

- [ ] **Step 6: Commit**

```bash
git add internal/kuma/spec.go internal/kuma/translate.go internal/kuma/translate_test.go
git commit -m "feat: add Gamedig-specific Phase 1 fields to kuma package"
```

---

### Task 7: Monitor CRD — new fields on every type

**Files:**
- Modify: `api/v1alpha1/monitor_types.go`
- Generated (do not hand-edit): `api/v1alpha1/zz_generated.deepcopy.go`, `config/crd/bases/uptime-kuma.io_monitors.yaml`, `charts/uptime-kuma-operator/crds/uptime-kuma.io_monitors.yaml`

**Interfaces:**
- Produces: CRD `MonitorSpec` and every `*MonitorSpec` gain the same fields as their `kuma` package counterparts from Tasks 2–6, with `lowerCamelCase` JSON tags.
- Consumed by: Task 8 (`internal/controller/convert.go`).

- [ ] **Step 1: Extend the CRD types**

In `api/v1alpha1/monitor_types.go`, replace `HTTPMonitorSpec` through `GamedigMonitorSpec` and the common fields of `MonitorSpec`:

```go
// +kubebuilder:object:generate=true
type HTTPMonitorSpec struct {
	URL                 string   `json:"url"`
	Method              string   `json:"method,omitempty"`
	AcceptedStatusCodes []string `json:"acceptedStatusCodes,omitempty"`

	// Timeout is the request timeout in seconds.
	Timeout int64 `json:"timeout,omitempty"`
	// MaxRedirects caps how many redirects the check follows.
	MaxRedirects int32 `json:"maxRedirects,omitempty"`
	// IgnoreTLS skips TLS certificate validation.
	IgnoreTLS bool `json:"ignoreTLS,omitempty"`
	// CacheBust appends a cache-busting query parameter to the request URL.
	CacheBust bool `json:"cacheBust,omitempty"`
	// ExpiryNotification enables TLS certificate expiry notifications.
	ExpiryNotification bool `json:"expiryNotification,omitempty"`
	// DomainExpiryNotification enables domain expiry notifications.
	DomainExpiryNotification bool `json:"domainExpiryNotification,omitempty"`
	// Headers is an opaque, unvalidated passthrough to Kuma.
	Headers string `json:"headers,omitempty"`
	// Body is an opaque, unvalidated passthrough to Kuma.
	Body string `json:"body,omitempty"`
}

// +kubebuilder:object:generate=true
type TCPMonitorSpec struct {
	Host string `json:"host"`
	Port int32  `json:"port"`

	// TLSMode selects the TLS handshake mode: "" (plain TCP), "nostarttls",
	// "secure", or "starttls".
	// +kubebuilder:validation:Enum=;nostarttls;secure;starttls
	TLSMode string `json:"tlsMode,omitempty"`
	// ExpectedSSLAlert is the TLS alert name expected during the handshake
	// (e.g. for mTLS verification).
	ExpectedSSLAlert string `json:"expectedSSLAlert,omitempty"`
	// ExpiryNotification enables TLS certificate expiry notifications. Only
	// honoured by Kuma when TLSMode is "secure" or "starttls".
	ExpiryNotification bool `json:"expiryNotification,omitempty"`
	// DomainExpiryNotification enables domain expiry notifications.
	DomainExpiryNotification bool `json:"domainExpiryNotification,omitempty"`
}

// +kubebuilder:object:generate=true
type PingMonitorSpec struct {
	Host string `json:"host"`

	// Timeout is the per-ping timeout in seconds.
	Timeout int64 `json:"timeout,omitempty"`
	// PacketSize is the ICMP packet size in bytes.
	PacketSize int32 `json:"packetSize,omitempty"`
	// DomainExpiryNotification enables domain expiry notifications.
	DomainExpiryNotification bool `json:"domainExpiryNotification,omitempty"`
}

// +kubebuilder:object:generate=true
type DNSMonitorSpec struct {
	Host           string `json:"host"`
	ResolverServer string `json:"resolverServer,omitempty"`
	ResolveType    string `json:"resolveType,omitempty"`
	Port           int32  `json:"port,omitempty"`

	// DomainExpiryNotification enables domain expiry notifications.
	DomainExpiryNotification bool `json:"domainExpiryNotification,omitempty"`
}

// +kubebuilder:object:generate=true
type GamedigMonitorSpec struct {
	Host string `json:"host"`
	Port int32  `json:"port"`
	Game string `json:"game"`

	// GivenPortOnly, when true, probes only the given port instead of
	// letting Kuma guess it. false (default) matches Kuma's "Guess Port"
	// checked in its UI.
	GivenPortOnly bool `json:"givenPortOnly,omitempty"`
	// DomainExpiryNotification enables domain expiry notifications.
	DomainExpiryNotification bool `json:"domainExpiryNotification,omitempty"`
}
```

And in `MonitorSpec`, add the three common fields after `RetryInterval`:

```go
	Name          string `json:"name,omitempty"`
	Interval      int64  `json:"interval,omitempty"`
	Retries       int64  `json:"retries,omitempty"`
	RetryInterval int64  `json:"retryInterval,omitempty"`

	// Description is shown alongside the monitor in the Kuma UI.
	Description string `json:"description,omitempty"`
	// ResendInterval is how many consecutive failed checks pass between
	// repeated down notifications. 0 disables resending.
	ResendInterval int64 `json:"resendInterval,omitempty"`
	// UpsideDown inverts up/down: the monitor reports "up" when the
	// underlying check fails and "down" when it succeeds.
	UpsideDown bool `json:"upsideDown,omitempty"`
```

(Leave the `HTTP *HTTPMonitorSpec` through `Gamedig *GamedigMonitorSpec` fields and every CEL validation rule exactly as they are — nothing about the discriminator changes.)

- [ ] **Step 2: Regenerate deepcopy and CRD manifests**

Run: `make generate manifests`
Expected: `api/v1alpha1/zz_generated.deepcopy.go`, `config/crd/bases/uptime-kuma.io_monitors.yaml`, and `charts/uptime-kuma-operator/crds/uptime-kuma.io_monitors.yaml` are regenerated with the new fields. Confirm with `git diff --stat` that all three changed.

- [ ] **Step 3: Verify the build**

Run: `go build ./...`
Expected: succeeds (this task adds no new logic to test directly — Task 8 exercises these fields through `toKumaSpec`).

- [ ] **Step 4: Commit**

```bash
git add api/v1alpha1/monitor_types.go api/v1alpha1/zz_generated.deepcopy.go config/crd/bases/uptime-kuma.io_monitors.yaml charts/uptime-kuma-operator/crds/uptime-kuma.io_monitors.yaml
git commit -m "feat: add Phase 1 fields to the Monitor CRD"
```

---

### Task 8: Wire CRD fields into `toKumaSpec`

**Files:**
- Modify: `internal/controller/convert.go`
- Test: `internal/controller/convert_test.go`
- Test: `internal/controller/monitor_controller_test.go`

**Interfaces:**
- Consumes: CRD fields from Task 7, `kuma.MonitorSpec` fields from Tasks 2–6.
- Produces: `toKumaSpec` maps every new CRD field to its `kuma.MonitorSpec` counterpart.

- [ ] **Step 1: Write the failing tests**

Append to `internal/controller/convert_test.go`:

```go
func TestToKumaSpec_Phase1CommonFields(t *testing.T) {
	crd := uptimekumaiov1alpha1.MonitorSpec{
		Type: uptimekumaiov1alpha1.MonitorTypePing,
		Name: "ping-check",
		Description: "a friendly description", ResendInterval: 3, UpsideDown: true,
		Ping: &uptimekumaiov1alpha1.PingMonitorSpec{Host: "10.0.0.1"},
	}

	got := toKumaSpec(crd)
	if got.Description != "a friendly description" || got.ResendInterval != 3 || !got.UpsideDown {
		t.Errorf("common fields = %+v, want Description/ResendInterval/UpsideDown applied", got)
	}
}

func TestToKumaSpec_HTTPFields(t *testing.T) {
	crd := uptimekumaiov1alpha1.MonitorSpec{
		Type: uptimekumaiov1alpha1.MonitorTypeHTTP,
		Name: "web",
		HTTP: &uptimekumaiov1alpha1.HTTPMonitorSpec{
			URL: "https://example.com/", Timeout: 30, MaxRedirects: 5, IgnoreTLS: true,
			CacheBust: true, ExpiryNotification: true, DomainExpiryNotification: true,
			Headers: `{"X-Custom":"value"}`, Body: `{"key":"value"}`,
		},
	}

	got := toKumaSpec(crd)
	if got.HTTP == nil {
		t.Fatal("HTTP is nil")
	}
	if got.HTTP.Timeout != 30 || got.HTTP.MaxRedirects != 5 || !got.HTTP.IgnoreTLS || !got.HTTP.CacheBust ||
		!got.HTTP.ExpiryNotification || !got.HTTP.DomainExpiryNotification ||
		got.HTTP.Headers != `{"X-Custom":"value"}` || got.HTTP.Body != `{"key":"value"}` {
		t.Errorf("HTTP fields = %+v, want all Phase 1 overrides applied", got.HTTP)
	}
}

func TestToKumaSpec_TCPFields(t *testing.T) {
	crd := uptimekumaiov1alpha1.MonitorSpec{
		Type: uptimekumaiov1alpha1.MonitorTypeTCP,
		Name: "port-check",
		TCP: &uptimekumaiov1alpha1.TCPMonitorSpec{
			Host: "example.com", Port: 443,
			TLSMode: "secure", ExpectedSSLAlert: "certificate_required",
			ExpiryNotification: true, DomainExpiryNotification: true,
		},
	}

	got := toKumaSpec(crd)
	if got.TCP == nil {
		t.Fatal("TCP is nil")
	}
	if got.TCP.TLSMode != "secure" || got.TCP.ExpectedSSLAlert != "certificate_required" ||
		!got.TCP.ExpiryNotification || !got.TCP.DomainExpiryNotification {
		t.Errorf("TCP fields = %+v, want all Phase 1 overrides applied", got.TCP)
	}
}

func TestToKumaSpec_PingFields(t *testing.T) {
	crd := uptimekumaiov1alpha1.MonitorSpec{
		Type: uptimekumaiov1alpha1.MonitorTypePing,
		Name: "ping-check",
		Ping: &uptimekumaiov1alpha1.PingMonitorSpec{
			Host: "10.0.0.1", Timeout: 5, PacketSize: 64, DomainExpiryNotification: true,
		},
	}

	got := toKumaSpec(crd)
	if got.Ping == nil {
		t.Fatal("Ping is nil")
	}
	if got.Ping.Timeout != 5 || got.Ping.PacketSize != 64 || !got.Ping.DomainExpiryNotification {
		t.Errorf("Ping fields = %+v, want all Phase 1 overrides applied", got.Ping)
	}
}

func TestToKumaSpec_DNSFields(t *testing.T) {
	crd := uptimekumaiov1alpha1.MonitorSpec{
		Type: uptimekumaiov1alpha1.MonitorTypeDNS,
		Name: "dns-check",
		DNS: &uptimekumaiov1alpha1.DNSMonitorSpec{
			Host: "example.com", DomainExpiryNotification: true,
		},
	}

	got := toKumaSpec(crd)
	if got.DNS == nil {
		t.Fatal("DNS is nil")
	}
	if !got.DNS.DomainExpiryNotification {
		t.Errorf("DNS fields = %+v, want DomainExpiryNotification applied", got.DNS)
	}
}

func TestToKumaSpec_GamedigFields(t *testing.T) {
	crd := uptimekumaiov1alpha1.MonitorSpec{
		Type: uptimekumaiov1alpha1.MonitorTypeGamedig,
		Name: "game-server",
		Gamedig: &uptimekumaiov1alpha1.GamedigMonitorSpec{
			Host: "game.example.com", Port: 27015, Game: "csgo",
			GivenPortOnly: true, DomainExpiryNotification: true,
		},
	}

	got := toKumaSpec(crd)
	if got.Gamedig == nil {
		t.Fatal("Gamedig is nil")
	}
	if !got.Gamedig.GivenPortOnly || !got.Gamedig.DomainExpiryNotification {
		t.Errorf("Gamedig fields = %+v, want GivenPortOnly/DomainExpiryNotification applied", got.Gamedig)
	}
}
```

Append to `internal/controller/monitor_controller_test.go` (this is the plan's required reconciler-level test proving a new field reaches the `FakeClient`, not just a unit-level conversion):

```go
func TestMonitorReconciler_SyncsPhase1Fields(t *testing.T) {
	ctx := context.Background()
	r, fake := newMonitorReconciler(t)

	mon := &uptimekumaiov1alpha1.Monitor{
		ObjectMeta: metav1.ObjectMeta{Name: "phase1-fields", Namespace: "default"},
		Spec: uptimekumaiov1alpha1.MonitorSpec{
			Type:        uptimekumaiov1alpha1.MonitorTypePing,
			Description: "a friendly description",
			Ping:        &uptimekumaiov1alpha1.PingMonitorSpec{Host: "10.0.0.1", PacketSize: 64},
		},
	}
	if err := k8sClient.Create(ctx, mon); err != nil {
		t.Fatalf("create Monitor: %v", err)
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: mon.Name, Namespace: mon.Namespace}}
	defer func() {
		_ = k8sClient.Delete(ctx, mon)
		_, _ = r.Reconcile(ctx, req)
	}()

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	updated := &uptimekumaiov1alpha1.Monitor{}
	if err := k8sClient.Get(ctx, req.NamespacedName, updated); err != nil {
		t.Fatalf("get Monitor: %v", err)
	}
	id, err := strconv.ParseInt(updated.Status.MonitorID, 10, 64)
	if err != nil {
		t.Fatalf("status.monitorID %q is not an integer: %v", updated.Status.MonitorID, err)
	}
	spec, ok := fake.Monitors[id]
	if !ok {
		t.Fatalf("fake Kuma client has no monitor with id %d", id)
	}
	if spec.Description != "a friendly description" {
		t.Errorf("Description = %q, want %q", spec.Description, "a friendly description")
	}
	if spec.Ping == nil || spec.Ping.PacketSize != 64 {
		t.Errorf("Ping = %+v, want PacketSize=64", spec.Ping)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `KUBEBUILDER_ASSETS="$(bin/setup-envtest use -p path 1.31.x)" go test ./internal/controller/... -run 'ToKumaSpec_Phase1|ToKumaSpec_HTTPFields|ToKumaSpec_TCPFields|ToKumaSpec_PingFields|ToKumaSpec_DNSFields|ToKumaSpec_GamedigFields|SyncsPhase1Fields' -v`
Expected: `toKumaSpec` conversion tests fail (fields not copied over — `got.Description` etc. come back empty/false); `TestMonitorReconciler_SyncsPhase1Fields` fails the same way once it compiles.

- [ ] **Step 3: Extend `toKumaSpec`**

Replace `internal/controller/convert.go`'s body:

```go
func toKumaSpec(spec uptimekumaiov1alpha1.MonitorSpec) kuma.MonitorSpec {
	out := kuma.MonitorSpec{
		Type:           kuma.MonitorType(spec.Type),
		Name:           spec.Name,
		Interval:       spec.Interval,
		RetryInterval:  spec.RetryInterval,
		MaxRetries:     spec.Retries,
		Description:    spec.Description,
		ResendInterval: spec.ResendInterval,
		UpsideDown:     spec.UpsideDown,
	}

	if spec.HTTP != nil {
		out.HTTP = &kuma.HTTPSpec{
			URL:                       spec.HTTP.URL,
			Method:                    spec.HTTP.Method,
			AcceptedStatusCodes:       spec.HTTP.AcceptedStatusCodes,
			Timeout:                   spec.HTTP.Timeout,
			MaxRedirects:              int(spec.HTTP.MaxRedirects),
			IgnoreTLS:                 spec.HTTP.IgnoreTLS,
			CacheBust:                 spec.HTTP.CacheBust,
			ExpiryNotification:        spec.HTTP.ExpiryNotification,
			DomainExpiryNotification:  spec.HTTP.DomainExpiryNotification,
			Headers:                   spec.HTTP.Headers,
			Body:                      spec.HTTP.Body,
		}
	}
	if spec.TCP != nil {
		out.TCP = &kuma.TCPSpec{
			Host: spec.TCP.Host, Port: int(spec.TCP.Port),
			TLSMode: spec.TCP.TLSMode, ExpectedSSLAlert: spec.TCP.ExpectedSSLAlert,
			ExpiryNotification:       spec.TCP.ExpiryNotification,
			DomainExpiryNotification: spec.TCP.DomainExpiryNotification,
		}
	}
	if spec.Ping != nil {
		out.Ping = &kuma.PingSpec{
			Host: spec.Ping.Host, Timeout: spec.Ping.Timeout, PacketSize: int(spec.Ping.PacketSize),
			DomainExpiryNotification: spec.Ping.DomainExpiryNotification,
		}
	}
	if spec.DNS != nil {
		out.DNS = &kuma.DNSSpec{
			Host: spec.DNS.Host, ResolverServer: spec.DNS.ResolverServer,
			ResolveType: spec.DNS.ResolveType, Port: int(spec.DNS.Port),
			DomainExpiryNotification: spec.DNS.DomainExpiryNotification,
		}
	}
	if spec.Gamedig != nil {
		out.Gamedig = &kuma.GamedigSpec{
			Host: spec.Gamedig.Host, Port: int(spec.Gamedig.Port), Game: spec.Gamedig.Game,
			GivenPortOnly:            spec.Gamedig.GivenPortOnly,
			DomainExpiryNotification: spec.Gamedig.DomainExpiryNotification,
		}
	}
	return out
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `make test`
Expected: PASS, full suite green (this is the first task that needs the real envtest-backed suite, since `TestMonitorReconciler_SyncsPhase1Fields` uses `k8sClient`).

- [ ] **Step 5: Commit**

```bash
git add internal/controller/convert.go internal/controller/convert_test.go internal/controller/monitor_controller_test.go
git commit -m "feat: wire Phase 1 CRD fields into toKumaSpec"
```

---

### Task 9: Apply annotation overrides in `internal/derive`, plus `path`

**Files:**
- Modify: `internal/derive/ingress.go`
- Modify: `internal/derive/httproute.go`
- Test: `internal/derive/ingress_test.go`
- Test: `internal/derive/httproute_test.go`
- Test: `internal/controller/ingress_controller_test.go`

**Interfaces:**
- Consumes: `annotations.Overrides` fields from Task 1, `kuma.MonitorSpec`/`kuma.HTTPSpec` fields from Tasks 2–3.
- Produces: `IngressMonitors` and `HTTPRouteMonitors` apply every common + HTTP-specific override, and build the derived URL using `ov.Path` (default `/`) instead of a hardcoded trailing slash.

- [ ] **Step 1: Write the failing tests**

Append to `internal/derive/ingress_test.go`:

```go
func TestIngressMonitors_Phase1OverridesApplied(t *testing.T) {
	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "web"},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{{Host: "app.example.com"}},
		},
	}
	ov := annotations.Overrides{
		Description: "a friendly description", ResendInterval: 3, UpsideDown: true,
		Timeout: 30, MaxRedirects: 5, IgnoreTLS: true, CacheBust: true,
		ExpiryNotification: true, DomainExpiryNotification: true,
		Headers: `{"X-Custom":"value"}`, Body: `{"key":"value"}`,
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

	got := IngressMonitors(ing, annotations.Overrides{Path: "/healthz"})
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
```

Append to `internal/derive/httproute_test.go`:

```go
func TestHTTPRouteMonitors_PathOverride(t *testing.T) {
	route := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "web"},
		Spec: gatewayv1.HTTPRouteSpec{
			Hostnames: []gatewayv1.Hostname{"app.example.com"},
		},
	}

	got := HTTPRouteMonitors(route, annotations.Overrides{Path: "/healthz"})
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
		Timeout: 30, MaxRedirects: 5, IgnoreTLS: true, CacheBust: true,
		ExpiryNotification: true, DomainExpiryNotification: true,
		Headers: `{"X-Custom":"value"}`, Body: `{"key":"value"}`,
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
```

Append to `internal/controller/ingress_controller_test.go` (the plan's required reconciler-level test proving an *annotation* reaches the `FakeClient`):

```go
func TestIngressReconciler_SyncsPhase1Overrides(t *testing.T) {
	ctx := context.Background()
	r, fake := newIngressReconciler(false)

	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name: "phase1-overrides", Namespace: "default",
			Annotations: map[string]string{
				annotations.Enabled:     "true",
				annotations.Description: "a friendly description",
				annotations.IgnoreTLS:   "true",
				annotations.Path:        "/healthz",
			},
		},
		Spec: networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{Host: "app.example.com"}}},
	}
	if err := k8sClient.Create(ctx, ing); err != nil {
		t.Fatalf("create Ingress: %v", err)
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: ing.Name, Namespace: ing.Namespace}}
	defer func() {
		_ = k8sClient.Delete(ctx, ing)
		_, _ = r.Reconcile(ctx, req)
	}()

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(fake.Monitors) != 1 {
		t.Fatalf("expected 1 monitor, got %d", len(fake.Monitors))
	}
	var spec kuma.MonitorSpec
	for _, s := range fake.Monitors {
		spec = s
	}
	if spec.Description != "a friendly description" {
		t.Errorf("Description = %q, want %q", spec.Description, "a friendly description")
	}
	if spec.HTTP == nil || !spec.HTTP.IgnoreTLS {
		t.Errorf("HTTP.IgnoreTLS = %v, want true", spec.HTTP)
	}
	if spec.HTTP.URL != "http://app.example.com/healthz" {
		t.Errorf("URL = %q, want %q", spec.HTTP.URL, "http://app.example.com/healthz")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/derive/... -run 'Phase1|PathOverride|NoPathOverride' -v`
Expected: FAIL — the derived spec has none of the new fields set, and the URL is always `.../<host>/` regardless of `ov.Path`.

- [ ] **Step 3: Apply the overrides in `internal/derive/ingress.go`**

Replace the loop body's tail (from `scheme := "http"` through the `append`):

```go
		scheme := "http"
		if tlsHosts[rule.Host] {
			scheme = "https"
		}
		if ov.Scheme != "" {
			scheme = ov.Scheme
		}

		path := "/"
		if ov.Path != "" {
			path = ov.Path
		}

		name := ov.Name
		if name == "" {
			name = fmt.Sprintf("%s/%s/%s", ing.Namespace, ing.Name, rule.Host)
		}

		out = append(out, DesiredMonitor{
			Host: rule.Host,
			Spec: kuma.MonitorSpec{
				Type:           kuma.TypeHTTP,
				Name:           name,
				Interval:       ov.Interval,
				RetryInterval:  ov.RetryInterval,
				MaxRetries:     ov.MaxRetries,
				Description:    ov.Description,
				ResendInterval: ov.ResendInterval,
				UpsideDown:     ov.UpsideDown,
				HTTP: &kuma.HTTPSpec{
					URL:                       fmt.Sprintf("%s://%s%s", scheme, rule.Host, path),
					AcceptedStatusCodes:       ov.AcceptedStatusCodes,
					Timeout:                   ov.Timeout,
					MaxRedirects:              int(ov.MaxRedirects),
					IgnoreTLS:                 ov.IgnoreTLS,
					CacheBust:                 ov.CacheBust,
					ExpiryNotification:        ov.ExpiryNotification,
					DomainExpiryNotification:  ov.DomainExpiryNotification,
					Headers:                   ov.Headers,
					Body:                      ov.Body,
				},
			},
		})
```

- [ ] **Step 4: Apply the same overrides in `internal/derive/httproute.go`**

Replace the loop body's tail (from `name := ov.Name` through the `append`) — note `HTTPRouteMonitors` has no per-host TLS lookup, so `scheme` is computed earlier in the function and only `path` and the `append` block need the same additions as Step 3:

```go
		path := "/"
		if ov.Path != "" {
			path = ov.Path
		}

		name := ov.Name
		if name == "" {
			name = fmt.Sprintf("%s/%s/%s", route.Namespace, route.Name, host)
		}

		out = append(out, DesiredMonitor{
			Host: host,
			Spec: kuma.MonitorSpec{
				Type:           kuma.TypeHTTP,
				Name:           name,
				Interval:       ov.Interval,
				RetryInterval:  ov.RetryInterval,
				MaxRetries:     ov.MaxRetries,
				Description:    ov.Description,
				ResendInterval: ov.ResendInterval,
				UpsideDown:     ov.UpsideDown,
				HTTP: &kuma.HTTPSpec{
					URL:                       fmt.Sprintf("%s://%s%s", scheme, host, path),
					AcceptedStatusCodes:       ov.AcceptedStatusCodes,
					Timeout:                   ov.Timeout,
					MaxRedirects:              int(ov.MaxRedirects),
					IgnoreTLS:                 ov.IgnoreTLS,
					CacheBust:                 ov.CacheBust,
					ExpiryNotification:        ov.ExpiryNotification,
					DomainExpiryNotification:  ov.DomainExpiryNotification,
					Headers:                   ov.Headers,
					Body:                      ov.Body,
				},
			},
		})
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `make test`
Expected: PASS, full suite green.

- [ ] **Step 6: Commit**

```bash
git add internal/derive/ingress.go internal/derive/httproute.go internal/derive/ingress_test.go internal/derive/httproute_test.go internal/controller/ingress_controller_test.go
git commit -m "feat: apply Phase 1 annotation overrides and path in derive package"
```

---

### Task 10: Documentation

**Files:**
- Modify: `README.md`
- Modify: `docs/superpowers/specs/2026-09-14-uptime-kuma-operator-design.md`
- Modify: `config/samples/uptime-kuma_v1alpha1_monitor.yaml`

**Interfaces:**
- Consumes: nothing new — purely documents Tasks 1–9's finished surface.

- [ ] **Step 1: Extend the annotation contract table in the main design spec**

In `docs/superpowers/specs/2026-09-14-uptime-kuma-operator-design.md`, the annotation table currently ends with (search for this exact block):

```
| `uptime-kuma.io/accepted-statuscodes` | Comma-separated list. Optional. |
| `uptime-kuma.io/monitor-ids` | **Operator-written.** JSON map `{"host": "kumaMonitorID"}`. One Ingress/HTTPRoute can expand into multiple monitors (one per host). |
```

Insert the new rows between them:

```
| `uptime-kuma.io/accepted-statuscodes` | Comma-separated list. Optional. |
| `uptime-kuma.io/path` | Path appended after the host to build the derived URL. Default: `/`. Must start with `/`. |
| `uptime-kuma.io/description` | Free-text description shown in the Kuma UI. Optional. |
| `uptime-kuma.io/resend-interval` | How many consecutive failed checks pass between repeated down notifications. `0` disables resending (Kuma default). |
| `uptime-kuma.io/upside-down` | `"true"` / `"false"`. Inverts up/down semantics. Default: `false`. |
| `uptime-kuma.io/timeout` | Request timeout in seconds. Optional, Kuma default otherwise. |
| `uptime-kuma.io/max-redirects` | Maximum redirects to follow. Optional. |
| `uptime-kuma.io/ignore-tls` | `"true"` / `"false"`. Skip TLS certificate validation. Default: `false`. |
| `uptime-kuma.io/cache-bust` | `"true"` / `"false"`. Append a cache-busting query parameter. Default: `false`. |
| `uptime-kuma.io/expiry-notification` | `"true"` / `"false"`. TLS certificate expiry notifications. Default: `false`. |
| `uptime-kuma.io/domain-expiry-notification` | `"true"` / `"false"`. Domain expiry notifications. Default: `false`. |
| `uptime-kuma.io/headers` | Opaque HTTP headers passthrough (same raw text the Kuma UI accepts). Optional. |
| `uptime-kuma.io/body` | Opaque HTTP request body passthrough. Optional. |
| `uptime-kuma.io/monitor-ids` | **Operator-written.** JSON map `{"host": "kumaMonitorID"}`. One Ingress/HTTPRoute can expand into multiple monitors (one per host). |
```

Also add a one-line pointer near the top of that table (or in the file's own decisions log, whichever reads more naturally alongside the existing prose) noting that TCP/Ping/DNS/Gamedig-specific fields are Monitor-CRD-only and have no annotation equivalent, with a link to `docs/superpowers/specs/2026-09-15-monitor-config-expansion-phase1-design.md` for the full field list.

- [ ] **Step 2: Update the Monitor CRD sample**

In `config/samples/uptime-kuma_v1alpha1_monitor.yaml`, add a second, HTTP-typed example showing the new fields (keep the existing Gamedig example as-is):

```yaml
---
apiVersion: uptime-kuma.io/v1alpha1
kind: Monitor
metadata:
  name: http-sample
spec:
  type: HTTP
  name: "Sample HTTP Monitor"
  interval: 60
  description: "Example showing Phase 1 fields"
  resendInterval: 3
  http:
    url: https://example.com/health
    timeout: 30
    maxRedirects: 5
    ignoreTLS: false
    cacheBust: false
    expiryNotification: true
    domainExpiryNotification: true
```

- [ ] **Step 3: Update the README**

In `README.md`, near the existing "Opt an Ingress in" section (where `uptime-kuma.io/enabled` and the override annotations are already documented), add a short paragraph pointing to the full annotation table in the design spec and the CRD sample, e.g.:

```markdown
   Phase 1 monitor configuration (description, timeouts, TLS options,
   notification toggles, custom headers/body, and more) is available via
   both annotations (HTTP-applicable fields only — see the annotation
   table in `docs/superpowers/specs/2026-09-14-uptime-kuma-operator-design.md`)
   and the Monitor CRD (all types — see
   `config/samples/uptime-kuma_v1alpha1_monitor.yaml`).
```

- [ ] **Step 4: Verify**

Run: `make test` and `helm lint charts/uptime-kuma-operator --set kuma.url=https://x --set kuma.existingSecret=s`
Expected: both clean (documentation-only changes shouldn't affect either, this just confirms nothing else broke).

- [ ] **Step 5: Commit**

```bash
git add README.md docs/superpowers/specs/2026-09-14-uptime-kuma-operator-design.md config/samples/uptime-kuma_v1alpha1_monitor.yaml
git commit -m "docs: document Phase 1 monitor configuration fields"
```
