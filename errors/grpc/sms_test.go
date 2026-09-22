package grpc

import (
	"testing"

	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/sms"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
	"google.golang.org/grpc/codes"
)

func TestSMSMappings(T *testing.T) {
	T.Parallel()

	T.Run("maps an opted-out recipient to FailedPrecondition", func(t *testing.T) {
		t.Parallel()

		// Not PermissionDenied: the caller may send, and it is the recipient who
		// withdrew consent. The state that has to change before a retry can
		// succeed is theirs.
		code, ok := PlatformMapper.Map(sms.ErrRecipientOptedOut)

		must.True(t, ok)
		test.EqOp(t, codes.FailedPrecondition, code)
	})

	T.Run("maps an invalid recipient to InvalidArgument", func(t *testing.T) {
		t.Parallel()

		code, ok := PlatformMapper.Map(sms.ErrInvalidRecipient)

		must.True(t, ok)
		test.EqOp(t, codes.InvalidArgument, code)
	})

	T.Run("maps an unverified recipient to Internal", func(t *testing.T) {
		t.Parallel()

		// The gRPC counterpart of the HTTP mapper's 500, and the same argument: a
		// trial account is this deployment being unfinished.
		code, ok := PlatformMapper.Map(sms.ErrUnverifiedRecipient)

		must.True(t, ok)
		test.EqOp(t, codes.Internal, code)
	})

	T.Run("maps wrapped sentinels too", func(t *testing.T) {
		t.Parallel()

		test.EqOp(t, codes.FailedPrecondition,
			MapToGRPC(platformerrors.Wrapf(sms.ErrRecipientOptedOut, "twilio error %d", 21610), codes.Unknown))
	})
}
