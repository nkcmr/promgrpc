package promgrpc

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	"github.com/pkg/errors"
	prom "github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"
)

type ctxMonitorKey struct{}
type monitor struct {
	service, method string
	streamType      string // set on HandleRPC.Begin()
}

type defaultPromStats struct {
	metrics *metricsPack
}

var _ stats.Handler = defaultPromStats{}

// TagRPC can attach some information to the given context.
// The context used for the rest lifetime of the RPC will be derived from
// the returned context.
func (d defaultPromStats) TagRPC(parent context.Context, info *stats.RPCTagInfo) context.Context {
	service, method, err := parseMethod(info.FullMethodName)
	if err != nil {
		slog.WarnContext(parent, "promgrpc.TagRPC: failed to parse grpc method", slog.String("error", err.Error()))
		return parent
	}
	return context.WithValue(parent, ctxMonitorKey{}, &monitor{
		service:    service,
		method:     method,
		streamType: "unknown",
	})
}

const (
	streamTypeUnary  = "unary"
	streamTypeClient = "client_stream"
	streamTypeServer = "server_stream"
	streamTypeBidi   = "bidi_stream"
)

func determineStreamType(begin *stats.Begin) string {
	if begin.IsClientStream && !begin.IsServerStream {
		return streamTypeClient
	} else if !begin.IsClientStream && begin.IsServerStream {
		return streamTypeServer
	} else if begin.IsClientStream && begin.IsServerStream {
		return streamTypeBidi
	}
	return streamTypeUnary
}

// HandleRPC processes the RPC stats.
func (d defaultPromStats) HandleRPC(ctx context.Context, statsany stats.RPCStats) {
	carrier, ok := ctx.Value(ctxMonitorKey{}).(*monitor)
	if !ok {
		return
	}
	switch stats := statsany.(type) {
	case *stats.Begin:
		carrier.streamType = determineStreamType(stats)
		d.metrics.startedCounter.WithLabelValues(carrier.streamType, carrier.service, carrier.method).Inc()
	case *stats.InPayload:
		d.metrics.streamMsgReceived.WithLabelValues(carrier.streamType, carrier.service, carrier.method).Inc()
	case *stats.OutPayload:
		d.metrics.streamMsgSent.WithLabelValues(carrier.streamType, carrier.service, carrier.method).Inc()
	case *stats.End:
		dur := stats.EndTime.Sub(stats.BeginTime)
		code := codes.OK
		if stats.Error != nil {
			code = codes.Unknown
		}
		if rpcStatus, ok := status.FromError(stats.Error); ok {
			code = rpcStatus.Code()
		}
		d.metrics.handlingSeconds.WithLabelValues(carrier.streamType, carrier.service, carrier.method, code.String()).Observe(dur.Seconds())
	}
}

func (d defaultPromStats) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}

func (d defaultPromStats) HandleConn(ctx context.Context, _ stats.ConnStats) {}

type metricsPack struct {
	startedCounter, handledCounter, streamMsgReceived, streamMsgSent *prom.CounterVec
	handlingSeconds                                                  *prom.HistogramVec
}

func (m *metricsPack) Describe(ch chan<- *prom.Desc) {
	m.startedCounter.Describe(ch)
	m.handledCounter.Describe(ch)
	m.streamMsgReceived.Describe(ch)
	m.streamMsgSent.Describe(ch)
	m.handlingSeconds.Describe(ch)
}

func (m *metricsPack) Collect(ch chan<- prom.Metric) {
	m.startedCounter.Collect(ch)
	m.handledCounter.Collect(ch)
	m.streamMsgReceived.Collect(ch)
	m.streamMsgSent.Collect(ch)
	m.handlingSeconds.Collect(ch)
}

var defaultServerMetrics = newServerMetrics()
var defaultClientMetrics = newClientMetrics()

