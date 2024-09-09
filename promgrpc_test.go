package promgrpc_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"code.nkcmr.net/opt"
	"code.nkcmr.net/promgrpc"
	"github.com/pkg/errors"
	prom "github.com/prometheus/client_golang/prometheus"
	promtest "github.com/prometheus/client_golang/prometheus/testutil"
	io_prometheus_client "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/examples/features/proto/echo"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type testEchoServer struct {
	echo.UnimplementedEchoServer
}

var _ echo.EchoServer = testEchoServer{}

// UnaryEcho is unary echo.

func (t testEchoServer) UnaryEcho(_ context.Context, request *echo.EchoRequest) (*echo.EchoResponse, error) {
	if strings.Contains(request.GetMessage(), "sleep_100ms") {
		time.Sleep(time.Millisecond * 100)
	}
	if strings.Contains(request.Message, "bad_code") {
		return nil, status.New(codes.PermissionDenied, "permission_denied").Err()
	}
	return &echo.EchoResponse{Message: request.Message}, nil
}

var nTimesPattern = regexp.MustCompile(`\b([0-9]+)_times\b`)

func extractNTimes(s string) int {
	nTimes := int(10)
	if matches := nTimesPattern.FindAllStringSubmatch(s, -1); len(matches) > 0 {
		n, err := strconv.Atoi(matches[0][1])
		if err != nil {
			panic(err)
		}
		nTimes = n
	}
	return nTimes
}

// ServerStreamingEcho is server side streaming.
func (t testEchoServer) ServerStreamingEcho(request *echo.EchoRequest, stream grpc.ServerStreamingServer[echo.EchoResponse]) error {
	for range extractNTimes(request.GetMessage()) {
		if err := stream.Send(&echo.EchoResponse{
			Message: request.GetMessage(),
		}); err != nil {
			return err
		}
	}
	return nil
}

// // ClientStreamingEcho is client side streaming.
// func (t testEchoServer) ClientStreamingEcho(stream grpc.ClientStreamingServer[echo.EchoRequest, echo.EchoResponse]) error {
// 	return nil
// }

// // BidirectionalStreamingEcho is bidi streaming.
// func (t testEchoServer) BidirectionalStreamingEcho(_ grpc.BidiStreamingServer[echo.EchoRequest, echo.EchoResponse]) error {
// 	panic("not implemented") // TODO: Implement
// }

func newTestServer(t *testing.T, pr *prom.Registry) echo.EchoClient {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	gsrv := grpc.NewServer(promgrpc.StatsHandlerOnRegistry(pr))
	echo.RegisterEchoServer(gsrv, testEchoServer{})
	go func() {
		_ = gsrv.Serve(lis)
	}()
	t.Cleanup(func() {
		_ = lis.Close()
	})
	conn, err := grpc.DialContext(
		context.Background(),
		"bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return lis.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		promgrpc.WithStatsHandlerOnRegistry(pr),
	)
	require.NoError(t, err)
	return echo.NewEchoClient(conn)
}

func counterMetrics(kind string) []string {
	return []string{
		fmt.Sprintf("grpc_%s_started_total", kind),
		fmt.Sprintf("grpc_%s_handled_total", kind),
		fmt.Sprintf("grpc_%s_msg_received_total", kind),
		fmt.Sprintf("grpc_%s_msg_sent_total", kind),
	}
}

// func histogramMetrics(kind string) []string {
// 	return []string{
// 		fmt.Sprintf("grpc_%s_handling_seconds", kind),
// 	}
// }

func find[E any, S ~[]E](s S, ff func(E) bool) opt.Option[E] {
	for _, e := range s {
		if ff(e) {
			return opt.Some(e)
		}
	}
	return opt.None[E]()
}

func requireHistogram(t *testing.T, pr *prom.Registry, metric string, grpcType, service, method string, buckets map[float64]int) {
	t.Helper()
	metrics, err := pr.Gather()
	require.NoError(t, err)
	histmf, ok := find(metrics, func(mf *io_prometheus_client.MetricFamily) bool {
		return mf.GetName() == metric && mf.GetType() == io_prometheus_client.MetricType_HISTOGRAM
	}).MaybeUnwrap()
	require.True(t, ok)
	histm, ok := find(
		histmf.GetMetric(),
		func(m *io_prometheus_client.Metric) bool {
			for k, v := range map[string]string{
				"grpc_type":    grpcType,
				"grpc_service": service,
				"grpc_method":  method,
			} {
				lv, ok := find(m.GetLabel(), func(l *io_prometheus_client.LabelPair) bool { return l.GetName() == k }).MaybeUnwrap()
				if !ok {
					return false
				}
				if lv.GetValue() != v {
					return false
				}
			}
			return true
		},
	).MaybeUnwrap()
	require.True(t, ok)
	actual := map[float64]int{}
	for _, b := range histm.GetHistogram().GetBucket() {
		actual[b.GetUpperBound()] = int(b.GetCumulativeCount())
	}
	require.Equal(t, buckets, actual)
}

