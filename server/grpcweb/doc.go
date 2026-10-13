/*
Package grpcweb serves gRPC-web, so a browser or React Native client can call a
gRPC service directly rather than through a BFF.

Neither can speak gRPC: fetch exposes no HTTP trailers, and page code cannot
require HTTP/2. gRPC-web is the gRPC project's answer, specified in
doc/PROTOCOL-WEB.md in grpc/grpc: the same paths, messages and status codes,
with the trailers moved into a final frame of the response body. NewHandler
translates it into native gRPC for a server's ServeHTTP, so a call made this way
runs through the same interceptors, stats handler and drain as one that arrived
on the server's listener.

	handler := grpcweb.NewHandler(grpcServer, grpcweb.WithLogger(logger))

Mounting it, and CORS in front of it, are the consumer's.

# What it serves

It is scoped to what a client of ours needs:

  - Unary methods only. A streaming method is answered UNIMPLEMENTED without
    reaching the server, which is why NewHandler takes a Server rather than an
    http.Handler: it reads which methods stream from GetServiceInfo.
  - The binary encodings only, application/grpc-web and
    application/grpc-web+proto. grpc-web-text, and anything else, is answered
    415.
  - Any HTTP version. grpc-go's ServeHTTP insists on HTTP/2, and a request that
    arrived over HTTP/1.1 is presented to it as one, since the length-prefixed
    message in the body is the same on either.

grpc-status, grpc-message and grpc-status-details-bin reach the client byte for
byte, in the trailer frame. A trailers-only response, a call that failed before
producing a message, carries them as headers too, as the protocol requires.

# Why this and not a library

The wire protocol is what clients depend on, and gRPC-web has independent server
implementations (Envoy, tonic-web, ASP.NET Core), so this translator is
replaceable without a client change. vanguard-go was mid-rewrite onto connect-go
v2 when this was written, and improbable-eng/grpc-web has not been pushed to since
2023. grpc-go marks Server.ServeHTTP EXPERIMENTAL; if it is ever removed, Envoy's
gRPC-web filter in front of the native listener is the fallback, and still no
client changes.
*/
package grpcweb

//platform:transport server: gRPC-web in front of the gRPC server, for browser and React Native clients
