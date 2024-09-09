# promgrpc

simple prometheus metrics for gRPC-go that observes from the `stats.Handler` point in the gRPC stack.

## exported metrics

Label explanations:

* `grpc_type`: one of `unary`, `server_stream`, `client_stream`, `bidi_stream`
* `grpc_service`: given full method name of `/grpc.examples.echo.Echo/UnaryEcho`, this will be set to `grpc.examples.echo.Echo`
* `grpc_method`: given full method name of `/grpc.examples.echo.Echo/UnaryEcho`, this will be set to `UnaryEcho`
* `grpc_code`: a grpc code in title case, eg. `OK`, `PermissionDenied`

### server metrics

* `grpc_server_started_total`: Total number of RPCs started on the server. Observed when the RPC first begins.
  * Labels: `grpc_type`, `grpc_service`, `grpc_method`
* `grpc_server_handled_total`: Total number of RPCs completed on the server, regardless of success or failure.
  * Labels: `grpc_type`, `grpc_service`, `grpc_method`, `grpc_code`
* `grpc_server_msg_received_total`: Total number of RPC stream messages received on the server. Only observes +1 on non-stream server input, +N, where N>=0 on streaming server input.
  * Labels: `grpc_type`, `grpc_service`, `grpc_method`
* `grpc_server_msg_sent_total`: Total number of gRPC stream messages sent by the server. Only observes +1 on non-stream server output, +N, where N>=0 on streaming server output.
  * Labels: `grpc_type`, `grpc_service`, `grpc_method`
* `grpc_server_handling_seconds`: Histogram of response latency (seconds) of gRPC that had been application-level handled by the server.
  * Labels: `grpc_type`, `grpc_service`, `grpc_method`

### client metrics

* `grpc_client_started_total`: Total number of RPCs started on the client. Observed when the RPC first begins.
  * Labels: `grpc_type`, `grpc_service`, `grpc_method`
* `grpc_client_handled_total`: Total number of RPCs completed by the client, regardless of success or failure.
  * Labels: `grpc_type`, `grpc_service`, `grpc_method`, `grpc_code`
* `grpc_client_msg_received_total`: Total number of RPC stream messages received by the client. Only observes +1 on non-stream server output, +N, where N>=0 on streaming server output.
  * Labels: `grpc_type`, `grpc_service`, `grpc_method`
* `grpc_client_msg_sent_total`: Total number of gRPC stream messages sent by the client. Only observes +1 on non-stream client input, +N, where N>=0 on streaming client input.
  * Labels: `grpc_type`, `grpc_service`, `grpc_method`
* `grpc_client_handling_seconds`: Histogram of response latency (seconds) of the gRPC until it is finished by the application.
  * Labels: `grpc_type`, `grpc_service`, `grpc_method`



## license 

MIT License: Copyright (c) 2024 Nicholas Comer