func TestUnary(t *testing.T) {
	t.Run("server", func(t *testing.T) {
		pr := prom.NewRegistry()
		echoClient := newTestServer(t, pr)
		_, err := echoClient.UnaryEcho(context.Background(), &echo.EchoRequest{Message: "ec7acad8-4c58-4c99-8ded-d124a63cc15f"})
		require.NoError(t, err)
		_, err = echoClient.UnaryEcho(context.Background(), &echo.EchoRequest{Message: "167cccf0-ea11-4dad-b4c6-7fad29e4ae36 (sleep_100ms)"})
		require.NoError(t, err)
		_, err = echoClient.UnaryEcho(context.Background(), &echo.EchoRequest{Message: "926780f9-0a98-4918-ac7f-11e08b63adcf (bad_code)"})
		require.Error(t, err)
		require.Equal(t, "rpc error: code = PermissionDenied desc = permission_denied", err.Error())

		requireHistogram(t, pr, "grpc_server_handling_seconds", "unary", "grpc.examples.echo.Echo", "UnaryEcho", map[float64]int{
			0.005: 2,
			0.01:  2,
			0.025: 2,
			0.05:  2,
			0.1:   2,
			// because of the sleep request
			0.25: 3,
			0.5:  3,
			1:    3,
			2.5:  3,
			5:    3,
			10:   3,
		})
		err = promtest.GatherAndCompare(pr, strings.NewReader(`
# HELP grpc_server_handled_total Total number of RPCs completed on the server, regardless of success or failure.
# TYPE grpc_server_handled_total counter
grpc_server_handled_total{grpc_code="OK",grpc_method="UnaryEcho",grpc_service="grpc.examples.echo.Echo",grpc_type="unary"} 2
grpc_server_handled_total{grpc_code="PermissionDenied",grpc_method="UnaryEcho",grpc_service="grpc.examples.echo.Echo",grpc_type="unary"} 1
# HELP grpc_server_msg_received_total Total number of RPC stream messages received on the server.
# TYPE grpc_server_msg_received_total counter
grpc_server_msg_received_total{grpc_method="UnaryEcho",grpc_service="grpc.examples.echo.Echo",grpc_type="unary"} 3
# HELP grpc_server_msg_sent_total Total number of gRPC stream messages sent by the server.
# TYPE grpc_server_msg_sent_total counter
grpc_server_msg_sent_total{grpc_method="UnaryEcho",grpc_service="grpc.examples.echo.Echo",grpc_type="unary"} 2
# HELP grpc_server_started_total Total number of RPCs started on the server.
# TYPE grpc_server_started_total counter
grpc_server_started_total{grpc_method="UnaryEcho",grpc_service="grpc.examples.echo.Echo",grpc_type="unary"} 3
`), counterMetrics("server")...)
		require.NoError(t, err)
	})
	t.Run("client", func(t *testing.T) {
		pr := prom.NewRegistry()
		echoClient := newTestServer(t, pr)
		_, err := echoClient.UnaryEcho(context.Background(), &echo.EchoRequest{Message: "0017a656-49b1-40cc-b6fb-1e924040fa70"})
		require.NoError(t, err)
		_, err = echoClient.UnaryEcho(context.Background(), &echo.EchoRequest{Message: "322ea012-59f6-4228-89c7-96a4f3f92ade (sleep_100ms)"})
		require.NoError(t, err)
		_, err = echoClient.UnaryEcho(context.Background(), &echo.EchoRequest{Message: "0144143c-8c96-4426-93ab-a293c14f00c0 (bad_code)"})
		require.Error(t, err)
		require.Equal(t, "rpc error: code = PermissionDenied desc = permission_denied", err.Error())
		requireHistogram(t, pr, "grpc_client_handling_seconds", "unary", "grpc.examples.echo.Echo", "UnaryEcho", map[float64]int{
			0.005: 2,
			0.01:  2,
			0.025: 2,
			0.05:  2,
			0.1:   2,
			// because of the sleep request
			0.25: 3,
			0.5:  3,
			1:    3,
			2.5:  3,
			5:    3,
			10:   3,
		})
		err = promtest.GatherAndCompare(pr, strings.NewReader(`
# HELP grpc_client_handled_total Total number of RPCs completed by the client, regardless of success or failure.
# TYPE grpc_client_handled_total counter
grpc_client_handled_total{grpc_code="OK",grpc_method="UnaryEcho",grpc_service="grpc.examples.echo.Echo",grpc_type="unary"} 2
grpc_client_handled_total{grpc_code="PermissionDenied",grpc_method="UnaryEcho",grpc_service="grpc.examples.echo.Echo",grpc_type="unary"} 1
# HELP grpc_client_msg_received_total Total number of RPC stream messages received by the client.
# TYPE grpc_client_msg_received_total counter
grpc_client_msg_received_total{grpc_method="UnaryEcho",grpc_service="grpc.examples.echo.Echo",grpc_type="unary"} 2
# HELP grpc_client_msg_sent_total Total number of gRPC stream messages sent by the client.
# TYPE grpc_client_msg_sent_total counter
grpc_client_msg_sent_total{grpc_method="UnaryEcho",grpc_service="grpc.examples.echo.Echo",grpc_type="unary"} 3
# HELP grpc_client_started_total Total number of RPCs started on the client.
# TYPE grpc_client_started_total counter
grpc_client_started_total{grpc_method="UnaryEcho",grpc_service="grpc.examples.echo.Echo",grpc_type="unary"} 3
`), counterMetrics("client")...)
		require.NoError(t, err)
	})
}

