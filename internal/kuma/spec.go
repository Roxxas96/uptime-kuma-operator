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
}

type TCPSpec struct {
	Host string
	Port int
}

type PingSpec struct {
	Host string
}

type DNSSpec struct {
	Host           string
	ResolverServer string
	ResolveType    string
	Port           int
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

	HTTP    *HTTPSpec
	TCP     *TCPSpec
	Ping    *PingSpec
	DNS     *DNSSpec
	Gamedig *GamedigSpec
}
