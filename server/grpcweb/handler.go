package grpcweb

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/primandproper/primitives-go/v2/observability/logging"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

const (
	loggerName = "grpcweb"

	// trailerFrameFlag marks the frame carrying the trailers, which gRPC-web
	// moves into the response body because fetch cannot read HTTP trailers.
	trailerFrameFlag byte = 0x80

	// frameHeaderSize is the flag byte and the four-byte big-endian length that
	// precede every gRPC frame.
	frameHeaderSize = 5
)

type (
	// Server is the gRPC server a Handler translates for. *grpcserver.Server
	// satisfies it.
	//
	// It is narrower than an http.Handler on purpose: GetServiceInfo is how the
	// Handler tells a streaming method from a unary one, and a plain handler
	// cannot say.
	Server interface {
		http.Handler
		GetServiceInfo() map[string]grpc.ServiceInfo
	}

	handler struct {
		next      http.Handler
		logger    logging.Logger
		streaming map[string]struct{}
	}
)

// NewHandler serves gRPC-web from next, translating each call into one next
// serves as native gRPC and its response back.
//
// The streaming methods are read from next once, here, so every service must
// already be registered on it.
func NewHandler(next Server, opts ...Option) http.Handler {
	o := newOptions(opts)

	streaming := map[string]struct{}{}
	services := next.GetServiceInfo()
	for service := range services {
		for _, method := range services[service].Methods {
			if method.IsClientStream || method.IsServerStream {
				streaming["/"+service+"/"+method.Name] = struct{}{}
			}
		}
	}

	return &handler{
		next:      next,
		logger:    logging.NewNamedLogger(o.logger, loggerName),
		streaming: streaming,
	}
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, fmt.Sprintf("gRPC-web requires POST, not %s", r.Method), http.StatusMethodNotAllowed)

		return
	}

	webContentType, grpcContentType, ok := negotiate(r.Header.Get("Content-Type"))
	if !ok {
		http.Error(w, fmt.Sprintf("unsupported gRPC-web content type %q", r.Header.Get("Content-Type")), http.StatusUnsupportedMediaType)

		return
	}

	next := h.next
	if _, streaming := h.streaming[r.URL.Path]; streaming {
		next = trailersOnly(codes.Unimplemented, "gRPC-web serves unary methods only")
	}

	// grpc-go's ServeHTTP refuses anything but HTTP/2. The body it reads is the
	// same length-prefixed message on any version, so the version is only a
	// claim, and this is where it is made.
	req := r.Clone(r.Context())
	req.Proto, req.ProtoMajor, req.ProtoMinor = "HTTP/2.0", 2, 0
	req.Header.Set("Content-Type", grpcContentType)

	rw := &responseWriter{w: w, header: http.Header{}, contentType: webContentType}
	next.ServeHTTP(rw, req)

	if err := rw.finish(); err != nil {
		// The client has usually gone away. The call itself completed and was
		// logged by the server's interceptors; only its status went undelivered.
		h.logger.WithError(err).WithValue("rpc.method", r.URL.Path).Debug("writing gRPC-web trailers")
	}
}

// negotiate maps a gRPC-web request content type to the one grpc-go accepts.
// Only the binary encodings are served: grpc-web-text, the base64 encoding, is
// for clients that cannot read a binary body, and none of ours is one.
func negotiate(contentType string) (webContentType, grpcContentType string, ok bool) {
	mediaType, _, _ := strings.Cut(contentType, ";")

	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "application/grpc-web":
		return "application/grpc-web", "application/grpc", true
	case "application/grpc-web+proto":
		return "application/grpc-web+proto", "application/grpc+proto", true
	default:
		return "", "", false
	}
}

// trailersOnly answers a call with a status and no message, as gRPC does when a
// call fails before it produces one.
func trailersOnly(code codes.Code, message string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hdr := w.Header()
		hdr.Set("Grpc-Status", strconv.Itoa(int(code)))
		hdr.Set("Grpc-Message", message)
		w.WriteHeader(http.StatusOK)
	})
}

// responseWriter stands between grpc-go and the client. It passes the headers
// and message frames through, and holds back the trailers grpc-go writes so they
// can be sent as the body's final frame.
//
// grpc-go writes to it from a single goroutine, the one serving the request, so
// it needs no locking.
type responseWriter struct {
	w           http.ResponseWriter
	header      http.Header
	err         error
	contentType string
	status      int
	committed   bool
}

func (rw *responseWriter) Header() http.Header {
	return rw.header
}

// WriteHeader passes a status other than 200 straight through. gRPC answers
// every call 200 and carries its outcome in grpc-status, so anything else is
// grpc-go refusing the request as HTTP, with a plain-text body explaining why.
func (rw *responseWriter) WriteHeader(code int) {
	if rw.status != 0 {
		return
	}

	rw.status = code
	if code == http.StatusOK {
		return
	}

	maps.Copy(rw.w.Header(), rw.header)
	rw.w.WriteHeader(code)
	rw.committed = true
}

