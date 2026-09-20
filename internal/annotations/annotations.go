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
// Scoped annotations use a "<type>.uptime-kuma.io/<field>" key, since a
// Kubernetes annotation key permits only one "/" (separating the DNS
// subdomain prefix from the name) — "uptime-kuma.io/http/timeout" is not a
// legal annotation key. Putting the type in the subdomain instead (the same
// pattern as e.g. cert-manager.io/*) keeps the grouping while staying valid.
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
