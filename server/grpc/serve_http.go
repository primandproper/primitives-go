package grpc

import (
	"net/http"
	"strconv"
	"sync"
	"time"

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
//
// A Shutdown that is forced unblocks the call's read of its request body, which
// nothing else would: grpc-go does not return from a call until that read does,
// and a client that stalls mid-body leaves it pending for as long as the
// connection stays open. That needs w to reach the server's own ResponseWriter
// through http.NewResponseController, so a wrapper between the two has to
// provide Unwrap.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	if !s.httpCalls.begin(rc) {
		h := w.Header()
		h.Set("Content-Type", "application/grpc")
		h.Set("Grpc-Status", strconv.Itoa(int(codes.Unavailable)))
		h.Set("Grpc-Message", "server is shutting down")
		w.WriteHeader(http.StatusOK)

		return
	}
	defer s.httpCalls.end(rc)

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
	inFlight map[*http.ResponseController]struct{}
	wg       sync.WaitGroup
	mu       sync.Mutex
	draining bool
}

// begin admits a call, unless a drain has started.
func (c *httpCalls) begin(rc *http.ResponseController) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.draining {
		return false
	}

	if c.inFlight == nil {
		c.inFlight = map[*http.ResponseController]struct{}{}
	}
	c.inFlight[rc] = struct{}{}
	c.wg.Add(1)

	return true
}

// end retires a call. It runs before ServeHTTP returns, which is what makes
// abort's use of the call's ResponseController safe: net/http forbids it once
// the handler has returned, and abort holds the lock end needs.
func (c *httpCalls) end(rc *http.ResponseController) {
	c.mu.Lock()
	delete(c.inFlight, rc)
	c.mu.Unlock()

	c.wg.Done()
}

// abort unblocks the request-body read of every call still in flight, by
// moving its read deadline to now. It works on HTTP/1.1 and HTTP/2 alike, and
// reports the calls it could not reach — those whose ResponseWriter does not
// unwrap to the server's.
func (c *httpCalls) abort() (unreached int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for rc := range c.inFlight {
		if err := rc.SetReadDeadline(now); err != nil {
			unreached++
		}
	}

	return unreached
}

// drain refuses new calls and waits for the admitted ones to finish.
func (c *httpCalls) drain() {
	c.mu.Lock()
	c.draining = true
	c.mu.Unlock()

	c.wg.Wait()
}