func (rw *responseWriter) Write(p []byte) (int, error) {
	if rw.status == 0 {
		rw.WriteHeader(http.StatusOK)
	}

	if !rw.committed {
		rw.commit(nil)
	}

	n, err := rw.w.Write(p)
	if err != nil && rw.err == nil {
		rw.err = err
	}

	return n, err
}

// Unwrap hands http.NewResponseController the client's ResponseWriter. The
// server's forced Shutdown reaches through it to cut short a call whose client
// stalled mid-body, which it cannot do through this wrapper.
func (rw *responseWriter) Unwrap() http.ResponseWriter {
	return rw.w
}

// Flush is a no-op until a message has been written. grpc-go flushes before it
// sets the status of a call that failed without one, and passing that through
// would send the headers before the status could be among them.
func (rw *responseWriter) Flush() {
	if !rw.committed {
		return
	}

	if err := http.NewResponseController(rw.w).Flush(); err != nil && rw.err == nil {
		rw.err = err
	}
}

// commit sends the response headers: everything grpc-go set except its
// trailers, plus the trailers given, which a trailers-only response carries as
// headers as well.
func (rw *responseWriter) commit(trailers http.Header) {
	rw.committed = true

	held := rw.trailerNames()
	dst := rw.w.Header()
	for k, vv := range rw.header {
		if _, isTrailer := held[k]; isTrailer || k == "Trailer" || strings.HasPrefix(k, http.TrailerPrefix) {
			continue
		}

		dst[k] = slices.Clone(vv)
	}

	for k, vv := range trailers {
		dst[k] = slices.Clone(vv)
	}

	dst.Set("Content-Type", rw.contentType)
	rw.w.WriteHeader(http.StatusOK)
}

// finish writes the trailer frame, committing the headers first when no message
// was written, which makes this a trailers-only response.
func (rw *responseWriter) finish() error {
	if rw.status != 0 && rw.status != http.StatusOK {
		return nil
	}

	trailers := rw.trailers()
	if trailers.Get("Grpc-Status") == "" {
		// grpc-go returns without a status when the server is stopped under
		// the call, which a gRPC client would see as a reset stream. A gRPC-web
		// client has no stream to see reset, so the status says it instead.
		trailers.Set("Grpc-Status", strconv.Itoa(int(codes.Unavailable)))
		trailers.Set("Grpc-Message", "the server stopped before the call finished")
	}

	if !rw.committed {
		rw.commit(trailers)
	}

	if _, err := rw.w.Write(encodeTrailerFrame(trailers)); err != nil && rw.err == nil {
		rw.err = err
	}

	return rw.err
}

// trailerNames is the set of header keys that are trailers: the status fields
// gRPC always sends as trailers, and any name declared in a Trailer header.
func (rw *responseWriter) trailerNames() map[string]struct{} {
	names := map[string]struct{}{
		"Grpc-Status":             {},
		"Grpc-Message":            {},
		"Grpc-Status-Details-Bin": {},
	}

	for _, declared := range rw.header.Values("Trailer") {
		for name := range strings.SplitSeq(declared, ",") {
			names[http.CanonicalHeaderKey(strings.TrimSpace(name))] = struct{}{}
		}
	}

	return names
}

// trailers collects the trailers grpc-go wrote, both the declared ones and
// those it set undeclared under http.TrailerPrefix. Their values are passed
// through as written: grpc-message stays percent-encoded and
// grpc-status-details-bin stays base64.
func (rw *responseWriter) trailers() http.Header {
	names := rw.trailerNames()

	trailers := http.Header{}
	for k, vv := range rw.header {
		if name, undeclared := strings.CutPrefix(k, http.TrailerPrefix); undeclared {
			trailers[http.CanonicalHeaderKey(name)] = append(trailers[http.CanonicalHeaderKey(name)], vv...)
			continue
		}

		if _, ok := names[k]; ok {
			trailers[k] = append(trailers[k], vv...)
		}
	}

	return trailers
}

// encodeTrailerFrame lays trailers out as gRPC-web's final frame: the 0x80 flag,
// a four-byte big-endian length, then the trailers as an HTTP/1 header block
// with lowercase names.
func encodeTrailerFrame(trailers http.Header) []byte {
	var block bytes.Buffer
	for _, k := range slices.Sorted(maps.Keys(trailers)) {
		name := strings.ToLower(k)
		for _, v := range trailers[k] {
			block.WriteString(name)
			block.WriteString(": ")
			block.WriteString(v)
			block.WriteString("\r\n")
		}
	}

	frame := make([]byte, frameHeaderSize, frameHeaderSize+block.Len())
	frame[0] = trailerFrameFlag
	binary.BigEndian.PutUint32(frame[1:], uint32(block.Len()))

	return append(frame, block.Bytes()...)
}
