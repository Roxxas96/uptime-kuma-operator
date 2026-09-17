package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:validation:Enum=HTTP;TCP;Ping;DNS;Gamedig
type MonitorType string

const (
	MonitorTypeHTTP    MonitorType = "HTTP"
	MonitorTypeTCP     MonitorType = "TCP"
	MonitorTypePing    MonitorType = "Ping"
	MonitorTypeDNS     MonitorType = "DNS"
	MonitorTypeGamedig MonitorType = "Gamedig"
)

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
	// +kubebuilder:validation:Enum="";nostarttls;secure;starttls
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

// The first five rules require the sub-struct matching spec.type; the second
// five reject every other sub-struct, so the discriminator is exclusive rather
// than silently ignoring extra configuration.
//
// +kubebuilder:validation:XValidation:rule="self.type != 'HTTP' || has(self.http)",message="spec.http is required when type is HTTP"
// +kubebuilder:validation:XValidation:rule="self.type != 'TCP' || has(self.tcp)",message="spec.tcp is required when type is TCP"
// +kubebuilder:validation:XValidation:rule="self.type != 'Ping' || has(self.ping)",message="spec.ping is required when type is Ping"
// +kubebuilder:validation:XValidation:rule="self.type != 'DNS' || has(self.dns)",message="spec.dns is required when type is DNS"
// +kubebuilder:validation:XValidation:rule="self.type != 'Gamedig' || has(self.gamedig)",message="spec.gamedig is required when type is Gamedig"
// +kubebuilder:validation:XValidation:rule="self.type == 'HTTP' || !has(self.http)",message="spec.http must not be set unless type is HTTP"
// +kubebuilder:validation:XValidation:rule="self.type == 'TCP' || !has(self.tcp)",message="spec.tcp must not be set unless type is TCP"
// +kubebuilder:validation:XValidation:rule="self.type == 'Ping' || !has(self.ping)",message="spec.ping must not be set unless type is Ping"
// +kubebuilder:validation:XValidation:rule="self.type == 'DNS' || !has(self.dns)",message="spec.dns must not be set unless type is DNS"
// +kubebuilder:validation:XValidation:rule="self.type == 'Gamedig' || !has(self.gamedig)",message="spec.gamedig must not be set unless type is Gamedig"
// +kubebuilder:object:generate=true
type MonitorSpec struct {
	Type MonitorType `json:"type"`

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

	// Notifications lists notification channel names to alert on this
	// monitor. Each must already exist in Kuma — an unresolvable name
	// fails the reconcile.
	Notifications []string `json:"notifications,omitempty"`
	// Group is the name of an existing parent group monitor. Must already
	// exist in Kuma — an unresolvable name fails the reconcile.
	Group string `json:"group,omitempty"`
	// Proxy is the numeric ID of an existing Kuma proxy to route checks
	// through. Kuma proxies have no name field, so this is ID-based.
	Proxy int64 `json:"proxy,omitempty"`

	HTTP    *HTTPMonitorSpec    `json:"http,omitempty"`
	TCP     *TCPMonitorSpec     `json:"tcp,omitempty"`
	Ping    *PingMonitorSpec    `json:"ping,omitempty"`
	DNS     *DNSMonitorSpec     `json:"dns,omitempty"`
	Gamedig *GamedigMonitorSpec `json:"gamedig,omitempty"`
}

// +kubebuilder:object:generate=true
type MonitorStatus struct {
	MonitorID string `json:"monitorID,omitempty"`
	// ObservedGeneration is the .metadata.generation last successfully
	// synced to Kuma. The reconciler skips the Kuma round-trip when it
	// already matches .metadata.generation — kuma.Client.Upsert (editMonitor)
	// restarts the monitor's check timer even on an unchanged payload, so
	// calling it on every reconcile (including ones triggered by something
	// other than a spec change) would prevent the monitor from ever
	// completing more than one check cycle.
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=".spec.type"
// +kubebuilder:printcolumn:name="MonitorID",type=string,JSONPath=".status.monitorID"
type Monitor struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MonitorSpec   `json:"spec,omitempty"`
	Status MonitorStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type MonitorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Monitor `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Monitor{}, &MonitorList{})
}
