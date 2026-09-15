package kuma

// MonitorType is the discriminator for which *Spec field of MonitorSpec is populated.
type MonitorType string

const (
	TypeHTTP    MonitorType = "HTTP"
	TypeTCP     MonitorType = "TCP"
	TypePing    MonitorType = "Ping"
	TypeDNS     MonitorType = "DNS"
	TypeGamedig MonitorType = "Gamedig"
)

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

type PingSpec struct {
	Host string

	// Timeout is the per-ping timeout in seconds. 0 = use Kuma default.
	Timeout int64
	// PacketSize is the ICMP packet size in bytes. 0 = use Kuma default.
	PacketSize int
	// DomainExpiryNotification enables domain expiry notifications.
	DomainExpiryNotification bool
}

type DNSSpec struct {
	Host           string
	ResolverServer string
	ResolveType    string
	Port           int

	// DomainExpiryNotification enables domain expiry notifications.
	DomainExpiryNotification bool
}

type GamedigSpec struct {
	Host string
	Port int
	Game string
}

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
