// Package telemetry wires up the operator's OpenTelemetry metrics.
//
// Metrics are always exposed in Prometheus format on the controller-runtime
// metrics endpoint (alongside its built-in reconcile and workqueue metrics),
// and are additionally pushed over OTLP/gRPC when an OTLP endpoint is
// configured through the standard OTEL_EXPORTER_OTLP_* environment variables.
package telemetry

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
)

// ServiceName is the default service.name resource attribute; OTEL_SERVICE_NAME
// and OTEL_RESOURCE_ATTRIBUTES override it.
const ServiceName = "uptime-kuma-operator"

// MeterName is the instrumentation scope for every operator instrument.
const MeterName = "uptime-kuma-operator"

// Setup builds a MeterProvider exporting to reg (in Prometheus format) and,
// when OTLP is configured in getenv, to an OTLP collector. It installs the
// provider as the global one and returns it along with a shutdown func that
// flushes pending OTLP exports.
func Setup(ctx context.Context, reg prometheus.Registerer, getenv func(string) string) (*sdkmetric.MeterProvider, func(context.Context) error, error) {
	res, err := resource.New(ctx,
		resource.WithAttributes(attribute.String("service.name", ServiceName)),
		resource.WithTelemetrySDK(),
		resource.WithFromEnv(),
	)
	if err != nil {
		return nil, nil, err
	}

	promExporter, err := otelprom.New(
		otelprom.WithRegisterer(reg),
		otelprom.WithoutScopeInfo(),
	)
	if err != nil {
		return nil, nil, err
	}

	opts := []sdkmetric.Option{
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(promExporter),
	}

	if OTLPEnabled(getenv) {
		otlpExporter, err := otlpmetricgrpc.New(ctx)
		if err != nil {
			return nil, nil, err
		}
		opts = append(opts, sdkmetric.WithReader(sdkmetric.NewPeriodicReader(otlpExporter)))
	}

	mp := sdkmetric.NewMeterProvider(opts...)
	otel.SetMeterProvider(mp)
	return mp, mp.Shutdown, nil
}

// OTLPEnabled reports whether an OTLP metrics endpoint is configured.
// OTEL_METRICS_EXPORTER=none disables OTLP even when an endpoint is set.
func OTLPEnabled(getenv func(string) string) bool {
	if getenv("OTEL_METRICS_EXPORTER") == "none" {
		return false
	}
	return getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" ||
		getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT") != ""
}
