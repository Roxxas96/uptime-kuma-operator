// +groupName=uptime-kuma.io
// Package v1alpha1 contains API types for the uptime-kuma.io/v1alpha1 group.
package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	GroupVersion  = schema.GroupVersion{Group: "uptime-kuma.io", Version: "v1alpha1"}
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion} //nolint:staticcheck // scheme.Builder is deprecated in favor of hand-rolled AddToScheme; this is the standard kubebuilder scaffold pattern
	AddToScheme   = SchemeBuilder.AddToScheme
)