func TestServerStream(t *testing.T) {
	t.Run("server", func(t *testing.T) {
		pr := prom.NewRegistry()
		echoClient := newTestServer(t, pr)
		stream, err := echoClient.ServerStreamingEcho(context.Background(), &echo.EchoRequest{Message: "8436275e-e0cd-4393-85b1-acdf79455738 (3_times)"})
		require.NoError(t, err)
		times := int(0)
		for {
			_, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				break
			}
			require.NoError(t, err)
			times++
		}
		require.Equal(t, int(3), times)
		requireHistogram(t, pr, "grpc_server_handling_seconds", "server_stream", "grpc.examples.echo.Echo", "ServerStreamingEcho", map[float64]int{
			0.005: 1,
			0.01:  1,
			0.025: 1,
			0.05:  1,
			0.1:   1,
			// because of the sleep request
			0.25: 1,
			0.5:  1,
			1:    1,
			2.5:  1,
			5:    1,
			10:   1,
		})
		err = promtest.GatherAndCompare(pr, strings.NewReader(`
# HELP grpc_server_handled_total Total number of RPCs completed on the server, regardless of success or failure.
# TYPE grpc_server_handled_total counter
grpc_server_handled_total{grpc_code="OK",grpc_method="ServerStreamingEcho",grpc_service="grpc.examples.echo.Echo",grpc_type="server_stream"} 1
# HELP grpc_server_msg_received_total Total number of RPC stream messages received on the server.
# TYPE grpc_server_msg_received_total counter
grpc_server_msg_received_total{grpc_method="ServerStreamingEcho",grpc_service="grpc.examples.echo.Echo",grpc_type="server_stream"} 1
# HELP grpc_server_msg_sent_total Total number of gRPC stream messages sent by the server.
# TYPE grpc_server_msg_sent_total counter
grpc_server_msg_sent_total{grpc_method="ServerStreamingEcho",grpc_service="grpc.examples.echo.Echo",grpc_type="server_stream"} 3
# HELP grpc_server_started_total Total number of RPCs started on the server.
# TYPE grpc_server_started_total counter
grpc_server_started_total{grpc_method="ServerStreamingEcho",grpc_service="grpc.examples.echo.Echo",grpc_type="server_stream"} 1
`), counterMetrics("server")...)
		require.NoError(t, err)
	})
	t.Run("client", func(t *testing.T) {
		pr := prom.NewRegistry()
		echoClient := newTestServer(t, pr)
		stream, err := echoClient.ServerStreamingEcho(context.Background(), &echo.EchoRequest{Message: "8436275e-e0cd-4393-85b1-acdf79455738 (3_times)"})
		require.NoError(t, err)
		times := int(0)
		for {
			_, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				break
			}
			require.NoError(t, err)
			times++
		}
		require.Equal(t, int(3), times)
		requireHistogram(t, pr, "grpc_client_handling_seconds", "server_stream", "grpc.examples.echo.Echo", "ServerStreamingEcho", map[float64]int{
			0.005: 1,
			0.01:  1,
			0.025: 1,
			0.05:  1,
			0.1:   1,
			// because of the sleep request
			0.25: 1,
			0.5:  1,
			1:    1,
			2.5:  1,
			5:    1,
			10:   1,
		})
		err = promtest.GatherAndCompare(pr, strings.NewReader(`
# HELP grpc_client_handled_total Total number of RPCs completed by the client, regardless of success or failure.
# TYPE grpc_client_handled_total counter
grpc_client_handled_total{grpc_code="OK",grpc_method="ServerStreamingEcho",grpc_service="grpc.examples.echo.Echo",grpc_type="server_stream"} 1
# HELP grpc_client_msg_received_total Total number of RPC stream messages received by the client.
# TYPE grpc_client_msg_received_total counter
grpc_client_msg_received_total{grpc_method="ServerStreamingEcho",grpc_service="grpc.examples.echo.Echo",grpc_type="server_stream"} 3
# HELP grpc_client_msg_sent_total Total number of gRPC stream messages sent by the client.
# TYPE grpc_client_msg_sent_total counter
grpc_client_msg_sent_total{grpc_method="ServerStreamingEcho",grpc_service="grpc.examples.echo.Echo",grpc_type="server_stream"} 1
# HELP grpc_client_started_total Total number of RPCs started on the client.
# TYPE grpc_client_started_total counter
grpc_client_started_total{grpc_method="ServerStreamingEcho",grpc_service="grpc.examples.echo.Echo",grpc_type="server_stream"} 1
`), counterMetrics("client")...)
		require.NoError(t, err)
	})
}