func newServerMetrics() *metricsPack {
	return &metricsPack{
		startedCounter: prom.NewCounterVec(
			prom.CounterOpts{
				Name: "grpc_server_started_total",
				Help: "Total number of RPCs started on the server.",
			}, []string{"grpc_type", "grpc_service", "grpc_method"},
		),
		handledCounter: prom.NewCounterVec(
			prom.CounterOpts{
				Name: "grpc_server_handled_total",
				Help: "Total number of RPCs completed on the server, regardless of success or failure.",
			}, []string{"grpc_type", "grpc_service", "grpc_method", "grpc_code"},
		),
		streamMsgReceived: prom.NewCounterVec(
			prom.CounterOpts{
				Name: "grpc_server_msg_received_total",
				Help: "Total number of RPC stream messages received on the server.",
			}, []string{"grpc_type", "grpc_service", "grpc_method"},
		),
		streamMsgSent: prom.NewCounterVec(
			prom.CounterOpts{
				Name: "grpc_server_msg_sent_total",
				Help: "Total number of gRPC stream messages sent by the server.",
			}, []string{"grpc_type", "grpc_service", "grpc_method"},
		),
		handlingSeconds: prom.NewHistogramVec(prom.HistogramOpts{
			Name:    "grpc_server_handling_seconds",
			Help:    "Histogram of response latency (seconds) of gRPC that had been application-level handled by the server.",
			Buckets: prom.DefBuckets,
		}, []string{"grpc_type", "grpc_service", "grpc_method"}),
	}
}

func newClientMetrics() *metricsPack {
	return &metricsPack{
		startedCounter: prom.NewCounterVec(
			prom.CounterOpts{
				Name: "grpc_client_started_total",
				Help: "Total number of RPCs started on the client.",
			}, []string{"grpc_type", "grpc_service", "grpc_method"},
		),
		handledCounter: prom.NewCounterVec(
			prom.CounterOpts{
				Name: "grpc_client_handled_total",
				Help: "Total number of RPCs completed by the client, regardless of success or failure.",
			}, []string{"grpc_type", "grpc_service", "grpc_method", "grpc_code"},
		),
		streamMsgReceived: prom.NewCounterVec(
			prom.CounterOpts{
				Name: "grpc_client_msg_received_total",
				Help: "Total number of RPC stream messages received by the client.",
			}, []string{"grpc_type", "grpc_service", "grpc_method"},
		),
		streamMsgSent: prom.NewCounterVec(
			prom.CounterOpts{
				Name: "grpc_client_msg_sent_total",
				Help: "Total number of gRPC stream messages sent by the client.",
			}, []string{"grpc_type", "grpc_service", "grpc_method"},
		),
		handlingSeconds: prom.NewHistogramVec(prom.HistogramOpts{
			Name:    "grpc_client_handling_seconds",
			Help:    "Histogram of response latency (seconds) of the gRPC until it is finished by the application.",
			Buckets: prom.DefBuckets,
		}, []string{"grpc_type", "grpc_service", "grpc_method"}),
	}
}

var registerServerMetrics = sync.OnceFunc(func() {
	prom.MustRegister(defaultServerMetrics)
})

var registerClientMetrics = sync.OnceFunc(func() {
	prom.MustRegister(defaultClientMetrics)
})

func StatsHandler() grpc.ServerOption {
	registerServerMetrics()
	return grpc.StatsHandler(defaultPromStats{metrics: defaultServerMetrics})
}

func WithStatsHandler() grpc.DialOption {
	registerClientMetrics()
	return grpc.WithStatsHandler(defaultPromStats{metrics: defaultClientMetrics})
}

func StatsHandlerOnRegistry(pr prom.Registerer) grpc.ServerOption {
	m := newServerMetrics()
	pr.MustRegister(m)
	return grpc.StatsHandler(defaultPromStats{metrics: m})
}

func WithStatsHandlerOnRegistry(pr prom.Registerer) grpc.DialOption {
	m := newClientMetrics()
	pr.MustRegister(m)
	return grpc.WithStatsHandler(defaultPromStats{metrics: m})
}

// parseMethod splits service and method from the input. It expects format
// "/service/method".
//
// Copied from: https://github.com/grpc/grpc-go/blob/00d3ec8c71a7ba0f9c755881f0f7147eba5e814c/internal/grpcutil/method.go#L26-L39
func parseMethod(methodName string) (service, method string, _ error) {
	if !strings.HasPrefix(methodName, "/") {
		return "", "", errors.New("invalid method name: should start with /")
	}
	methodName = methodName[1:]

	pos := strings.LastIndex(methodName, "/")
	if pos < 0 {
		return "", "", errors.New("invalid method name: suffix /method is missing")
	}
	return methodName[:pos], methodName[pos+1:], nil
}
