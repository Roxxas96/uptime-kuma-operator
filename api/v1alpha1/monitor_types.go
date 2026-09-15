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
}

// +kubebuilder:object:generate=true
type TCPMonitorSpec struct {
	Host string `json:"host"`
	Port int32  `json:"port"`
}

// +kubebuilder:object:generate=true
type PingMonitorSpec struct {
	Host string `json:"host"`
}

// +kubebuilder:object:generate=true
type DNSMonitorSpec struct {
	Host           string `json:"host"`
	ResolverServer string `json:"resolverServer,omitempty"`
	ResolveType    string `json:"resolveType,omitempty"`
	Port           int32  `json:"port,omitempty"`
}

// +kubebuilder:object:generate=true
type GamedigMonitorSpec struct {
	Host string `json:"host"`
	Port int32  `json:"port"`
	Game string `json:"game"`
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
