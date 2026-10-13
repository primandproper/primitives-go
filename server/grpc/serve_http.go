package grpc

import (
	"net/http"
	"strconv"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

// ServeHTTP serves one gRPC call from an http.Handler stack rather than from the
// listener Serve binds, through grpc-go's Server.ServeHTTP. The call runs through
// the same interceptors, stats handler and message-size bounds as one that
// arrived on the listener, and Shutdown drains it.
//
// grpc-go requires the request to be HTTP/2 with a gRPC content type, and
// answers anything else with a plain HTTP error. Its ServeHTTP is marked
// EXPERIMENTAL; server/grpcweb is the reason this exists, and the gRPC-web wire
// protocol it serves is what clients depend on, so a proxy in front can replace
// this path without a client change.
//
// A call that arrives once Shutdown has begun is answered UNAVAILABLE without
// reaching grpc-go.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.httpCalls.begin() {
		h := w.Header()
		h.Set("Content-Type", "application/grpc")
		h.Set("Grpc-Status", strconv.Itoa(int(codes.Unavailable)))
		h.Set("Grpc-Message", "server is shutting down")
		w.WriteHeader(http.StatusOK)

		return
	}
	defer s.httpCalls.end()

	s.grpcServer.ServeHTTP(w, r)
}

// GetServiceInfo reports the services registered on the server, keyed by
// service name, as grpc-go's Server.GetServiceInfo does. server/grpcweb reads it
// to tell a streaming method from a unary one.
func (s *Server) GetServiceInfo() map[string]grpc.ServiceInfo {
	return s.grpcServer.GetServiceInfo()
}

// httpCalls tracks the calls in flight through ServeHTTP, which grpc-go's
// GracefulStop does not wait for: it closes their transports instead, cancelling
// them mid-call.
type httpCalls struct {
	wg       sync.WaitGroup
	mu       sync.Mutex
	draining bool
}

// begin admits a call, unless a drain has started.
func (c *httpCalls) begin() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.draining {
		return false
	}

	c.wg.Add(1)

	return true
}

func (c *httpCalls) end() {
	c.wg.Done()
}

// drain refuses new calls and waits for the admitted ones to finish.
func (c *httpCalls) drain() {
	c.mu.Lock()
	c.draining = true
	c.mu.Unlock()

	c.wg.Wait()
}
