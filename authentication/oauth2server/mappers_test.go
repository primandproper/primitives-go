package oauth2server_test

import (
	"testing"

	"github.com/primandproper/primitives-go/v2/authentication/oauth2server"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	grpcerrors "github.com/primandproper/primitives-go/v2/errors/grpc"
	httperrors "github.com/primandproper/primitives-go/v2/errors/http"

	"github.com/shoenig/test"
	"google.golang.org/grpc/codes"
)

func TestMappers(T *testing.T) {
	T.Parallel()

	// One table for both transports, so a sentinel added to one mapper and not
	// the other fails here rather than reaching a client as E100 on one side and
	// codes.Unknown on the other.
	cases := []struct {
		err  error
		name string
		http httperrors.ErrorCode
		grpc codes.Code
	}{
		{name: "no bearer token", err: oauth2server.ErrNoBearerToken, http: httperrors.ErrAuthenticationFailed, grpc: codes.Unauthenticated},
		{name: "not found", err: oauth2server.ErrNotFound, http: httperrors.ErrAuthenticationFailed, grpc: codes.Unauthenticated},
		{name: "expired", err: oauth2server.ErrExpired, http: httperrors.ErrAuthenticationFailed, grpc: codes.Unauthenticated},
		{name: "audience mismatch", err: oauth2server.ErrTokenAudienceMismatch, http: httperrors.ErrAuthenticationFailed, grpc: codes.Unauthenticated},
		{name: "insufficient scope", err: oauth2server.ErrInsufficientScope, http: httperrors.ErrUserIsNotAuthorized, grpc: codes.PermissionDenied},
	}

	for _, tc := range cases {
		T.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Handlers wrap, so a mapping that only works on a bare sentinel
			// works nowhere real.
			for _, err := range []error{tc.err, platformerrors.Wrap(tc.err, "verifying")} {
				code, msg, ok := oauth2server.HTTPMapper.Map(err)
				test.True(t, ok)
				test.EqOp(t, tc.http, code)
				test.NotEq(t, "", msg)

				grpcCode, grpcOK := oauth2server.GRPCMapper.Map(err)
				test.True(t, grpcOK)
				test.EqOp(t, tc.grpc, grpcCode)
			}
		})
	}

	T.Run("statuses", func(t *testing.T) {
		t.Parallel()

		test.EqOp(t, 401, httperrors.HTTPStatusForCode(httperrors.ErrAuthenticationFailed))
		test.EqOp(t, 403, httperrors.HTTPStatusForCode(httperrors.ErrUserIsNotAuthorized))
	})

	T.Run("leaves other errors unclaimed", func(t *testing.T) {
		t.Parallel()

		stranger := platformerrors.New("something nobody has a mapping for")

		_, _, ok := oauth2server.HTTPMapper.Map(stranger)
		test.False(t, ok)

		code, ok := oauth2server.GRPCMapper.Map(stranger)
		test.False(t, ok)
		test.EqOp(t, codes.Unknown, code)

		_, _, ok = oauth2server.HTTPMapper.Map(nil)
		test.False(t, ok)

		_, ok = oauth2server.GRPCMapper.Map(nil)
		test.False(t, ok)
	})

	T.Run("no platform mapping answers first", func(t *testing.T) {
		t.Parallel()

		// Both transports ask PlatformMapper before any registered mapper, so a
		// sentinel it claimed would never reach these. ErrNoBearerToken used to
		// wrap ErrEmptyInputParameter and was answered as a 400.
		for _, tc := range cases {
			_, _, ok := httperrors.PlatformMapper.Map(tc.err)
			test.False(t, ok, test.Sprintf("errors/http claims %v", tc.err))

			_, ok = grpcerrors.PlatformMapper.Map(tc.err)
			test.False(t, ok, test.Sprintf("errors/grpc claims %v", tc.err))
		}
	})
}

func TestRefusalDescription(T *testing.T) {
	T.Parallel()

	T.Run("describes every refusal", func(t *testing.T) {
		t.Parallel()

		for _, err := range []error{
			oauth2server.ErrNoBearerToken,
			oauth2server.ErrNotFound,
			oauth2server.ErrTokenAudienceMismatch,
			oauth2server.ErrInsufficientScope,
		} {
			description, ok := oauth2server.RefusalDescription(err)
			test.True(t, ok)
			test.NotEq(t, "", description)
		}
	})

	T.Run("describes nothing else", func(t *testing.T) {
		t.Parallel()

		_, ok := oauth2server.RefusalDescription(platformerrors.New("store is down"))
		test.False(t, ok)

		_, ok = oauth2server.RefusalDescription(nil)
		test.False(t, ok)
	})
}
