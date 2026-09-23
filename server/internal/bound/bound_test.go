package bound

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func TestAddress(T *testing.T) {
	T.Parallel()

	T.Run("the zero value waits and then reports what was settled", func(t *testing.T) {
		t.Parallel()

		var a Address
		want := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 4321}

		go a.Settle(want, nil)

		got, err := a.Wait(t.Context())
		must.NoError(t, err)
		test.Eq[net.Addr](t, want, got)
	})

	T.Run("only the first settlement counts", func(t *testing.T) {
		t.Parallel()

		var a Address
		first := &net.TCPAddr{Port: 1}

		a.Settle(first, nil)
		a.Settle(&net.TCPAddr{Port: 2}, errors.New("second"))

		got, err := a.Wait(t.Context())
		must.NoError(t, err)
		test.Eq[net.Addr](t, first, got)
	})

	T.Run("a failure is reported rather than waited out", func(t *testing.T) {
		t.Parallel()

		var a Address
		bindErr := errors.New("address already in use")

		a.Settle(nil, bindErr)

		got, err := a.Wait(t.Context())
		test.Nil(t, got)
		test.ErrorIs(t, err, bindErr)
	})

	T.Run("an unsettled wait ends with its context", func(t *testing.T) {
		t.Parallel()

		var a Address

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		got, err := a.Wait(ctx)
		test.Nil(t, got)
		test.ErrorIs(t, err, context.Canceled)
	})
}
