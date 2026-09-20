# Service-based Monitors with a Type Annotation — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a plain `Service` be opted into Kuma monitoring via annotations alone, producing a monitor of any type (HTTP/TCP/Ping/DNS/Gamedig) selected by a new `uptime-kuma.io/type` annotation, while rescoping the whole annotation contract to mirror the `Monitor` CRD's per-type sub-specs.

**Architecture:** A new `ServiceReconciler` reuses the existing `reconcileHostBasedResource` shared reconcile loop (same one `IngressReconciler`/`HTTPRouteReconciler` use), fed by a new `derive.ServiceMonitors` function that always yields exactly one `DesiredMonitor` per Service, of whichever Kuma type `uptime-kuma.io/type` selects (default `HTTP`). Getting there requires first restructuring `internal/annotations.Overrides` from a flat struct into one mirroring `MonitorSpec`'s top-level-plus-sub-spec shape, since annotations are now scoped per type (`http.uptime-kuma.io/timeout`, `tcp.uptime-kuma.io/host`, etc.) rather than flat.

**Tech Stack:** Go, controller-runtime, envtest (Kubernetes API server for controller tests), Helm (chart RBAC).

**Spec:** `docs/superpowers/specs/2026-09-20-service-monitor-design.md`

## Global Constraints

- Module path is `uptime-kuma-operator`; internal packages are imported as `uptime-kuma-operator/internal/...`.
- This is a **breaking change** to every existing annotation name from Phase 1/2 — no backwards-compatibility aliases, no deprecation period (per spec: "the operator has no users yet").
- Annotation keys are all-lowercase, kebab-case, under the `uptime-kuma.io/` prefix (unscoped) or `<type>.uptime-kuma.io/` prefix (scoped — the type lives in the DNS-subdomain prefix, since a Kubernetes annotation key allows only one `/`) — exact names are given per-task below, copied from the spec.
- `go test ./internal/annotations/...` and `go test ./internal/derive/...` run without any special setup. `go test ./internal/controller/...` (and `./cmd/...`) need envtest binaries — use `make test` (sets `KUBEBUILDER_ASSETS` automatically) unless already exported in your shell.
- Follow TDD: write the failing test, confirm it fails for the right reason, then write the minimal implementation, then confirm it passes.
- Every commit message body ends with the attribution line configured for this session (already present in this repo's recent commits as `Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>`).
- Do not touch `api/v1alpha1/monitor_types.go`, `internal/controller/monitor_controller.go`, or `internal/controller/convert.go` — the `Monitor` CRD path is explicitly out of scope (spec's "Out of scope" section).

---

## Task 1: Rescope `internal/annotations`

**Files:**
- Modify: `internal/annotations/annotations.go`
- Test: `internal/annotations/annotations_test.go`

**Interfaces:**
- Produces: the full rescoped `annotations.Overrides` struct (`Name, Type, Group string`; `Interval, RetryInterval, MaxRetries, ResendInterval, Proxy int64`; `Description string`; `UpsideDown bool`; `Notifications, Tags []string`; `HTTP HTTPOverrides`; `TCP TCPOverrides`; `Ping PingOverrides`; `DNS DNSOverrides`; `Gamedig GamedigOverrides`) and every renamed/new constant, consumed by Task 2 (`derive` package) and Task 3 (`controller` package).

This task fully replaces the file. `ShouldSync`, `ParseMonitorIDs`, `SetMonitorIDs`, `ParseSyncedHash`, `SetSyncedHash`, and the low-level `parseIntAnnotation`/`parseIntAnnotationForIntRange`/`parseBoolAnnotation` helpers are unchanged — only `Overrides` and `ParseOverrides` (and the annotation-key constants) change. After this task, `internal/derive` and `internal/controller` will **not** compile yet — that's expected and fixed in Tasks 2 and 3.

- [ ] **Step 1: Replace `internal/annotations/annotations_test.go` with the rescoped test suite**

```go
package annotations

import (
	"reflect"
	"testing"
)

func TestShouldSync(t *testing.T) {
	cases := []struct {
		name           string
		optInByDefault bool
		ann            map[string]string
		want           bool
	}{
		{"opt-in required, no annotation", false, nil, false},
		{"opt-in required, enabled=true", false, map[string]string{Enabled: "true"}, true},
		{"opt-in required, enabled=false", false, map[string]string{Enabled: "false"}, false},
		{"opt-in by default, no annotation", true, nil, true},
		{"opt-in by default, enabled=false opts out", true, map[string]string{Enabled: "false"}, false},
		{"opt-in by default, enabled=true", true, map[string]string{Enabled: "true"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ShouldSync(c.optInByDefault, c.ann); got != c.want {
				t.Errorf("ShouldSync(%v, %v) = %v, want %v", c.optInByDefault, c.ann, got, c.want)
			}
		})
	}
}

func TestParseOverrides_TopLevelFields(t *testing.T) {
	ov, err := ParseOverrides(map[string]string{
		Name:           "friendly-name",
		Type:           "TCP",
		Interval:       "30",
		RetryInterval:  "10",
		MaxRetries:     "5",
		Description:    "a friendly description",
		ResendInterval: "3",
		UpsideDown:     "true",
		Notifications:  "slack-prod, email-oncall",
		Group:          "prod-services",
		Proxy:          "7",
		Tags:           "env-prod, team-platform",
	})
	if err != nil {
		t.Fatalf("ParseOverrides: %v", err)
	}
	want := Overrides{
		Name: "friendly-name", Type: "TCP",
		Interval: 30, RetryInterval: 10, MaxRetries: 5,
		Description: "a friendly description", ResendInterval: 3, UpsideDown: true,
		Notifications: []string{"slack-prod", "email-oncall"},
		Group:         "prod-services",
		Proxy:         7,
		Tags:          []string{"env-prod", "team-platform"},
	}
	if !reflect.DeepEqual(ov, want) {
		t.Errorf("ParseOverrides = %+v, want %+v", ov, want)
	}
}

func TestParseOverrides_InvalidInteger(t *testing.T) {
	if _, err := ParseOverrides(map[string]string{Interval: "not-a-number"}); err == nil {
		t.Fatal("expected error for non-integer interval, got nil")
	}
}

func TestParseOverrides_InvalidBoolean(t *testing.T) {
	if _, err := ParseOverrides(map[string]string{UpsideDown: "not-a-bool"}); err == nil {
		t.Fatal("expected error for non-boolean upside-down, got nil")
	}
}

func TestParseOverrides_InvalidProxy(t *testing.T) {
	if _, err := ParseOverrides(map[string]string{Proxy: "not-a-number"}); err == nil {
		t.Fatal("expected error for non-integer proxy, got nil")
	}
}

// A trailing or doubled comma must not yield an empty-string entry: for tags
// that would create and attach a blank tag in Kuma, and for notifications it
// fails the reconcile with a confusing `channel "" not found` error.
func TestParseOverrides_SkipsEmptyCommaSeparatedEntries(t *testing.T) {
	ov, err := ParseOverrides(map[string]string{
		Tags:          "env-prod,,team-platform, ",
		Notifications: "slack-prod,,email-oncall, ",
	})
	if err != nil {
		t.Fatalf("ParseOverrides: %v", err)
	}
	if !reflect.DeepEqual(ov.Tags, []string{"env-prod", "team-platform"}) {
		t.Errorf("Tags = %v, want [env-prod team-platform] with no empty entries", ov.Tags)
	}
	if !reflect.DeepEqual(ov.Notifications, []string{"slack-prod", "email-oncall"}) {
		t.Errorf("Notifications = %v, want [slack-prod email-oncall] with no empty entries", ov.Notifications)
	}
}

func TestParseOverrides_HTTPFields(t *testing.T) {
	ov, err := ParseOverrides(map[string]string{
		HTTPHost:                     "custom.example.com",
		HTTPPort:                     "8080",
		HTTPScheme:                   "https",
		HTTPPath:                     "/healthz",
		HTTPAcceptedStatusCodes:      "200-299, 301",
		HTTPTimeout:                  "10",
		HTTPMaxRedirects:             "5",
		HTTPIgnoreTLS:                "true",
		HTTPCacheBust:                "true",
		HTTPExpiryNotification:       "true",
		HTTPDomainExpiryNotification: "true",
		HTTPHeaders:                  `{"X-Custom":"value"}`,
		HTTPBody:                     `{"key":"value"}`,
	})
	if err != nil {
		t.Fatalf("ParseOverrides: %v", err)
	}
	want := HTTPOverrides{
		Host: "custom.example.com", Port: "8080",
		Scheme: "https", Path: "/healthz",
		AcceptedStatusCodes:      []string{"200-299", "301"},
		Timeout:                  10,
		MaxRedirects:             5,
		IgnoreTLS:                true,
		CacheBust:                true,
		ExpiryNotification:       true,
		DomainExpiryNotification: true,
		Headers:                  `{"X-Custom":"value"}`,
		Body:                     `{"key":"value"}`,
	}
	if !reflect.DeepEqual(ov.HTTP, want) {
		t.Errorf("ov.HTTP = %+v, want %+v", ov.HTTP, want)
	}
}

func TestParseOverrides_InvalidHTTPScheme(t *testing.T) {
	if _, err := ParseOverrides(map[string]string{HTTPScheme: "ftp"}); err == nil {
		t.Fatal("expected error for invalid scheme, got nil")
	}
}

func TestParseOverrides_InvalidHTTPPath(t *testing.T) {
	if _, err := ParseOverrides(map[string]string{HTTPPath: "no-leading-slash"}); err == nil {
		t.Fatal("expected error for path without leading slash, got nil")
	}
}

func TestParseOverrides_HTTPPathUnsetDefaultsEmpty(t *testing.T) {
	ov, err := ParseOverrides(nil)
	if err != nil {
		t.Fatalf("ParseOverrides: %v", err)
	}
	if ov.HTTP.Path != "" {
		t.Errorf("HTTP.Path = %q, want empty string when unset", ov.HTTP.Path)
	}
}

func TestParseOverrides_TCPFields(t *testing.T) {
	ov, err := ParseOverrides(map[string]string{
		TCPHost:                     "tcp.example.com",
		TCPPort:                     "5432",
		TCPTLSMode:                  "secure",
		TCPExpectedSSLAlert:         "unrecognized_name",
		TCPExpiryNotification:       "true",
		TCPDomainExpiryNotification: "true",
	})
	if err != nil {
		t.Fatalf("ParseOverrides: %v", err)
	}
	want := TCPOverrides{
		Host: "tcp.example.com", Port: "5432",
		TLSMode: "secure", ExpectedSSLAlert: "unrecognized_name",
		ExpiryNotification: true, DomainExpiryNotification: true,
	}
	if !reflect.DeepEqual(ov.TCP, want) {
		t.Errorf("ov.TCP = %+v, want %+v", ov.TCP, want)
	}
}

func TestParseOverrides_InvalidTCPTLSMode(t *testing.T) {
	if _, err := ParseOverrides(map[string]string{TCPTLSMode: "bogus"}); err == nil {
		t.Fatal("expected error for invalid tls-mode, got nil")
	}
}

func TestParseOverrides_PingFields(t *testing.T) {
	ov, err := ParseOverrides(map[string]string{
		PingHost:                     "ping.example.com",
		PingTimeout:                  "5",
		PingPacketSize:               "56",
		PingDomainExpiryNotification: "true",
	})
	if err != nil {
		t.Fatalf("ParseOverrides: %v", err)
	}
	want := PingOverrides{
		Host: "ping.example.com", Timeout: 5, PacketSize: 56, DomainExpiryNotification: true,
	}
	if !reflect.DeepEqual(ov.Ping, want) {
		t.Errorf("ov.Ping = %+v, want %+v", ov.Ping, want)
	}
}

func TestParseOverrides_DNSFields(t *testing.T) {
	ov, err := ParseOverrides(map[string]string{
		DNSHost:                     "dns.example.com",
		DNSPort:                     "5353",
		DNSResolverServer:           "1.1.1.1",
		DNSResolveType:              "A",
		DNSDomainExpiryNotification: "true",
	})
	if err != nil {
		t.Fatalf("ParseOverrides: %v", err)
	}
	want := DNSOverrides{
		Host: "dns.example.com", Port: 5353,
		ResolverServer: "1.1.1.1", ResolveType: "A",
		DomainExpiryNotification: true,
	}
	if !reflect.DeepEqual(ov.DNS, want) {
		t.Errorf("ov.DNS = %+v, want %+v", ov.DNS, want)
	}
}

func TestParseOverrides_GamedigFields(t *testing.T) {
	ov, err := ParseOverrides(map[string]string{
		GamedigHost:                     "game.example.com",
		GamedigPort:                     "27015",
		GamedigGame:                     "csgo",
		GamedigGivenPortOnly:            "true",
		GamedigDomainExpiryNotification: "true",
	})
	if err != nil {
		t.Fatalf("ParseOverrides: %v", err)
	}
	want := GamedigOverrides{
		Host: "game.example.com", Port: "27015",
		Game: "csgo", GivenPortOnly: true, DomainExpiryNotification: true,
	}
	if !reflect.DeepEqual(ov.Gamedig, want) {
		t.Errorf("ov.Gamedig = %+v, want %+v", ov.Gamedig, want)
	}
}

func TestMonitorIDsRoundTrip(t *testing.T) {
	ann := map[string]string{}
	ann = SetMonitorIDs(ann, map[string]string{"a.example.com": "1", "b.example.com": "2"})

	got, err := ParseMonitorIDs(ann)
	if err != nil {
		t.Fatalf("ParseMonitorIDs: %v", err)
	}
	if got["a.example.com"] != "1" || got["b.example.com"] != "2" {
		t.Errorf("ParseMonitorIDs = %v, want {a.example.com:1 b.example.com:2}", got)
	}
}

func TestSetMonitorIDs_EmptyRemovesAnnotation(t *testing.T) {
	ann := map[string]string{MonitorIDs: `{"a":"1"}`}
	ann = SetMonitorIDs(ann, map[string]string{})
	if _, ok := ann[MonitorIDs]; ok {
		t.Error("MonitorIDs annotation still present after clearing to empty map")
	}
}

func TestSyncedHashRoundTrip(t *testing.T) {
	ann := map[string]string{}
	ann = SetSyncedHash(ann, "deadbeef")
	if got := ParseSyncedHash(ann); got != "deadbeef" {
		t.Errorf("ParseSyncedHash = %q, want %q", got, "deadbeef")
	}
}

func TestSetSyncedHash_EmptyRemovesAnnotation(t *testing.T) {
	ann := map[string]string{SyncedHash: "deadbeef"}
	ann = SetSyncedHash(ann, "")
	if _, ok := ann[SyncedHash]; ok {
		t.Error("SyncedHash annotation still present after clearing to empty string")
	}
}

func TestParseSyncedHash_MissingAnnotationIsEmptyString(t *testing.T) {
	if got := ParseSyncedHash(nil); got != "" {
		t.Errorf("ParseSyncedHash(nil) = %q, want empty string", got)
	}
}

func TestParseMonitorIDs_MissingAnnotationIsEmptyMap(t *testing.T) {
	got, err := ParseMonitorIDs(nil)
	if err != nil {
		t.Fatalf("ParseMonitorIDs: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ParseMonitorIDs(nil) = %v, want empty map", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail to compile against the current (flat) `Overrides`**

Run: `go test ./internal/annotations/...`
Expected: FAIL — compile errors like `unknown field Type in struct literal` and `undefined: HTTPHost` etc. (the old flat fields/constants don't exist under these names/shapes yet).

- [ ] **Step 3: Replace `internal/annotations/annotations.go`**

```go
package annotations

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Top-level annotations mirror api/v1alpha1.MonitorSpec's own top-level
// fields — the ones that exist once regardless of monitor type.
const (
	Enabled = "uptime-kuma.io/enabled"
	Name    = "uptime-kuma.io/name"
	// Type selects the Kuma monitor type for a Service ("" defaults to
	// HTTP): "HTTP", "TCP", "Ping", "DNS", or "Gamedig". Ingress/HTTPRoute
	// never read this — they are always HTTP.
	Type          = "uptime-kuma.io/type"
	Interval      = "uptime-kuma.io/interval"
	RetryInterval = "uptime-kuma.io/retry-interval"
	MaxRetries    = "uptime-kuma.io/max-retries"

	Description    = "uptime-kuma.io/description"
	ResendInterval = "uptime-kuma.io/resend-interval"
	UpsideDown     = "uptime-kuma.io/upside-down"

	Tags          = "uptime-kuma.io/tags"
	Notifications = "uptime-kuma.io/notifications"
	Proxy         = "uptime-kuma.io/proxy"
	Group         = "uptime-kuma.io/group"

	MonitorIDs = "uptime-kuma.io/monitor-ids"
	SyncedHash = "uptime-kuma.io/synced-hash"
	Finalizer  = "uptime-kuma.io/finalizer"
)

// HTTP-scoped annotations mirror api/v1alpha1.HTTPMonitorSpec. Host and
// Port have no HTTPMonitorSpec equivalent (the CRD takes a literal url
// instead) — they exist only to let a Service-derived HTTP monitor
// synthesize one.
const (
	HTTPHost                     = "http.uptime-kuma.io/host"
	HTTPPort                     = "http.uptime-kuma.io/port"
	HTTPScheme                   = "http.uptime-kuma.io/scheme"
	HTTPPath                     = "http.uptime-kuma.io/path"
	HTTPAcceptedStatusCodes      = "http.uptime-kuma.io/accepted-statuscodes"
	HTTPTimeout                  = "http.uptime-kuma.io/timeout"
	HTTPMaxRedirects             = "http.uptime-kuma.io/max-redirects"
	HTTPIgnoreTLS                = "http.uptime-kuma.io/ignore-tls"
	HTTPCacheBust                = "http.uptime-kuma.io/cache-bust"
	HTTPExpiryNotification       = "http.uptime-kuma.io/expiry-notification"
	HTTPDomainExpiryNotification = "http.uptime-kuma.io/domain-expiry-notification"
	HTTPHeaders                  = "http.uptime-kuma.io/headers"
	HTTPBody                     = "http.uptime-kuma.io/body"
)

// TCP-scoped annotations mirror api/v1alpha1.TCPMonitorSpec.
const (
	TCPHost                     = "tcp.uptime-kuma.io/host"
	TCPPort                     = "tcp.uptime-kuma.io/port"
	TCPTLSMode                  = "tcp.uptime-kuma.io/tls-mode"
	TCPExpectedSSLAlert         = "tcp.uptime-kuma.io/expected-ssl-alert"
	TCPExpiryNotification       = "tcp.uptime-kuma.io/expiry-notification"
	TCPDomainExpiryNotification = "tcp.uptime-kuma.io/domain-expiry-notification"
)

// Ping-scoped annotations mirror api/v1alpha1.PingMonitorSpec. There is no
// Ping port annotation — ICMP has no port concept in Kuma.
const (
	PingHost                     = "ping.uptime-kuma.io/host"
	PingTimeout                  = "ping.uptime-kuma.io/timeout"
	PingPacketSize               = "ping.uptime-kuma.io/packet-size"
	PingDomainExpiryNotification = "ping.uptime-kuma.io/domain-expiry-notification"
)

// DNS-scoped annotations mirror api/v1alpha1.DNSMonitorSpec. DNSPort is the
// resolver's query port, not a Service port — see DNSOverrides.Port.
const (
	DNSHost                     = "dns.uptime-kuma.io/host"
	DNSPort                     = "dns.uptime-kuma.io/port"
	DNSResolverServer           = "dns.uptime-kuma.io/resolver-server"
	DNSResolveType              = "dns.uptime-kuma.io/resolve-type"
	DNSDomainExpiryNotification = "dns.uptime-kuma.io/domain-expiry-notification"
)

// Gamedig-scoped annotations mirror api/v1alpha1.GamedigMonitorSpec.
const (
	GamedigHost                     = "gamedig.uptime-kuma.io/host"
	GamedigPort                     = "gamedig.uptime-kuma.io/port"
	GamedigGame                     = "gamedig.uptime-kuma.io/game"
	GamedigGivenPortOnly            = "gamedig.uptime-kuma.io/given-port-only"
	GamedigDomainExpiryNotification = "gamedig.uptime-kuma.io/domain-expiry-notification"
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
// from annotations, mirroring api/v1alpha1.MonitorSpec's own shape: fields
// here are the ones that exist once regardless of monitor type; per-type
// fields live in HTTP/TCP/Ping/DNS/Gamedig. Zero values mean "not set,
// caller picks a default".
type Overrides struct {
	Name string
	// Type selects the monitor type ("" defaults to HTTP). Only consulted
	// by Service-derived monitors — Ingress/HTTPRoute are always HTTP and
	// never read this field.
	Type string

	Interval       int64
	RetryInterval  int64
	MaxRetries     int64
	Description    string
	ResendInterval int64
	UpsideDown     bool

	Notifications []string
	Group         string
	Proxy         int64
	Tags          []string

	HTTP    HTTPOverrides
	TCP     TCPOverrides
	Ping    PingOverrides
	DNS     DNSOverrides
	Gamedig GamedigOverrides
}

type HTTPOverrides struct {
	// Host and Port are consulted only for a Service-derived HTTP monitor
	// (Ingress/HTTPRoute derive their host from routing rules instead).
	// Port is "" (auto, requires exactly one Service port), a literal port
	// number, or a Service port name.
	Host string
	Port string

	Scheme string // "" | "http" | "https"
	Path   string // "" | "/..." — must start with "/" if set

	AcceptedStatusCodes []string

	// Timeout is the request timeout in seconds.
	Timeout int64
	// MaxRedirects caps how many redirects the check follows.
	MaxRedirects int64
	// IgnoreTLS skips TLS certificate validation.
	IgnoreTLS bool
	// CacheBust appends a cache-busting query parameter to the request URL.
	CacheBust bool
	// ExpiryNotification enables TLS certificate expiry notifications.
	ExpiryNotification bool
	// DomainExpiryNotification enables domain expiry notifications.
	DomainExpiryNotification bool
	// Headers is an opaque, unvalidated passthrough to Kuma.
	Headers string
	// Body is an opaque, unvalidated passthrough to Kuma.
	Body string
}

type TCPOverrides struct {
	// Host and Port are consulted only for a Service-derived TCP monitor.
	// Port is "" (auto, requires exactly one Service port), a literal port
	// number, or a Service port name.
	Host string
	Port string

	// TLSMode selects the TLS handshake mode: "" (plain TCP), "nostarttls",
	// "secure", or "starttls".
	TLSMode string
	// ExpectedSSLAlert is the TLS alert name expected during the handshake.
	ExpectedSSLAlert string
	// ExpiryNotification enables TLS certificate expiry notifications. Only
	// honoured by Kuma when TLSMode is "secure" or "starttls".
	ExpiryNotification bool
	// DomainExpiryNotification enables domain expiry notifications.
	DomainExpiryNotification bool
}

type PingOverrides struct {
	// Host is consulted only for a Service-derived Ping monitor. There is
	// no Port — ICMP has no port concept in Kuma.
	Host string

	// Timeout is the per-ping timeout in seconds.
	Timeout int64
	// PacketSize is the ICMP packet size in bytes.
	PacketSize int64
	// DomainExpiryNotification enables domain expiry notifications.
	DomainExpiryNotification bool
}

type DNSOverrides struct {
	// Host is consulted only for a Service-derived DNS monitor.
	Host string
	// Port is the resolver's query port — a plain integer, never resolved
	// against the target Service's own exposed ports (unlike HTTP/TCP/
	// Gamedig's Port fields).
	Port int64

	ResolverServer string
	ResolveType    string

	// DomainExpiryNotification enables domain expiry notifications.
	DomainExpiryNotification bool
}

type GamedigOverrides struct {
	// Host and Port are consulted only for a Service-derived Gamedig
	// monitor. Port is "" (auto, requires exactly one Service port), a
	// literal port number, or a Service port name.
	Host string
	Port string

	// Game is required whenever Overrides.Type is "Gamedig".
	Game string
	// GivenPortOnly, when true, probes only the given port instead of
	// letting Kuma guess it.
	GivenPortOnly bool
	// DomainExpiryNotification enables domain expiry notifications.
	DomainExpiryNotification bool
}

func ParseOverrides(ann map[string]string) (Overrides, error) {
	var o Overrides
	o.Name = ann[Name]
	o.Type = ann[Type]

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
	o.Description = ann[Description]
	if o.ResendInterval, err = parseIntAnnotation(ann, ResendInterval); err != nil {
		return Overrides{}, err
	}
	if o.UpsideDown, err = parseBoolAnnotation(ann, UpsideDown); err != nil {
		return Overrides{}, err
	}
	o.Notifications = parseCommaList(ann[Notifications])
	o.Group = ann[Group]
	if o.Proxy, err = parseIntAnnotation(ann, Proxy); err != nil {
		return Overrides{}, err
	}
	o.Tags = parseCommaList(ann[Tags])

	if o.HTTP, err = parseHTTPOverrides(ann); err != nil {
		return Overrides{}, err
	}
	if o.TCP, err = parseTCPOverrides(ann); err != nil {
		return Overrides{}, err
	}
	if o.Ping, err = parsePingOverrides(ann); err != nil {
		return Overrides{}, err
	}
	if o.DNS, err = parseDNSOverrides(ann); err != nil {
		return Overrides{}, err
	}
	if o.Gamedig, err = parseGamedigOverrides(ann); err != nil {
		return Overrides{}, err
	}
	return o, nil
}

func parseHTTPOverrides(ann map[string]string) (HTTPOverrides, error) {
	var h HTTPOverrides
	h.Host = ann[HTTPHost]
	h.Port = ann[HTTPPort]
	h.Scheme = ann[HTTPScheme]
	if h.Scheme != "" && h.Scheme != "http" && h.Scheme != "https" {
		return HTTPOverrides{}, fmt.Errorf("annotations: %s must be %q or %q, got %q", HTTPScheme, "http", "https", h.Scheme)
	}
	h.Path = ann[HTTPPath]
	if h.Path != "" && !strings.HasPrefix(h.Path, "/") {
		return HTTPOverrides{}, fmt.Errorf("annotations: %s must start with \"/\", got %q", HTTPPath, h.Path)
	}
	if v := ann[HTTPAcceptedStatusCodes]; v != "" {
		for _, code := range strings.Split(v, ",") {
			h.AcceptedStatusCodes = append(h.AcceptedStatusCodes, strings.TrimSpace(code))
		}
	}

	var err error
	if h.Timeout, err = parseIntAnnotation(ann, HTTPTimeout); err != nil {
		return HTTPOverrides{}, err
	}
	if h.MaxRedirects, err = parseIntAnnotationForIntRange(ann, HTTPMaxRedirects); err != nil {
		return HTTPOverrides{}, err
	}
	if h.IgnoreTLS, err = parseBoolAnnotation(ann, HTTPIgnoreTLS); err != nil {
		return HTTPOverrides{}, err
	}
	if h.CacheBust, err = parseBoolAnnotation(ann, HTTPCacheBust); err != nil {
		return HTTPOverrides{}, err
	}
	if h.ExpiryNotification, err = parseBoolAnnotation(ann, HTTPExpiryNotification); err != nil {
		return HTTPOverrides{}, err
	}
	if h.DomainExpiryNotification, err = parseBoolAnnotation(ann, HTTPDomainExpiryNotification); err != nil {
		return HTTPOverrides{}, err
	}
	h.Headers = ann[HTTPHeaders]
	h.Body = ann[HTTPBody]
	return h, nil
}

func parseTCPOverrides(ann map[string]string) (TCPOverrides, error) {
	var t TCPOverrides
	t.Host = ann[TCPHost]
	t.Port = ann[TCPPort]
	t.TLSMode = ann[TCPTLSMode]
	if t.TLSMode != "" && t.TLSMode != "nostarttls" && t.TLSMode != "secure" && t.TLSMode != "starttls" {
		return TCPOverrides{}, fmt.Errorf("annotations: %s must be %q, %q, or %q, got %q", TCPTLSMode, "nostarttls", "secure", "starttls", t.TLSMode)
	}
	t.ExpectedSSLAlert = ann[TCPExpectedSSLAlert]

	var err error
	if t.ExpiryNotification, err = parseBoolAnnotation(ann, TCPExpiryNotification); err != nil {
		return TCPOverrides{}, err
	}
	if t.DomainExpiryNotification, err = parseBoolAnnotation(ann, TCPDomainExpiryNotification); err != nil {
		return TCPOverrides{}, err
	}
	return t, nil
}

func parsePingOverrides(ann map[string]string) (PingOverrides, error) {
	var p PingOverrides
	p.Host = ann[PingHost]

	var err error
	if p.Timeout, err = parseIntAnnotation(ann, PingTimeout); err != nil {
		return PingOverrides{}, err
	}
	if p.PacketSize, err = parseIntAnnotation(ann, PingPacketSize); err != nil {
		return PingOverrides{}, err
	}
	if p.DomainExpiryNotification, err = parseBoolAnnotation(ann, PingDomainExpiryNotification); err != nil {
		return PingOverrides{}, err
	}
	return p, nil
}

func parseDNSOverrides(ann map[string]string) (DNSOverrides, error) {
	var d DNSOverrides
	d.Host = ann[DNSHost]

	var err error
	if d.Port, err = parseIntAnnotation(ann, DNSPort); err != nil {
		return DNSOverrides{}, err
	}
	d.ResolverServer = ann[DNSResolverServer]
	d.ResolveType = ann[DNSResolveType]
	if d.DomainExpiryNotification, err = parseBoolAnnotation(ann, DNSDomainExpiryNotification); err != nil {
		return DNSOverrides{}, err
	}
	return d, nil
}

func parseGamedigOverrides(ann map[string]string) (GamedigOverrides, error) {
	var g GamedigOverrides
	g.Host = ann[GamedigHost]
	g.Port = ann[GamedigPort]
	g.Game = ann[GamedigGame]

	var err error
	if g.GivenPortOnly, err = parseBoolAnnotation(ann, GamedigGivenPortOnly); err != nil {
		return GamedigOverrides{}, err
	}
	if g.DomainExpiryNotification, err = parseBoolAnnotation(ann, GamedigDomainExpiryNotification); err != nil {
		return GamedigOverrides{}, err
	}
	return g, nil
}

// parseCommaList splits a comma-separated annotation value, trimming
// whitespace and skipping empty entries (so a trailing or doubled comma
// doesn't produce a blank entry — for tags that would create and attach a
// blank tag in Kuma, and for notifications it fails the reconcile with a
// confusing `channel "" not found` error). Returns nil for an empty/unset
// value.
func parseCommaList(v string) []string {
	if v == "" {
		return nil
	}
	var out []string
	for _, s := range strings.Split(v, ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	return out
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
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/annotations/... -v`
Expected: PASS for every test in the file.

- [ ] **Step 5: Commit**

```bash
git add internal/annotations/annotations.go internal/annotations/annotations_test.go
git commit -m "$(cat <<'EOF'
feat: rescope annotation contract to mirror MonitorSpec's per-type shape

Restructures Overrides into top-level fields plus HTTP/TCP/Ping/DNS/
Gamedig sub-structs, and renames every existing annotation into its
scoped form (e.g. uptime-kuma.io/timeout -> http.uptime-kuma.io/timeout).
Breaking change, accepted per the design spec: no prior users.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: Update `internal/derive`'s HTTP derivation for the rescoped `Overrides`

**Files:**
- Modify: `internal/derive/httpspec.go`
- Modify: `internal/derive/ingress.go`
- Modify: `internal/derive/httproute.go`
- Test: `internal/derive/ingress_test.go`
- Test: `internal/derive/httproute_test.go`

**Interfaces:**
- Consumes: `annotations.Overrides`, `annotations.HTTPOverrides` (Task 1).
- Produces: `httpMonitorSpec(name, scheme, host string, ov annotations.Overrides) kuma.MonitorSpec` — signature unchanged from before this task, so Task 4 (`derive.ServiceMonitors`) can call it exactly as `IngressMonitors`/`HTTPRouteMonitors` do, passing a `"host:port"` string as the `host` argument.

- [ ] **Step 1: Update the failing tests in `internal/derive/ingress_test.go`**

Replace `TestIngressMonitors_OverridesApplied`, `TestIngressMonitors_Phase1OverridesApplied`, and `TestIngressMonitors_PathOverride` with:

```go
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
```

Leave every other test in the file (`TestIngressMonitors_HTTPHostUsesHTTP`, `TestIngressMonitors_TLSHostUsesHTTPS`, `TestIngressMonitors_MultipleHosts`, `TestIngressMonitors_SkipsEmptyAndDuplicateHosts`, `TestIngressMonitors_NoPathOverrideDefaultsToSlash`) unchanged — they don't reference the renamed fields.

- [ ] **Step 2: Update the failing tests in `internal/derive/httproute_test.go`**

Replace `TestHTTPRouteMonitors_SchemeOverrideToHTTP`, `TestHTTPRouteMonitors_PathOverride`, and `TestHTTPRouteMonitors_Phase1OverridesApplied` with:

```go
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
```

- [ ] **Step 3: Run the tests to verify they fail to compile**

Run: `go test ./internal/derive/...`
Expected: FAIL — compile errors referencing `ov.Scheme`/`ov.Path`/`ov.Timeout` etc. not existing on `Overrides` anymore (`httpspec.go`, `ingress.go`, `httproute.go` still read the old flat fields).

- [ ] **Step 4: Update `internal/derive/httpspec.go`**

```go
package derive

import (
	"fmt"

	"uptime-kuma-operator/internal/annotations"
	"uptime-kuma-operator/internal/kuma"
)

// httpMonitorSpec builds the kuma.MonitorSpec common to both Ingress- and
// HTTPRoute-derived monitors: the common MonitorSpec fields plus a fully
// populated HTTPSpec (including the derived URL), given the resolved
// scheme and host and the resource's annotation overrides. host may
// already include a ":<port>" suffix (as Service-derived HTTP monitors
// need) — it is interpolated directly into the URL.
func httpMonitorSpec(name, scheme, host string, ov annotations.Overrides) kuma.MonitorSpec {
	path := "/"
	if ov.HTTP.Path != "" {
		path = ov.HTTP.Path
	}
	spec := kuma.MonitorSpec{
		Type:           kuma.TypeHTTP,
		Name:           name,
		Interval:       ov.Interval,
		RetryInterval:  ov.RetryInterval,
		MaxRetries:     ov.MaxRetries,
		Description:    ov.Description,
		ResendInterval: ov.ResendInterval,
		UpsideDown:     ov.UpsideDown,
		HTTP: &kuma.HTTPSpec{
			URL:                      fmt.Sprintf("%s://%s%s", scheme, host, path),
			AcceptedStatusCodes:      ov.HTTP.AcceptedStatusCodes,
			Timeout:                  ov.HTTP.Timeout,
			MaxRedirects:             int(ov.HTTP.MaxRedirects),
			IgnoreTLS:                ov.HTTP.IgnoreTLS,
			CacheBust:                ov.HTTP.CacheBust,
			ExpiryNotification:       ov.HTTP.ExpiryNotification,
			DomainExpiryNotification: ov.HTTP.DomainExpiryNotification,
			Headers:                  ov.HTTP.Headers,
			Body:                     ov.HTTP.Body,
		},
	}
	if ov.Proxy != 0 {
		spec.ProxyID = &ov.Proxy
	}
	return spec
}
```

- [ ] **Step 5: Update the scheme-override line in `internal/derive/ingress.go`**

In `IngressMonitors`, change:
```go
		if ov.Scheme != "" {
			scheme = ov.Scheme
		}
```
to:
```go
		if ov.HTTP.Scheme != "" {
			scheme = ov.HTTP.Scheme
		}
```
Also update the doc comment on line 20 (`ov.Scheme, when set, overrides that.`) to read `ov.HTTP.Scheme, when set, overrides that.`.

- [ ] **Step 6: Update the scheme-override line in `internal/derive/httproute.go`**

In `HTTPRouteMonitors`, change:
```go
	scheme := "https"
	if ov.Scheme != "" {
		scheme = ov.Scheme
	}
```
to:
```go
	scheme := "https"
	if ov.HTTP.Scheme != "" {
		scheme = ov.HTTP.Scheme
	}
```
Also update the doc comment (`ov.Scheme overrides it.`) to read `ov.HTTP.Scheme overrides it.`.

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test ./internal/derive/... -v`
Expected: PASS for every test in the package.

- [ ] **Step 8: Commit**

```bash
git add internal/derive/httpspec.go internal/derive/ingress.go internal/derive/httproute.go internal/derive/ingress_test.go internal/derive/httproute_test.go
git commit -m "$(cat <<'EOF'
refactor: read HTTP overrides from the new scoped ov.HTTP sub-struct

Follows the annotations rescoping — Ingress/HTTPRoute derivation is
otherwise unchanged, still always HTTP, still never reads ov.Type.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: Let `deriveMonitors` fail, and fix the controller package's renamed constants

**Files:**
- Modify: `internal/controller/host_reconciler.go`
- Modify: `internal/controller/ingress_controller.go`
- Modify: `internal/controller/httproute_controller.go`
- Modify: `internal/controller/ingress_controller_test.go`

**Interfaces:**
- Consumes: `annotations.Overrides` (Task 1), `derive.DesiredMonitor` (unchanged).
- Produces: `reconcileHostBasedResource`'s `deriveMonitors` parameter becomes `func(annotations.Overrides) ([]derive.DesiredMonitor, error)` — Task 5 (`ServiceReconciler`) relies on this to propagate `derive.ServiceMonitors`'s errors (ambiguous port, missing `gamedig/game`, unknown type).

This task's own tests are `internal/controller`'s existing envtest suite (`ingress_controller_test.go`, `httproute_controller_test.go`) — no new test file, since the signature change has no new externally-observable behavior for Ingress/HTTPRoute (they never error from `deriveMonitors`). The one real test-content change is `ingress_controller_test.go`'s renamed annotation constants.

- [ ] **Step 1: Update the renamed constants in `internal/controller/ingress_controller_test.go`**

In `TestIngressReconciler_SyncsPhase1Overrides`, change:
```go
			Annotations: map[string]string{
				annotations.Enabled:     "true",
				annotations.Description: "a friendly description",
				annotations.IgnoreTLS:   "true",
				annotations.Path:        "/healthz",
			},
```
to:
```go
			Annotations: map[string]string{
				annotations.Enabled:      "true",
				annotations.Description:  "a friendly description",
				annotations.HTTPIgnoreTLS: "true",
				annotations.HTTPPath:      "/healthz",
			},
```

- [ ] **Step 2: Run the controller tests to verify they fail to compile**

Run: `make test`
Expected: FAIL — compile error in `internal/controller` (`undefined: annotations.IgnoreTLS`, `undefined: annotations.Path`, and separately `cannot use func(...) []derive.DesiredMonitor as func(...) ([]derive.DesiredMonitor, error)` once you attempt Step 3's callers against the still-unchanged `host_reconciler.go` — expect to see both classes of error until Steps 3–5 are all done; that's fine, this is one compile unit).

- [ ] **Step 3: Update `internal/controller/host_reconciler.go`**

Change the `deriveMonitors` field type in the doc comment and signature:
```go
func reconcileHostBasedResource(
	ctx context.Context,
	p hostReconcilerParams,
	obj client.Object,
	kind string,
	deriveMonitors func(annotations.Overrides) []derive.DesiredMonitor,
) (ctrl.Result, error) {
```
to:
```go
func reconcileHostBasedResource(
	ctx context.Context,
	p hostReconcilerParams,
	obj client.Object,
	kind string,
	deriveMonitors func(annotations.Overrides) ([]derive.DesiredMonitor, error),
) (ctrl.Result, error) {
```
and update its doc comment's last sentence from:
```
// derive.IngressMonitors / derive.HTTPRouteMonitors, bound to obj by the
// caller's closure.
```
to:
```
// derive.IngressMonitors / derive.HTTPRouteMonitors / derive.ServiceMonitors,
// bound to obj by the caller's closure. Only the Service closure can
// actually return a non-nil error (ambiguous port, missing required
// per-type field, unknown type) — Ingress/HTTPRoute always return nil.
```

Then change the call site:
```go
	desired := deriveMonitors(ov)
```
to:
```go
	desired, err := deriveMonitors(ov)
	if err != nil {
		recordSyncFailure(p.Recorder, obj, err)
		return ctrl.Result{}, err
	}
```

- [ ] **Step 4: Update the closure in `internal/controller/ingress_controller.go`**

Change:
```go
	return reconcileHostBasedResource(ctx, r.params(), ing, "Ingress", func(ov annotations.Overrides) []derive.DesiredMonitor {
		return derive.IngressMonitors(ing, ov)
	})
```
to:
```go
	return reconcileHostBasedResource(ctx, r.params(), ing, "Ingress", func(ov annotations.Overrides) ([]derive.DesiredMonitor, error) {
		return derive.IngressMonitors(ing, ov), nil
	})
```

- [ ] **Step 5: Update the closure in `internal/controller/httproute_controller.go`**

Find the equivalent `reconcileHostBasedResource(ctx, r.params(), route, "HTTPRoute", func(ov annotations.Overrides) []derive.DesiredMonitor { return derive.HTTPRouteMonitors(route, ov) })` call and change it the same way:
```go
	return reconcileHostBasedResource(ctx, r.params(), route, "HTTPRoute", func(ov annotations.Overrides) ([]derive.DesiredMonitor, error) {
		return derive.HTTPRouteMonitors(route, ov), nil
	})
```

- [ ] **Step 6: Run the controller tests to verify they pass**

Run: `make test`
Expected: PASS for every test in `internal/controller` (and every other package — `make test` runs `go test ./...`).

- [ ] **Step 7: Commit**

```bash
git add internal/controller/host_reconciler.go internal/controller/ingress_controller.go internal/controller/httproute_controller.go internal/controller/ingress_controller_test.go
git commit -m "$(cat <<'EOF'
refactor: let deriveMonitors fail, for the upcoming Service reconciler

Ingress/HTTPRoute's closures always return a nil error — only
derive.ServiceMonitors (added next) can fail, on an ambiguous port,
a missing required per-type field, or an unrecognized type.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: `derive.ServiceMonitors`

**Files:**
- Create: `internal/derive/service.go`
- Test: `internal/derive/service_test.go`

**Interfaces:**
- Consumes: `annotations.Overrides` and its sub-structs (Task 1); `derive.DesiredMonitor{Host string; Spec kuma.MonitorSpec}` (existing, from `internal/derive/ingress.go`).
- Produces: `func ServiceMonitors(svc *corev1.Service, ov annotations.Overrides) ([]DesiredMonitor, error)`, consumed by Task 5 (`ServiceReconciler`).

- [ ] **Step 1: Write `internal/derive/service_test.go`**

```go
package derive

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"uptime-kuma-operator/internal/annotations"
	"uptime-kuma-operator/internal/kuma"
)

func svcWithPorts(ports ...corev1.ServicePort) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "web"},
		Spec:       corev1.ServiceSpec{Ports: ports},
	}
}

func TestServiceMonitors_DefaultsToHTTP(t *testing.T) {
	svc := svcWithPorts(corev1.ServicePort{Port: 8080})

	got, err := ServiceMonitors(svc, annotations.Overrides{})
	if err != nil {
		t.Fatalf("ServiceMonitors: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].Spec.Type != kuma.TypeHTTP {
		t.Errorf("Type = %q, want %q", got[0].Spec.Type, kuma.TypeHTTP)
	}
	wantURL := "http://web.prod.svc.cluster.local:8080/"
	if got[0].Spec.HTTP == nil || got[0].Spec.HTTP.URL != wantURL {
		t.Errorf("URL = %v, want %q", got[0].Spec.HTTP, wantURL)
	}
	if got[0].Spec.Name != "prod/web" {
		t.Errorf("Name = %q, want %q", got[0].Spec.Name, "prod/web")
	}
}

func TestServiceMonitors_HTTPHostAndSchemeOverride(t *testing.T) {
	svc := svcWithPorts(corev1.ServicePort{Port: 8080})
	ov := annotations.Overrides{HTTP: annotations.HTTPOverrides{Host: "custom.example.com", Scheme: "https", Path: "/healthz"}}

	got, err := ServiceMonitors(svc, ov)
	if err != nil {
		t.Fatalf("ServiceMonitors: %v", err)
	}
	want := "https://custom.example.com:8080/healthz"
	if got[0].Spec.HTTP.URL != want {
		t.Errorf("URL = %q, want %q", got[0].Spec.HTTP.URL, want)
	}
}

func TestServiceMonitors_SinglePortAutoPicked(t *testing.T) {
	svc := svcWithPorts(corev1.ServicePort{Port: 9090})

	got, err := ServiceMonitors(svc, annotations.Overrides{Type: "TCP"})
	if err != nil {
		t.Fatalf("ServiceMonitors: %v", err)
	}
	if got[0].Spec.TCP == nil || got[0].Spec.TCP.Port != 9090 {
		t.Errorf("TCP = %v, want Port 9090", got[0].Spec.TCP)
	}
	if got[0].Spec.TCP.Host != "web.prod.svc.cluster.local" {
		t.Errorf("Host = %q, want default DNS name", got[0].Spec.TCP.Host)
	}
}

func TestServiceMonitors_MultiplePortsRequiresAnnotation(t *testing.T) {
	svc := svcWithPorts(corev1.ServicePort{Port: 80}, corev1.ServicePort{Port: 443})
	if _, err := ServiceMonitors(svc, annotations.Overrides{Type: "TCP"}); err == nil {
		t.Fatal("expected error for ambiguous port, got nil")
	}
}

func TestServiceMonitors_PortByName(t *testing.T) {
	svc := svcWithPorts(
		corev1.ServicePort{Name: "http", Port: 80},
		corev1.ServicePort{Name: "metrics", Port: 9100},
	)
	ov := annotations.Overrides{Type: "TCP", TCP: annotations.TCPOverrides{Port: "metrics"}}

	got, err := ServiceMonitors(svc, ov)
	if err != nil {
		t.Fatalf("ServiceMonitors: %v", err)
	}
	if got[0].Spec.TCP.Port != 9100 {
		t.Errorf("Port = %d, want 9100", got[0].Spec.TCP.Port)
	}
}

func TestServiceMonitors_PortByNumber(t *testing.T) {
	svc := svcWithPorts(
		corev1.ServicePort{Name: "http", Port: 80},
		corev1.ServicePort{Name: "metrics", Port: 9100},
	)
	ov := annotations.Overrides{Type: "TCP", TCP: annotations.TCPOverrides{Port: "80"}}

	got, err := ServiceMonitors(svc, ov)
	if err != nil {
		t.Fatalf("ServiceMonitors: %v", err)
	}
	if got[0].Spec.TCP.Port != 80 {
		t.Errorf("Port = %d, want 80", got[0].Spec.TCP.Port)
	}
}

func TestServiceMonitors_UnresolvablePortOverride(t *testing.T) {
	svc := svcWithPorts(corev1.ServicePort{Name: "http", Port: 80})
	ov := annotations.Overrides{Type: "TCP", TCP: annotations.TCPOverrides{Port: "does-not-exist"}}
	if _, err := ServiceMonitors(svc, ov); err == nil {
		t.Fatal("expected error for unresolvable port override, got nil")
	}
}

func TestServiceMonitors_ServiceWithNoPortsErrorsForHTTP(t *testing.T) {
	svc := svcWithPorts()
	if _, err := ServiceMonitors(svc, annotations.Overrides{}); err == nil {
		t.Fatal("expected error for a Service with no ports, got nil")
	}
}

func TestServiceMonitors_Ping(t *testing.T) {
	// Multiple ports on the Service must not matter: Ping never needs one.
	svc := svcWithPorts(corev1.ServicePort{Port: 80}, corev1.ServicePort{Port: 443})
	ov := annotations.Overrides{Type: "Ping", Ping: annotations.PingOverrides{Timeout: 5, PacketSize: 56}}

	got, err := ServiceMonitors(svc, ov)
	if err != nil {
		t.Fatalf("ServiceMonitors: %v (Ping must not require a port)", err)
	}
	if got[0].Spec.Ping == nil || got[0].Spec.Ping.Host != "web.prod.svc.cluster.local" {
		t.Errorf("Ping.Host = %v, want default DNS name", got[0].Spec.Ping)
	}
	if got[0].Spec.Ping.Timeout != 5 || got[0].Spec.Ping.PacketSize != 56 {
		t.Errorf("Ping spec = %+v, want Timeout=5 PacketSize=56", got[0].Spec.Ping)
	}
}

func TestServiceMonitors_DNSPortIsNotResolvedAgainstServicePorts(t *testing.T) {
	// Multiple ports on the Service must not matter: DNS's port annotation
	// is the resolver's query port, unrelated to the Service's own ports.
	svc := svcWithPorts(corev1.ServicePort{Port: 80}, corev1.ServicePort{Port: 443})
	ov := annotations.Overrides{Type: "DNS", DNS: annotations.DNSOverrides{
		ResolverServer: "1.1.1.1", ResolveType: "A", Port: 5353,
	}}

	got, err := ServiceMonitors(svc, ov)
	if err != nil {
		t.Fatalf("ServiceMonitors: %v (DNS port must not require Service-port disambiguation)", err)
	}
	if got[0].Spec.DNS == nil || got[0].Spec.DNS.Port != 5353 {
		t.Errorf("DNS.Port = %v, want 5353 (the resolver port, not a Service port)", got[0].Spec.DNS)
	}
	if got[0].Spec.DNS.ResolverServer != "1.1.1.1" || got[0].Spec.DNS.ResolveType != "A" {
		t.Errorf("DNS spec = %+v, want ResolverServer=1.1.1.1 ResolveType=A", got[0].Spec.DNS)
	}
}

func TestServiceMonitors_GamedigRequiresGame(t *testing.T) {
	svc := svcWithPorts(corev1.ServicePort{Port: 27015})
	if _, err := ServiceMonitors(svc, annotations.Overrides{Type: "Gamedig"}); err == nil {
		t.Fatal("expected error for missing gamedig/game, got nil")
	}
}

func TestServiceMonitors_Gamedig(t *testing.T) {
	svc := svcWithPorts(corev1.ServicePort{Port: 27015})
	ov := annotations.Overrides{Type: "Gamedig", Gamedig: annotations.GamedigOverrides{Game: "csgo", GivenPortOnly: true}}

	got, err := ServiceMonitors(svc, ov)
	if err != nil {
		t.Fatalf("ServiceMonitors: %v", err)
	}
	if got[0].Spec.Gamedig == nil || got[0].Spec.Gamedig.Game != "csgo" ||
		got[0].Spec.Gamedig.Port != 27015 || !got[0].Spec.Gamedig.GivenPortOnly {
		t.Errorf("Gamedig spec = %+v, want Game=csgo Port=27015 GivenPortOnly=true", got[0].Spec.Gamedig)
	}
}

func TestServiceMonitors_UnknownTypeErrors(t *testing.T) {
	svc := svcWithPorts(corev1.ServicePort{Port: 80})
	if _, err := ServiceMonitors(svc, annotations.Overrides{Type: "Bogus"}); err == nil {
		t.Fatal("expected error for unknown type, got nil")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/derive/... -run TestServiceMonitors -v`
Expected: FAIL with `undefined: ServiceMonitors` (compile error).

- [ ] **Step 3: Write `internal/derive/service.go`**

```go
package derive

import (
	"fmt"
	"strconv"

	corev1 "k8s.io/api/core/v1"

	"uptime-kuma-operator/internal/annotations"
	"uptime-kuma-operator/internal/kuma"
)

// ServiceMonitors derives the single DesiredMonitor for svc, of the Kuma
// type ov.Type selects (defaulting to HTTP). Unlike Ingress/HTTPRoute,
// which derive one monitor per routing-rule host, a Service carries no
// routing information of its own — its check target is either the
// in-cluster DNS name or an explicit <type>/host override, and (for
// HTTP/TCP/Gamedig) a Service port resolved via resolveServicePort.
func ServiceMonitors(svc *corev1.Service, ov annotations.Overrides) ([]DesiredMonitor, error) {
	monType := ov.Type
	if monType == "" {
		monType = "HTTP"
	}

	name := ov.Name
	if name == "" {
		name = fmt.Sprintf("%s/%s", svc.Namespace, svc.Name)
	}

	spec := kuma.MonitorSpec{
		Name:           name,
		Interval:       ov.Interval,
		RetryInterval:  ov.RetryInterval,
		MaxRetries:     ov.MaxRetries,
		Description:    ov.Description,
		ResendInterval: ov.ResendInterval,
		UpsideDown:     ov.UpsideDown,
	}
	if ov.Proxy != 0 {
		spec.ProxyID = &ov.Proxy
	}

	var host string
	switch monType {
	case "HTTP":
		spec.Type = kuma.TypeHTTP
		host = defaultHost(svc, ov.HTTP.Host)
		port, err := resolveServicePort(svc, ov.HTTP.Port)
		if err != nil {
			return nil, err
		}
		scheme := "http"
		if ov.HTTP.Scheme != "" {
			scheme = ov.HTTP.Scheme
		}
		path := "/"
		if ov.HTTP.Path != "" {
			path = ov.HTTP.Path
		}
		spec.HTTP = &kuma.HTTPSpec{
			URL:                      fmt.Sprintf("%s://%s:%d%s", scheme, host, port, path),
			AcceptedStatusCodes:      ov.HTTP.AcceptedStatusCodes,
			Timeout:                  ov.HTTP.Timeout,
			MaxRedirects:             int(ov.HTTP.MaxRedirects),
			IgnoreTLS:                ov.HTTP.IgnoreTLS,
			CacheBust:                ov.HTTP.CacheBust,
			ExpiryNotification:       ov.HTTP.ExpiryNotification,
			DomainExpiryNotification: ov.HTTP.DomainExpiryNotification,
			Headers:                  ov.HTTP.Headers,
			Body:                     ov.HTTP.Body,
		}

	case "TCP":
		spec.Type = kuma.TypeTCP
		host = defaultHost(svc, ov.TCP.Host)
		port, err := resolveServicePort(svc, ov.TCP.Port)
		if err != nil {
			return nil, err
		}
		spec.TCP = &kuma.TCPSpec{
			Host: host, Port: int(port),
			TLSMode:                  ov.TCP.TLSMode,
			ExpectedSSLAlert:         ov.TCP.ExpectedSSLAlert,
			ExpiryNotification:       ov.TCP.ExpiryNotification,
			DomainExpiryNotification: ov.TCP.DomainExpiryNotification,
		}

	case "Ping":
		spec.Type = kuma.TypePing
		host = defaultHost(svc, ov.Ping.Host)
		spec.Ping = &kuma.PingSpec{
			Host: host, Timeout: ov.Ping.Timeout, PacketSize: int(ov.Ping.PacketSize),
			DomainExpiryNotification: ov.Ping.DomainExpiryNotification,
		}

	case "DNS":
		spec.Type = kuma.TypeDNS
		host = defaultHost(svc, ov.DNS.Host)
		spec.DNS = &kuma.DNSSpec{
			Host: host, ResolverServer: ov.DNS.ResolverServer, ResolveType: ov.DNS.ResolveType,
			Port:                     int(ov.DNS.Port),
			DomainExpiryNotification: ov.DNS.DomainExpiryNotification,
		}

	case "Gamedig":
		if ov.Gamedig.Game == "" {
			return nil, fmt.Errorf("service: %s is required when type is Gamedig", annotations.GamedigGame)
		}
		spec.Type = kuma.TypeGamedig
		host = defaultHost(svc, ov.Gamedig.Host)
		port, err := resolveServicePort(svc, ov.Gamedig.Port)
		if err != nil {
			return nil, err
		}
		spec.Gamedig = &kuma.GamedigSpec{
			Host: host, Port: int(port), Game: ov.Gamedig.Game,
			GivenPortOnly:            ov.Gamedig.GivenPortOnly,
			DomainExpiryNotification: ov.Gamedig.DomainExpiryNotification,
		}

	default:
		return nil, fmt.Errorf("service: unknown %s %q", annotations.Type, monType)
	}

	return []DesiredMonitor{{Host: host, Spec: spec}}, nil
}

// defaultHost returns override if set, else the Service's in-cluster DNS name.
func defaultHost(svc *corev1.Service, override string) string {
	if override != "" {
		return override
	}
	return fmt.Sprintf("%s.%s.svc.cluster.local", svc.Name, svc.Namespace)
}

// resolveServicePort picks which of svc's ports to check. An empty override
// auto-picks the Service's only port and errors if there is none or more
// than one; a non-empty override is matched first as a literal port
// number, then against a port's .Name.
func resolveServicePort(svc *corev1.Service, override string) (int32, error) {
	if override == "" {
		switch len(svc.Spec.Ports) {
		case 0:
			return 0, fmt.Errorf("service: %s/%s has no ports", svc.Namespace, svc.Name)
		case 1:
			return svc.Spec.Ports[0].Port, nil
		default:
			return 0, fmt.Errorf("service: %s/%s exposes %d ports, a port annotation is required to pick one",
				svc.Namespace, svc.Name, len(svc.Spec.Ports))
		}
	}

	if n, err := strconv.ParseInt(override, 10, 32); err == nil {
		for _, p := range svc.Spec.Ports {
			if int64(p.Port) == n {
				return p.Port, nil
			}
		}
		return 0, fmt.Errorf("service: %s/%s has no port %d", svc.Namespace, svc.Name, n)
	}

	for _, p := range svc.Spec.Ports {
		if p.Name == override {
			return p.Port, nil
		}
	}
	return 0, fmt.Errorf("service: %s/%s has no port named %q", svc.Namespace, svc.Name, override)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/derive/... -v`
Expected: PASS for every test in the package, including the new `TestServiceMonitors_*` cases.

- [ ] **Step 5: Commit**

```bash
git add internal/derive/service.go internal/derive/service_test.go
git commit -m "$(cat <<'EOF'
feat: add derive.ServiceMonitors for any-type Service-derived monitors

Always yields exactly one DesiredMonitor per Service, of whichever
Kuma type ov.Type selects (default HTTP). Host defaults to the
in-cluster DNS name; HTTP/TCP/Gamedig additionally resolve a Service
port (auto-picked when there's exactly one, otherwise required via
annotation); DNS's port is the resolver's own port, never resolved
against the Service's ports.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: `ServiceReconciler`

**Files:**
- Create: `internal/controller/service_controller.go`
- Test: `internal/controller/service_controller_test.go`

**Interfaces:**
- Consumes: `hostReconcilerParams`, `reconcileHostBasedResource` (Task 3); `derive.ServiceMonitors` (Task 4).
- Produces: `type ServiceReconciler struct { Client client.Client; Kuma kuma.Client; OptInByDefault bool; Recorder record.EventRecorder; DriftCheckInterval time.Duration; DefaultTags []string; LabelTagPatterns []*regexp.Regexp }` with `Reconcile` and `SetupWithManager` methods — consumed by Task 6 (`cmd/main.go`).

- [ ] **Step 1: Write `internal/controller/service_controller_test.go`**

```go
package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"

	"uptime-kuma-operator/internal/annotations"
	"uptime-kuma-operator/internal/config"
	"uptime-kuma-operator/internal/kuma"
)

func newServiceReconciler(optInByDefault bool) (*ServiceReconciler, *kuma.FakeClient) {
	fake := kuma.NewFakeClient()
	return &ServiceReconciler{
		Client:             k8sClient,
		Kuma:               fake,
		OptInByDefault:     optInByDefault,
		Recorder:           record.NewFakeRecorder(16),
		DriftCheckInterval: config.DefaultDriftCheckInterval,
	}, fake
}

func TestServiceReconciler_DefaultMode_SkipsWithoutAnnotation(t *testing.T) {
	ctx := context.Background()
	r, fake := newServiceReconciler(false)

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 8080}}},
	}
	if err := k8sClient.Create(ctx, svc); err != nil {
		t.Fatalf("create Service: %v", err)
	}
	defer k8sClient.Delete(ctx, svc)

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: svc.Name, Namespace: svc.Namespace}}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(fake.Monitors) != 0 {
		t.Errorf("expected no monitors created without opt-in annotation, got %d", len(fake.Monitors))
	}
}

func TestServiceReconciler_DefaultsToHTTPMonitor(t *testing.T) {
	ctx := context.Background()
	r, fake := newServiceReconciler(false)

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web", Namespace: "default",
			Annotations: map[string]string{annotations.Enabled: "true"},
		},
		Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 8080}}},
	}
	if err := k8sClient.Create(ctx, svc); err != nil {
		t.Fatalf("create Service: %v", err)
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: svc.Name, Namespace: svc.Namespace}}
	defer func() {
		_ = k8sClient.Delete(ctx, svc)
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
	if spec.Type != kuma.TypeHTTP {
		t.Errorf("Type = %q, want %q (default)", spec.Type, kuma.TypeHTTP)
	}
	wantURL := "http://web.default.svc.cluster.local:8080/"
	if spec.HTTP == nil || spec.HTTP.URL != wantURL {
		t.Errorf("URL = %v, want %q", spec.HTTP, wantURL)
	}
}

func TestServiceReconciler_TypeAnnotationSelectsTCPMonitor(t *testing.T) {
	ctx := context.Background()
	r, fake := newServiceReconciler(false)

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: "redis", Namespace: "default",
			Annotations: map[string]string{
				annotations.Enabled: "true",
				annotations.Type:    "TCP",
			},
		},
		Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 6379}}},
	}
	if err := k8sClient.Create(ctx, svc); err != nil {
		t.Fatalf("create Service: %v", err)
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: svc.Name, Namespace: svc.Namespace}}
	defer func() {
		_ = k8sClient.Delete(ctx, svc)
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
	if spec.Type != kuma.TypeTCP {
		t.Errorf("Type = %q, want %q", spec.Type, kuma.TypeTCP)
	}
	if spec.TCP == nil || spec.TCP.Host != "redis.default.svc.cluster.local" || spec.TCP.Port != 6379 {
		t.Errorf("TCP spec = %+v, want Host=redis.default.svc.cluster.local Port=6379", spec.TCP)
	}
}

func TestServiceReconciler_AmbiguousPortFailsReconcileWithoutCreatingAMonitor(t *testing.T) {
	ctx := context.Background()
	r, fake := newServiceReconciler(false)

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: "multi-port", Namespace: "default",
			Annotations: map[string]string{
				annotations.Enabled: "true",
				annotations.Type:    "TCP",
			},
		},
		Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Name: "http", Port: 80}, {Name: "https", Port: 443}}},
	}
	if err := k8sClient.Create(ctx, svc); err != nil {
		t.Fatalf("create Service: %v", err)
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: svc.Name, Namespace: svc.Namespace}}
	defer func() {
		_ = k8sClient.Delete(ctx, svc)
		_, _ = r.Reconcile(ctx, req)
	}()

	if _, err := r.Reconcile(ctx, req); err == nil {
		t.Fatal("expected Reconcile to fail for an ambiguous port, got nil")
	}
	if len(fake.Monitors) != 0 {
		t.Errorf("expected no monitor created when port resolution fails, got %d", len(fake.Monitors))
	}
}

func TestServiceReconciler_DeletionRemovesMonitorAndFinalizer(t *testing.T) {
	ctx := context.Background()
	r, fake := newServiceReconciler(false)

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web", Namespace: "default",
			Annotations: map[string]string{annotations.Enabled: "true"},
		},
		Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 8080}}},
	}
	if err := k8sClient.Create(ctx, svc); err != nil {
		t.Fatalf("create Service: %v", err)
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: svc.Name, Namespace: svc.Namespace}}

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("first Reconcile (create): %v", err)
	}
	if err := k8sClient.Delete(ctx, svc); err != nil {
		t.Fatalf("delete Service: %v", err)
	}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("second Reconcile (finalize): %v", err)
	}
	if len(fake.Monitors) != 0 {
		t.Errorf("expected monitor deleted after Service deletion, got %d remaining", len(fake.Monitors))
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `make test`
Expected: FAIL with `undefined: ServiceReconciler` (compile error in `internal/controller`).

- [ ] **Step 3: Write `internal/controller/service_controller.go`**

```go
package controller

import (
	"context"
	"regexp"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"uptime-kuma-operator/internal/annotations"
	"uptime-kuma-operator/internal/derive"
	"uptime-kuma-operator/internal/kuma"
)

type ServiceReconciler struct {
	Client client.Client
	Kuma   kuma.Client
	// OptInByDefault controls the annotation policy only — it is independent
	// of which namespaces the manager watches (config.Config.WatchAll).
	OptInByDefault bool
	Recorder       record.EventRecorder
	// DriftCheckInterval is how often a reconcile that found nothing to sync
	// re-checks that Kuma still has what it's supposed to.
	DriftCheckInterval time.Duration
	// DefaultTags lists tag names applied to every monitor this reconciler
	// manages, in addition to whatever the Service's own tags annotation
	// specifies. See sync.go's mergeTags doc comment.
	DefaultTags []string
	// LabelTagPatterns, when non-empty, turns on Phase 3's operator-wide
	// label-tag inference for every monitor this reconciler manages. See
	// sync.go's deriveLabelTags doc comment.
	LabelTagPatterns []*regexp.Regexp
}

func (r *ServiceReconciler) params() hostReconcilerParams {
	return hostReconcilerParams{
		Client:             r.Client,
		Kuma:               r.Kuma,
		OptInByDefault:     r.OptInByDefault,
		Recorder:           r.Recorder,
		DriftCheckInterval: r.DriftCheckInterval,
		DefaultTags:        r.DefaultTags,
		LabelTagPatterns:   r.LabelTagPatterns,
	}
}

func (r *ServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	svc := &corev1.Service{}
	if err := r.Client.Get(ctx, req.NamespacedName, svc); err != nil {
		if apierrors.IsNotFound(err) {
			log.V(1).Info("Service not found, assuming it was deleted")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	log.V(1).Info("reconcile triggered", "resourceVersion", svc.ResourceVersion, "generation", svc.Generation)

	return reconcileHostBasedResource(ctx, r.params(), svc, "Service", func(ov annotations.Overrides) ([]derive.DesiredMonitor, error) {
		return derive.ServiceMonitors(svc, ov)
	})
}

func (r *ServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Service{}).
		Complete(r)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `make test`
Expected: PASS for every test in `internal/controller`, including the new `TestServiceReconciler_*` cases.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/service_controller.go internal/controller/service_controller_test.go
git commit -m "$(cat <<'EOF'
feat: add ServiceReconciler for annotation-driven Service monitoring

Structurally identical to IngressReconciler/HTTPRouteReconciler —
shares reconcileHostBasedResource — fed by derive.ServiceMonitors so a
Service can produce a monitor of any Kuma type via uptime-kuma.io/type.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: Wire `ServiceReconciler` into `cmd/main.go`

**Files:**
- Modify: `cmd/main.go`

**Interfaces:**
- Consumes: `controller.ServiceReconciler` (Task 5), `config.Config` (unchanged — reuses `OptInByDefault`, `DriftCheckInterval`, `DefaultTags`, `LabelTagPatterns`, exactly as `IngressReconciler`'s construction does).

- [ ] **Step 1: Add `ServiceReconciler` registration in `cmd/main.go`**

Immediately after the existing `IngressReconciler` registration block:
```go
	if err := (&controller.IngressReconciler{
		Client: mgr.GetClient(), Kuma: kumaClient, OptInByDefault: cfg.OptInByDefault,
		Recorder: recorder, DriftCheckInterval: cfg.DriftCheckInterval, DefaultTags: cfg.DefaultTags,
		LabelTagPatterns: cfg.LabelTagPatterns,
	}).SetupWithManager(mgr); err != nil {
		log.Error(err, "unable to create Ingress controller")
		os.Exit(1)
	}
```
add:
```go
	if err := (&controller.ServiceReconciler{
		Client: mgr.GetClient(), Kuma: kumaClient, OptInByDefault: cfg.OptInByDefault,
		Recorder: recorder, DriftCheckInterval: cfg.DriftCheckInterval, DefaultTags: cfg.DefaultTags,
		LabelTagPatterns: cfg.LabelTagPatterns,
	}).SetupWithManager(mgr); err != nil {
		log.Error(err, "unable to create Service controller")
		os.Exit(1)
	}
```
No CRD-presence gate is needed (unlike the `HTTPRouteReconciler` block below it) — `corev1.Service` is core API, always present, and `corev1` is already imported in this file (used for the `client.CacheOptions` `DisableFor` list).

- [ ] **Step 2: Build and run the full test suite**

Run: `make test`
Expected: PASS for every package, including `cmd`'s `TestBuild` smoke test.

- [ ] **Step 3: Commit**

```bash
git add cmd/main.go
git commit -m "$(cat <<'EOF'
feat: register the Service controller in the manager

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: RBAC for `Service`

**Files:**
- Modify: `charts/uptime-kuma-operator/templates/rbac.yaml`

**Interfaces:** none (Helm template only).

- [ ] **Step 1: Add the `services` rule**

In the `$rules` list at the top of the file, add a new entry (placed alongside the other watched-resource rules, before the `events`/`secrets` rules that already close the list):
```yaml
{{- $rules := list
  (dict "apiGroups" (list "networking.k8s.io") "resources" (list "ingresses") "verbs" (list "get" "list" "watch" "update" "patch"))
  (dict "apiGroups" (list "gateway.networking.k8s.io") "resources" (list "httproutes") "verbs" (list "get" "list" "watch" "update" "patch"))
  (dict "apiGroups" (list "") "resources" (list "services") "verbs" (list "get" "list" "watch" "update" "patch"))
  (dict "apiGroups" (list "uptime-kuma.io") "resources" (list "monitors") "verbs" (list "get" "list" "watch" "update" "patch"))
  (dict "apiGroups" (list "uptime-kuma.io") "resources" (list "monitors/status") "verbs" (list "get" "update" "patch"))
  (dict "apiGroups" (list "uptime-kuma.io") "resources" (list "monitors/finalizers") "verbs" (list "update"))
  (dict "apiGroups" (list "") "resources" (list "events") "verbs" (list "create" "patch"))
  (dict "apiGroups" (list "") "resources" (list "secrets") "verbs" (list "get"))
-}}
```
(Only the new `services` line is added; every other line is unchanged.)

- [ ] **Step 2: Run the RBAC chart test**

Run: `./charts/uptime-kuma-operator/templates/tests/rbac_test.sh`
Expected: `OK` printed at the end, no `FAIL` lines — this script checks Role/ClusterRole rendering shape, not the specific resource list, so it should pass unchanged and confirms the template still renders correctly.

Also run: `helm lint charts/uptime-kuma-operator --set kuma.url=https://kuma.example.com --set kuma.existingSecret=kuma-credentials`
Expected: `0 chart(s) linted, 0 chart(s) failed`.

- [ ] **Step 3: Commit**

```bash
git add charts/uptime-kuma-operator/templates/rbac.yaml
git commit -m "$(cat <<'EOF'
chore: grant the operator RBAC on Services

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## Task 8: Documentation

**Files:**
- Modify: `README.md`
- Modify: `docs/README.md`

**Interfaces:** none (documentation only).

- [ ] **Step 1: Update `docs/README.md`'s annotation table**

Replace the existing flat annotation table (currently starting `| uptime-kuma.io/enabled | ... |` through the `proxy` row, i.e. covering lines in the neighborhood of what's shown below) with the rescoped version. Find the table that currently reads (abbreviated):
```
| `uptime-kuma.io/enabled` | `true`/`false` | ... |
| `uptime-kuma.io/name` | string | ... |
| `uptime-kuma.io/scheme` | `http`/`https` | ... |
| `uptime-kuma.io/path` | string, starts with `/` | ... |
...
| `uptime-kuma.io/proxy` | integer | ... |
```
and replace it with three tables — top-level, then one per type — reflecting Task 1's constants:

```markdown
### Top-level annotations

These apply to every resource kind (`Ingress`, `HTTPRoute`, `Service`)
regardless of monitor type.

| Annotation | Value | Meaning |
|---|---|---|
| `uptime-kuma.io/enabled` | `true`/`false` | Opts the resource in/out — see [above](#choosing-what-gets-watched) |
| `uptime-kuma.io/name` | string | Monitor name; defaults to `<namespace>/<name>/<host>` (Ingress/HTTPRoute) or `<namespace>/<name>` (Service) |
| `uptime-kuma.io/type` | `HTTP`/`TCP`/`Ping`/`DNS`/`Gamedig` | **Service only** — selects the monitor type; defaults to `HTTP`. Ingress/HTTPRoute never read this — they are always HTTP. |
| `uptime-kuma.io/interval` | integer (seconds) | Check interval |
| `uptime-kuma.io/retry-interval` | integer (seconds) | Interval between retries |
| `uptime-kuma.io/max-retries` | integer | Retries before a monitor is marked down |
| `uptime-kuma.io/description` | string | Shown alongside the monitor in the Kuma UI |
| `uptime-kuma.io/resend-interval` | integer | Failed checks between repeated down notifications; `0` disables resending |
| `uptime-kuma.io/upside-down` | `true`/`false` | Inverts up/down |
| `uptime-kuma.io/tags` | comma-separated list | See [Tags, notifications, groups, and proxies](#tags-notifications-groups-and-proxies) |
| `uptime-kuma.io/notifications` | comma-separated list | Notification channel names; each must already exist in Kuma |
| `uptime-kuma.io/group` | string | Parent group monitor name; must already exist in Kuma |
| `uptime-kuma.io/proxy` | integer | Kuma proxy ID (proxies have no name in Kuma, only an ID) |

### `http/` annotations

Used by Ingress and HTTPRoute always, and by Service when `type` is
`HTTP` (the default). `host`/`port` are Service-only — Ingress/HTTPRoute
derive their host from routing rules instead.

| Annotation | Value | Meaning |
|---|---|---|
| `http.uptime-kuma.io/host` | string | **Service only.** Overrides the default `<name>.<namespace>.svc.cluster.local` target |
| `http.uptime-kuma.io/port` | integer or Service port name | **Service only.** Required if the Service exposes more than one port; auto-picked if it exposes exactly one |
| `http.uptime-kuma.io/scheme` | `http`/`https` | Overrides the derived scheme (Ingress: from `spec.tls`; HTTPRoute/Service: `https`/`http` default) |
| `http.uptime-kuma.io/path` | string, starts with `/` | Request path; defaults to `/` |
| `http.uptime-kuma.io/accepted-statuscodes` | comma-separated list | e.g. `200-299,301` |
| `http.uptime-kuma.io/timeout` | integer (seconds) | Request timeout |
| `http.uptime-kuma.io/max-redirects` | integer | Redirects to follow |
| `http.uptime-kuma.io/ignore-tls` | `true`/`false` | Skip TLS certificate validation |
| `http.uptime-kuma.io/cache-bust` | `true`/`false` | Append a cache-busting query parameter |
| `http.uptime-kuma.io/expiry-notification` | `true`/`false` | TLS certificate expiry notifications |
| `http.uptime-kuma.io/domain-expiry-notification` | `true`/`false` | Domain expiry notifications |
| `http.uptime-kuma.io/headers` | string | Opaque passthrough to Kuma (raw JSON, as Kuma's UI expects) |
| `http.uptime-kuma.io/body` | string | Opaque passthrough to Kuma |

### `tcp/` annotations (Service, `type: TCP`)

| Annotation | Value | Meaning |
|---|---|---|
| `tcp.uptime-kuma.io/host` | string | Overrides the default `<name>.<namespace>.svc.cluster.local` target |
| `tcp.uptime-kuma.io/port` | integer or Service port name | Required if the Service exposes more than one port; auto-picked if it exposes exactly one |
| `tcp.uptime-kuma.io/tls-mode` | `nostarttls`/`secure`/`starttls` | TLS handshake mode; empty means plain TCP |
| `tcp.uptime-kuma.io/expected-ssl-alert` | string | Expected TLS alert name during the handshake |
| `tcp.uptime-kuma.io/expiry-notification` | `true`/`false` | TLS certificate expiry notifications (only honoured when `tls-mode` is `secure` or `starttls`) |
| `tcp.uptime-kuma.io/domain-expiry-notification` | `true`/`false` | Domain expiry notifications |

### `ping/` annotations (Service, `type: Ping`)

| Annotation | Value | Meaning |
|---|---|---|
| `ping.uptime-kuma.io/host` | string | Overrides the default `<name>.<namespace>.svc.cluster.local` target |
| `ping.uptime-kuma.io/timeout` | integer (seconds) | Per-ping timeout |
| `ping.uptime-kuma.io/packet-size` | integer (bytes) | ICMP packet size |
| `ping.uptime-kuma.io/domain-expiry-notification` | `true`/`false` | Domain expiry notifications |

There is no `ping/port` annotation — ICMP has no port concept in Kuma.

### `dns/` annotations (Service, `type: DNS`)

| Annotation | Value | Meaning |
|---|---|---|
| `dns.uptime-kuma.io/host` | string | Overrides the default `<name>.<namespace>.svc.cluster.local` target — the domain name to resolve |
| `dns.uptime-kuma.io/port` | integer | The resolver's query port. **Not resolved against the Service's own ports** — unlike `http`/`tcp`/`gamedig`'s `port`, this is a plain integer matching `DNSMonitorSpec.Port`'s own semantics |
| `dns.uptime-kuma.io/resolver-server` | string | DNS resolver server address |
| `dns.uptime-kuma.io/resolve-type` | string | Record type to resolve (e.g. `A`) |
| `dns.uptime-kuma.io/domain-expiry-notification` | `true`/`false` | Domain expiry notifications |

### `gamedig/` annotations (Service, `type: Gamedig`)

| Annotation | Value | Meaning |
|---|---|---|
| `gamedig.uptime-kuma.io/host` | string | Overrides the default `<name>.<namespace>.svc.cluster.local` target |
| `gamedig.uptime-kuma.io/port` | integer or Service port name | Required if the Service exposes more than one port; auto-picked if it exposes exactly one |
| `gamedig.uptime-kuma.io/game` | string | **Required.** Gamedig game ID (Kuma's `game` field) |
| `gamedig.uptime-kuma.io/given-port-only` | `true`/`false` | Probe only the given port instead of letting Kuma guess it |
| `gamedig.uptime-kuma.io/domain-expiry-notification` | `true`/`false` | Domain expiry notifications |

Secret-backed auth (HTTP Basic/Bearer/OAuth2, the Gamedig token) is not
available via annotation on any resource — use the `Monitor` CRD, whose
`SecretKeySelector` fields have no sane single-annotation-value
representation.
```

Also update the sample annotation block a few lines below (currently):
```yaml
    uptime-kuma.io/enabled: "true"
    uptime-kuma.io/interval: "60"
    uptime-kuma.io/accepted-statuscodes: "200-299"
    uptime-kuma.io/tags: "production,customer-facing"
    uptime-kuma.io/notifications: "slack-oncall"
```
to:
```yaml
    uptime-kuma.io/enabled: "true"
    uptime-kuma.io/interval: "60"
    http.uptime-kuma.io/accepted-statuscodes: "200-299"
    uptime-kuma.io/tags: "production,customer-facing"
    uptime-kuma.io/notifications: "slack-oncall"
```

- [ ] **Step 2: Add a "Watching Services" section to `docs/README.md`**

Immediately after the existing Ingress/HTTPRoute annotation-usage section (right after the sample annotation block from Step 1), add:

```markdown
## Watching Services

A plain `Service` can be opted in the same way as an Ingress/HTTPRoute,
but — since a Service carries no routing information of its own — it can
produce a monitor of **any** Kuma type, selected via `uptime-kuma.io/type`
(default `HTTP`):

```bash
kubectl annotate service my-app uptime-kuma.io/enabled=true
```

By default, the check target is the Service's in-cluster DNS name
(`<name>.<namespace>.svc.cluster.local`); every type's `host` annotation
(`http.uptime-kuma.io/host`, `.../tcp/host`, etc.) overrides that. HTTP,
TCP, and Gamedig monitors additionally need a Service port: it's
auto-picked when the Service exposes exactly one, otherwise the matching
`port` annotation (by port number or by `Service.spec.ports[].name`) is
required and reconciliation fails with a clear error without it.

```yaml
apiVersion: v1
kind: Service
metadata:
  name: game-server
  annotations:
    uptime-kuma.io/enabled: "true"
    uptime-kuma.io/type: "Gamedig"
    gamedig.uptime-kuma.io/game: "csgo"
spec:
  ports:
    - port: 27015
  selector:
    app: game-server
```
```

- [ ] **Step 3: Update the top-level `README.md`**

Change:
```markdown
Syncs Uptime Kuma monitors from `Ingress` and `HTTPRoute` resources, and
from a `Monitor` CRD for manually declared DNS/Gamedig/TCP/Ping monitors.
```
to:
```markdown
Syncs Uptime Kuma monitors from `Ingress`, `HTTPRoute`, and `Service`
resources (any monitor type via a `uptime-kuma.io/type` annotation on
`Service`), and from a `Monitor` CRD for full manual control.
```
And change the closing "## Annotations" section:
```markdown
## Annotations

See the design spec's "Annotation contract" section for the full list
(`uptime-kuma.io/enabled`, `.../name`, `.../scheme`, `.../interval`,
`.../retry-interval`, `.../max-retries`, `.../accepted-statuscodes`).
```
to:
```markdown
## Annotations

See [`docs/README.md`](docs/README.md#watching-services) for the full,
per-type annotation tables (top-level plus `http/`, `tcp/`, `ping/`,
`dns/`, `gamedig/`).
```

- [ ] **Step 4: Commit**

```bash
git add README.md docs/README.md
git commit -m "$(cat <<'EOF'
docs: document Service support and the rescoped annotation contract

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## Task 9: Full verification

**Files:** none (verification only).

- [ ] **Step 1: Run the full test suite**

Run: `make test`
Expected: PASS for every package (`go test ./... -count=1`), including `internal/annotations`, `internal/derive`, `internal/controller`, `cmd`.

- [ ] **Step 2: Run manifest generation and confirm nothing drifted**

Run: `make manifests`
Expected: no diff (`git status` shows no changes to `config/crd/bases/` or the chart's `crds/`) — this task adds no CRD, so this should be a no-op. If `git diff` shows any change, investigate before proceeding (it would mean something in `api/v1alpha1` was touched, which this plan does not do).

- [ ] **Step 3: Run the RBAC chart test and `helm lint` again**

Run: `./charts/uptime-kuma-operator/templates/tests/rbac_test.sh && helm lint charts/uptime-kuma-operator --set kuma.url=https://kuma.example.com --set kuma.existingSecret=kuma-credentials`
Expected: `OK` and `0 chart(s) failed`.

- [ ] **Step 4: Run pre-commit's full hook set**

Run: `pre-commit run --all-files`
Expected: every hook passes (`golangci-lint`, `govulncheck`, `helm lint`, the RBAC test, trailing-whitespace/EOF checks, etc. — same set that ran automatically on each commit above, run here once more across the whole tree as a final gate).

No commit for this task — it's verification only. If any step fails, fix the underlying issue in the relevant earlier task's files and re-run from Step 1.
