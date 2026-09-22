package grpc

import (
	"context"
	stderrors "errors"
	"net"
	"testing"

	platformerrors "github.com/primandproper/primitives-go/v2/errors"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// The worked example throughout is the one the feature was asked for: two
// refusals that share codes.Unauthenticated and differ only in what the client
// should do next. Without a reason a client tells them apart by matching the
// English sentence, which is the thing this exists to stop.
var (
	errSecondFactorRequired = platformerrors.New("a second-factor code is required")
	errInvalidCredentials   = platformerrors.New("those credentials are not valid")
)

func TestClientSafeReason(T *testing.T) {
	T.Parallel()

	RegisterClientSafeReasons(
		ClientReason{Err: errSecondFactorRequired, Reason: "SECOND_FACTOR_REQUIRED", Domain: "signin.example.com"},
		ClientReason{Err: errInvalidCredentials, Reason: "INVALID_CREDENTIALS", Domain: "signin.example.com"},
	)

	T.Run("a registered sentinel answers with its reason", func(t *testing.T) {
		t.Parallel()

		// Wrapped, because that is how one arrives from a handler.
		reason, ok := ClientSafeReason(platformerrors.Wrap(errSecondFactorRequired, "checking the second factor"))
		must.True(t, ok)
		test.EqOp(t, "SECOND_FACTOR_REQUIRED", reason.Reason)
		test.EqOp(t, "signin.example.com", reason.Domain)
	})

	T.Run("the two refusals are distinguishable", func(t *testing.T) {
		t.Parallel()

		// The whole point: same code, same shape, different identifier, and no
		// client anywhere has to read the sentence to know which it got.
		first, ok := ClientSafeReason(errSecondFactorRequired)
		must.True(t, ok)
		second, ok := ClientSafeReason(errInvalidCredentials)
		must.True(t, ok)

		test.NotEqOp(t, first.Reason, second.Reason)
	})

	T.Run("an unregistered sentinel has no reason", func(t *testing.T) {
		t.Parallel()

		reason, ok := ClientSafeReason(platformerrors.New("update users set x = 1"))
		test.False(t, ok)
		test.EqOp(t, "", reason.Reason)
	})

	T.Run("nil is nothing to report", func(t *testing.T) {
		t.Parallel()

		_, ok := ClientSafeReason(nil)
		test.False(t, ok)
	})

	T.Run("an entry missing either half is ignored", func(t *testing.T) {
		t.Parallel()

		reasonless := platformerrors.New("a sentinel registered without a reason")
		RegisterClientSafeReasons(
			ClientReason{Err: reasonless, Reason: ""},
			ClientReason{Err: nil, Reason: "NO_SENTINEL"},
		)

		// Neither half can answer anything on its own, so neither is consulted
		// — and in particular the nil-Err entry does not panic the walk.
		_, ok := ClientSafeReason(reasonless)
		test.False(t, ok)
	})
}

// TestClientSafeReason_outermostNodeWins pins the reason lookup to the same
// ordering rule ClientSafeMessage documents. The two channels describe one
// refusal, so a chain that resolves to the specific sentinel for the message and
// the general one for the reason would be telling a client two different things
// about the same response.
func TestClientSafeReason_outermostNodeWins(T *testing.T) {
	T.Parallel()

	inner := platformerrors.New("this account cannot sign in this way")
	outer := platformerrors.Wrap(inner, "this account is locked")

	RegisterClientSafeReasons(
		ClientReason{Err: inner, Reason: "SIGNIN_METHOD_REFUSED"},
		ClientReason{Err: outer, Reason: "ACCOUNT_LOCKED"},
	)

	T.Run("the registered wrapper outranks the registered sentinel inside it", func(t *testing.T) {
		t.Parallel()

		reason, ok := ClientSafeReason(platformerrors.Wrap(outer, "signing in"))
		must.True(t, ok)
		test.EqOp(t, "ACCOUNT_LOCKED", reason.Reason)

		// And std errors.Is still sees both, so nothing about matching moved.
		test.ErrorIs(t, platformerrors.Wrap(outer, "signing in"), inner)
	})

	T.Run("a join is walked depth-first in join order", func(t *testing.T) {
		t.Parallel()

		joined := platformerrors.Join(
			platformerrors.Wrap(stderrors.New("no reason at any depth"), "first branch"),
			platformerrors.Wrap(inner, "second branch"),
		)

		reason, ok := ClientSafeReason(joined)
		must.True(t, ok)
		test.EqOp(t, "SIGNIN_METHOD_REFUSED", reason.Reason)
	})
}

// TestRegisterClientSafeReasons_registersTheMessageToo pins the documented
// coupling: a reason and its sentinel's words are two halves of one statement
// about one refusal, and a caller that registers the identifier should not have
// to remember a second call for the prose — that is exactly how the two drift.
func TestRegisterClientSafeReasons_registersTheMessageToo(t *testing.T) {
	t.Parallel()

	sentinel := platformerrors.New("this session has been signed out elsewhere")
	RegisterClientSafeReasons(ClientReason{Err: sentinel, Reason: "SESSION_REVOKED"})

	msg, ok := ClientSafeMessage(platformerrors.Wrap(sentinel, "loading the session"))
	must.True(t, ok)
	test.EqOp(t, sentinel.Error(), msg)
}

func TestClientReason_Detail(t *testing.T) {
	t.Parallel()

	detail := ClientReason{Reason: "SECOND_FACTOR_REQUIRED", Domain: "signin.example.com"}.Detail()

	must.NotNil(t, detail)
	test.EqOp(t, "SECOND_FACTOR_REQUIRED", detail.GetReason())
	test.EqOp(t, "signin.example.com", detail.GetDomain())
}

func TestClientReasonFromStatus(T *testing.T) {
	T.Parallel()

	T.Run("reads the detail a server attached", func(t *testing.T) {
		t.Parallel()

		st, err := status.New(codes.Unauthenticated, "a second-factor code is required").
			WithDetails(&errdetails.ErrorInfo{Reason: "SECOND_FACTOR_REQUIRED", Domain: "signin.example.com"})
		must.NoError(t, err)

		info, ok := ClientReasonFromStatus(st.Err())
		must.True(t, ok)
		test.EqOp(t, "SECOND_FACTOR_REQUIRED", info.GetReason())
		test.EqOp(t, "signin.example.com", info.GetDomain())
	})

	T.Run("a status with no reason detail says so", func(t *testing.T) {
		t.Parallel()

		_, ok := ClientReasonFromStatus(status.New(codes.Unauthenticated, "Unauthenticated").Err())
		test.False(t, ok)
	})

	T.Run("a non-status error and nil say so", func(t *testing.T) {
		t.Parallel()

		_, ok := ClientReasonFromStatus(stderrors.New("plain error"))
		test.False(t, ok)

		_, ok = ClientReasonFromStatus(nil)
		test.False(t, ok)
	})
}

func TestStripEncodedErrorDetail(T *testing.T) {
	T.Parallel()

	sentinel := platformerrors.New("a widget refusal worth naming")
	RegisterClientSafeReasons(ClientReason{Err: sentinel, Reason: "WIDGET_REFUSED"})

	// The status an interceptor builds: both details, for two different readers.
	fromTheStore := platformerrors.Wrap(sentinel, "scanning widget_catalog_entries")
	full := statusWithDetails(context.Background(), codes.FailedPrecondition, "fetching the widget", fromTheStore)
	must.SliceLen(T, 2, full.Proto().GetDetails())

	T.Run("the client detail survives and the peer detail does not", func(t *testing.T) {
		t.Parallel()

		stripped := StripEncodedErrorDetail(full.Err())
		must.Error(t, stripped)

		info, ok := ClientReasonFromStatus(stripped)
		must.True(t, ok)
		test.EqOp(t, "WIDGET_REFUSED", info.GetReason())

		// The chain is gone, which is the whole job: the table name the message
		// channel refused to carry is no longer beside it either.
		test.False(t, platformerrors.Is(DecodeErrorFromStatus(t.Context(), stripped), sentinel))
	})

	T.Run("the code and message are untouched", func(t *testing.T) {
		t.Parallel()

		stripped := StripEncodedErrorDetail(full.Err())

		test.EqOp(t, codes.FailedPrecondition, status.Code(stripped))
		test.EqOp(t, "fetching the widget", status.Convert(stripped).Message())
	})

	T.Run("anything with nothing to strip comes back as it was", func(t *testing.T) {
		t.Parallel()

		test.Nil(t, StripEncodedErrorDetail(nil))

		plain := stderrors.New("plain error")
		test.ErrorIs(t, StripEncodedErrorDetail(plain), plain)

		bare := status.New(codes.NotFound, "NotFound").Err()
		test.ErrorIs(t, StripEncodedErrorDetail(bare), bare)
	})
}

func TestErrorEncodingInterceptors_attachTheReason(T *testing.T) {
	T.Parallel()

	sentinel := platformerrors.New("this upload exceeds the plan's file size")
	RegisterClientSafeReasons(ClientReason{Err: sentinel, Reason: "UPLOAD_TOO_LARGE", Domain: "uploads.example.com"})

	unregistered := platformerrors.New("update users set x = 1")

	T.Run("the unary interceptor attaches it", func(t *testing.T) {
		t.Parallel()

		interceptor := UnaryErrorEncodingInterceptor()
		handler := func(context.Context, any) (any, error) {
			return nil, platformerrors.Wrap(sentinel, "storing the object")
		}

		_, err := interceptor(context.Background(), nil, &grpc.UnaryServerInfo{}, handler)
		must.Error(t, err)

		info, ok := ClientReasonFromStatus(err)
		must.True(t, ok)
		test.EqOp(t, "UPLOAD_TOO_LARGE", info.GetReason())
		test.EqOp(t, "uploads.example.com", info.GetDomain())
	})

	T.Run("the stream interceptor attaches it", func(t *testing.T) {
		t.Parallel()

		interceptor := StreamErrorEncodingInterceptor()
		handler := func(any, grpc.ServerStream) error {
			return platformerrors.Wrap(sentinel, "storing the object")
		}

		ss := &mockServerStream{ctx: context.Background()}
		err := interceptor(nil, ss, &grpc.StreamServerInfo{}, handler)
		must.Error(t, err)

		info, ok := ClientReasonFromStatus(err)
		must.True(t, ok)
		test.EqOp(t, "UPLOAD_TOO_LARGE", info.GetReason())
	})

	T.Run("the detail is a plain google.rpc.ErrorInfo", func(t *testing.T) {
		t.Parallel()

		// The interop claim, and the reason the reason is not just another field
		// on the encoded chain: a client in any language reads a standard detail
		// at a standard type URL, with no cockroachdb decoder anywhere.
		interceptor := UnaryErrorEncodingInterceptor()
		handler := func(context.Context, any) (any, error) {
			return nil, platformerrors.Wrap(sentinel, "storing the object")
		}

		_, err := interceptor(context.Background(), nil, &grpc.UnaryServerInfo{}, handler)
		must.Error(t, err)

		st, ok := status.FromError(err)
		must.True(t, ok)

		var found bool
		for _, detail := range st.Proto().GetDetails() {
			if detail.GetTypeUrl() == "type.googleapis.com/google.rpc.ErrorInfo" {
				found = true
			}
		}
		test.True(t, found)
	})

	T.Run("an error with no registered reason gets no detail", func(t *testing.T) {
		t.Parallel()

		interceptor := UnaryErrorEncodingInterceptor()
		handler := func(context.Context, any) (any, error) {
			return nil, unregistered
		}

		_, err := interceptor(context.Background(), nil, &grpc.UnaryServerInfo{}, handler)
		must.Error(t, err)

		_, ok := ClientReasonFromStatus(err)
		test.False(t, ok)
	})

	T.Run("the encoded chain is still there beside it", func(t *testing.T) {
		t.Parallel()

		interceptor := UnaryErrorEncodingInterceptor()
		handler := func(context.Context, any) (any, error) {
			return nil, platformerrors.Wrap(sentinel, "storing the object")
		}

		_, err := interceptor(context.Background(), nil, &grpc.UnaryServerInfo{}, handler)
		must.Error(t, err)

		// Two channels, not one replacing the other.
		test.True(t, platformerrors.Is(DecodeErrorFromStatus(t.Context(), err), sentinel))
	})
}

// TestClientReason_OverTheWire is the property the feature is actually for, and
// it only exists over a real connection: a client branching on which refusal it
// got, without matching prose and without reading the peer's detail.
func TestClientReason_OverTheWire(T *testing.T) {
	T.Parallel()

	RegisterClientSafeReasons(
		ClientReason{Err: errSecondFactorRequired, Reason: "SECOND_FACTOR_REQUIRED", Domain: "signin.example.com"},
	)

	handlerErr := PrepareAndLogGRPCStatus(
		platformerrors.Wrap(errSecondFactorRequired, "loading the user's enrolled factors"),
		nil, nil, codes.Unauthenticated, "signing in",
	)

	T.Run("a client that decodes nothing still reads the reason", func(t *testing.T) {
		t.Parallel()

		// No decoding interceptor, and — more to the point — a client in a
		// language that has no way to decode the details at all reads the same
		// field out of the same standard detail.
		listener := bufconn.Listen(1 << 20)
		server := grpc.NewServer(grpc.UnaryInterceptor(UnaryErrorEncodingInterceptor()))
		grpc_health_v1.RegisterHealthServer(server, &failingHealthServer{err: handlerErr})

		go func() { _ = server.Serve(listener) }()
		t.Cleanup(server.Stop)

		conn, err := grpc.NewClient("passthrough:///bufnet",
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return listener.DialContext(ctx)
			}),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		must.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })

		_, checkErr := grpc_health_v1.NewHealthClient(conn).Check(t.Context(), &grpc_health_v1.HealthCheckRequest{})
		must.Error(t, checkErr)

		info, ok := ClientReasonFromStatus(checkErr)
		must.True(t, ok)
		test.EqOp(t, "SECOND_FACTOR_REQUIRED", info.GetReason())

		// The code alone could not have told it: the message is the only other
		// thing carrying the distinction, and it is prose.
		test.EqOp(t, codes.Unauthenticated, status.Code(checkErr))
	})

	T.Run("the decoding interceptor does not hide it", func(t *testing.T) {
		t.Parallel()

		client := serveHealth(t, handlerErr)

		_, err := client.Check(t.Context(), &grpc_health_v1.HealthCheckRequest{})
		must.Error(t, err)

		// What the decoding interceptor returns still answers as the status it
		// arrived as, so a caller gets both idioms: the sentinel and the reason.
		test.True(t, stderrors.Is(err, errSecondFactorRequired))

		info, ok := ClientReasonFromStatus(err)
		must.True(t, ok)
		test.EqOp(t, "SECOND_FACTOR_REQUIRED", info.GetReason())
	})

	T.Run("an edge may strip the peer detail and forward the rest", func(t *testing.T) {
		t.Parallel()

		client := serveHealth(t, handlerErr)

		_, err := client.Check(t.Context(), &grpc_health_v1.HealthCheckRequest{})
		must.Error(t, err)

		forwarded := StripEncodedErrorDetail(err)

		info, ok := ClientReasonFromStatus(forwarded)
		must.True(t, ok)
		test.EqOp(t, "SECOND_FACTOR_REQUIRED", info.GetReason())

		// And the sentence the client no longer needs to match is still there
		// for a person to read.
		test.EqOp(t, errSecondFactorRequired.Error(), status.Convert(forwarded).Message())
	})
}
