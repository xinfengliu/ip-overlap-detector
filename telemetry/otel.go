package telemetry

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpgrpc"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/global"
	"go.opentelemetry.io/otel/propagation"
	controller "go.opentelemetry.io/otel/sdk/metric/controller/basic"
	processor "go.opentelemetry.io/otel/sdk/metric/processor/basic"
	"go.opentelemetry.io/otel/sdk/metric/selector/simple"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/semconv"
	"go.opentelemetry.io/otel/trace"
)

var (
	tracer       = otel.Tracer("iod-tracer")
	meter        = global.Meter("iod-meter")
	commonLabels = []attribute.KeyValue{
		attribute.String("client", "cli"),
	}
	lastLbErrCnt                         int64
	metricLock                           sync.Mutex
	shutdown                             func()
	olErrCntRecorder, nodeErrCntRecorder metric.BoundInt64ValueRecorder
)

// KV represent an opentelemetry attribute
// type KV struct {
// 	K string
// 	V string
// }

// GetTracer returns the tracer used in this application
func GetTracer() trace.Tracer {
	return tracer
}

// StartTrace is a wrapper of tracer.Start()
func StartTrace(ctx context.Context, name string) (context.Context, trace.Span) {
	return tracer.Start(ctx, name, trace.WithAttributes(commonLabels...))
}

// // NewAttribute creates opentelemetry attributes
// func NewAttribute(kvs ...KV) trace.LifeCycleOption {
// 	attrs := []attribute.KeyValue{}
// 	for _, kv := range kvs {
// 		attrs = append(attrs, attribute.String(kv.K, kv.V))
// 	}
// 	return trace.WithAttributes(attrs...)
// }

// NewAttribute creates opentelemetry attributes
func NewAttribute(m map[string]string) trace.LifeCycleOption {
	attrs := []attribute.KeyValue{}
	for k, v := range m {
		attrs = append(attrs, attribute.String(k, v))
	}
	return trace.WithAttributes(attrs...)
}

// Init initialize the telemetry facilities
func Init() {
	shutdown = initProvider()

	// using observer is just for demoing observer usage
	metric.Must(meter).
		NewInt64ValueObserver(
			"iod/lbErrCnt",
			func(_ context.Context, result metric.Int64ObserverResult) {
				result.Observe(GetLbErrCnt())
			},
			metric.WithDescription("The number of LB IP errors"),
		)

	// recorder
	olErrCntRecorder = metric.Must(meter).
		NewInt64ValueRecorder(
			"iod/olErrCnt",
			metric.WithDescription("The number of IP overlappings"),
		).Bind(commonLabels...)

	nodeErrCntRecorder = metric.Must(meter).
		NewInt64ValueRecorder(
			"iod/nodeErrCnt",
			metric.WithDescription("The number of IP overlappings"),
		).Bind(commonLabels...)
}

// Shutdown closes resources relating to the telemetry facilities
func Shutdown() {
	shutdown()
}

// GetLbErrCnt retrieves LB IP error count for metric observer
func GetLbErrCnt() int64 {
	metricLock.Lock()
	defer metricLock.Unlock()
	return lastLbErrCnt
}

// SetLbErrCnt set LB IP error count for metric observer
func SetLbErrCnt(v int64) {
	metricLock.Lock()
	lastLbErrCnt = v
	metricLock.Unlock()
}

// SetOlErrCnt set IP overlapping error count for metric recorder
func SetOlErrCnt(ctx context.Context, v int64) {
	olErrCntRecorder.Record(ctx, v)
}

// SetNodeErrCnt set node error count for metric recorder
func SetNodeErrCnt(ctx context.Context, v int64) {
	nodeErrCntRecorder.Record(ctx, v)
}

// Initializes an OTLP exporter, and configures the corresponding trace and
// metric providers.
func initProvider() func() {
	ctx := context.Background()

	otelAgentAddr, ok := os.LookupEnv("OTEL_AGENT_ENDPOINT")
	if !ok {
		otelAgentAddr = "127.0.0.1:4317"
	}

	exp, err := otlp.NewExporter(ctx, otlpgrpc.NewDriver(
		otlpgrpc.WithInsecure(),
		otlpgrpc.WithEndpoint(otelAgentAddr),
		otlpgrpc.WithDialOption(grpc.WithBlock()), // useful for testing
	))
	handleErr(err, "failed to create exporter")

	res, err := resource.New(ctx,
		resource.WithAttributes(
			// the service name used to display traces in backends
			semconv.ServiceNameKey.String("ip-overlap-detector"),
		),
	)
	handleErr(err, "failed to create resource")

	bsp := sdktrace.NewBatchSpanProcessor(exp)
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithResource(res),
		sdktrace.WithSpanProcessor(bsp),
	)

	cont := controller.New(
		processor.New(
			simple.NewWithExactDistribution(),
			exp,
		),
		controller.WithCollectPeriod(5*time.Second),
		controller.WithExporter(exp),
	)

	// set global propagator to tracecontext (the default is no-op).
	otel.SetTextMapPropagator(propagation.TraceContext{})
	otel.SetTracerProvider(tracerProvider)
	global.SetMeterProvider(cont.MeterProvider())
	handleErr(cont.Start(context.Background()), "failed to start metric controller")

	return func() {
		handleErr(tracerProvider.Shutdown(ctx), "failed to shutdown provider")
		handleErr(cont.Stop(context.Background()), "failed to stop metrics controller") // pushes any last exports to the receiver
		handleErr(exp.Shutdown(ctx), "failed to stop exporter")
	}
}

func handleErr(err error, message string) {
	if err != nil {
		logrus.Fatalf("%s: %v", message, err)
	}
}
