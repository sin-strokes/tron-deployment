// Package observability sets up OpenTelemetry tracing for trond.
//
// trond is short-lived (most invocations are one cobra command from
// process start to exit), so the design is:
//
//   - default: no-op tracer provider (zero overhead, no network IO)
//   - opt-in: when TROND_OTLP_ENDPOINT is set, an OTLP-over-HTTP
//     exporter ships spans to the configured collector at process
//     exit (Shutdown blocks for up to TROND_OTLP_TIMEOUT, default 5s)
//
// What gets traced today: a single root span per cobra invocation
// covering Execute(). Subcommands and internal packages can attach
// child spans via Tracer().Start(ctx, "name").
//
// Why we ship this as opt-in rather than always-on: trond often runs
// in CI / cron / detached daemon contexts where a network call to a
// missing collector would either fail noisily (bad UX) or block on
// dial timeout (worse). TROND_OTLP_ENDPOINT explicitly authorises the
// outbound traffic.
package observability

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

const (
	envEndpoint = "TROND_OTLP_ENDPOINT"
	envInsecure = "TROND_OTLP_INSECURE" // any non-empty value disables TLS
	envTimeout  = "TROND_OTLP_TIMEOUT"  // Go duration; default 5s
	envHeaders  = "TROND_OTLP_HEADERS"  // comma-separated key=value pairs
)

// Init configures the global OpenTelemetry tracer provider. Returns
// a Shutdown function the caller must defer; passing it to defer at
// the top of main() is the canonical shape.
//
// When TROND_OTLP_ENDPOINT is unset Init is a near-no-op: the global
// provider is set to a noop tracer (so callers can blindly call
// Tracer().Start() without nil checks) and Shutdown is a noop.
//
// Version is stamped into the resource as service.version so
// distributed traces can be correlated across releases.
func Init(ctx context.Context, version string) (shutdown func(context.Context) error) {
	endpoint := strings.TrimSpace(os.Getenv(envEndpoint))
	if endpoint == "" {
		// No-op path: install a no-op provider so call sites that do
		// `Tracer().Start(...)` work without conditional checks.
		otel.SetTracerProvider(noop.NewTracerProvider())
		return func(context.Context) error { return nil }
	}

	exporter, err := newExporter(ctx, endpoint)
	if err != nil {
		// Don't fail the whole CLI just because telemetry didn't
		// initialise. Surface the reason to stderr and fall back to
		// no-op so the user can investigate without losing their
		// actual trond invocation.
		fmt.Fprintf(os.Stderr, "trond: OTLP exporter init failed (%v); tracing disabled\n", err)
		otel.SetTracerProvider(noop.NewTracerProvider())
		return func(context.Context) error { return nil }
	}

	res, _ := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName("trond"),
			semconv.ServiceVersion(version),
		),
	)

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	return func(ctx context.Context) error {
		timeout := parseTimeout()
		shutdownCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return tp.Shutdown(shutdownCtx)
	}
}

// Tracer returns trond's named tracer. Components attach child spans
// via Tracer().Start(ctx, name). Safe to call before Init — returns
// the global no-op tracer until Init swaps in the real one.
func Tracer() trace.Tracer {
	return otel.Tracer("github.com/tronprotocol/tron-deployment")
}

func newExporter(ctx context.Context, endpoint string) (sdktrace.SpanExporter, error) {
	opts := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(stripScheme(endpoint)),
	}
	if scheme := schemeOf(endpoint); scheme == "http" {
		opts = append(opts, otlptracehttp.WithInsecure())
	} else if v := os.Getenv(envInsecure); v != "" {
		opts = append(opts, otlptracehttp.WithInsecure())
	}
	if h := parseHeaders(); len(h) > 0 {
		opts = append(opts, otlptracehttp.WithHeaders(h))
	}
	return otlptrace.New(ctx, otlptracehttp.NewClient(opts...))
}

// schemeOf returns "http", "https", or "" — used so we can default
// WithInsecure correctly without forcing the user to set both env vars.
func schemeOf(endpoint string) string {
	switch {
	case strings.HasPrefix(endpoint, "https://"):
		return "https"
	case strings.HasPrefix(endpoint, "http://"):
		return "http"
	default:
		return ""
	}
}

// stripScheme normalises endpoint for otlptracehttp.WithEndpoint,
// which expects host:port not http(s)://host:port.
func stripScheme(endpoint string) string {
	for _, p := range []string{"https://", "http://"} {
		if rest, ok := strings.CutPrefix(endpoint, p); ok {
			return rest
		}
	}
	return endpoint
}

func parseTimeout() time.Duration {
	if v := os.Getenv(envTimeout); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return 5 * time.Second
}

func parseHeaders() map[string]string {
	v := os.Getenv(envHeaders)
	if v == "" {
		return nil
	}
	out := map[string]string{}
	for pair := range strings.SplitSeq(v, ",") {
		idx := strings.IndexByte(pair, '=')
		if idx <= 0 {
			continue
		}
		out[strings.TrimSpace(pair[:idx])] = strings.TrimSpace(pair[idx+1:])
	}
	return out
}
