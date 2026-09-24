// Package bound records the address a server's listener bound, for the callers
// that asked for an ephemeral port and need to know which one they were given.
//
// Both servers read Port 0 as "let the OS choose", and until this existed
// neither could say what it chose: the listener was opened inside Serve and
// never left it. The only way to dial a server started that way was to bind
// :0 elsewhere, read the port, close it, and configure the server with the
// number — a race between the close and the re-bind that a parallel test run
// loses often enough to be read as something else entirely.
//
// It is one type used by both servers because the part that can be got wrong
// is the same in each: a wait that must end whether the bind succeeded or
// failed, and a zero value that must work, since tests build both servers as
// struct literals.
package bound

import (
	"context"
	"net"
	"sync"

	"github.com/primandproper/primitives-go/v2/errors"
)

// ErrNotBound is what a wait reports when the server settled without either an
// address or an error: it returned before binding and had nothing to say why.
var ErrNotBound = errors.New("server returned without binding a listener")

// Address is the outcome of a server's first bind.
//
// The zero value is ready to use.
type Address struct {
	addr    net.Addr
	err     error
	done    chan struct{}
	initOne sync.Once
	settled sync.Once
}

func (a *Address) ch() chan struct{} {
	a.initOne.Do(func() { a.done = make(chan struct{}) })

	return a.done
}

// Settle records the outcome of a bind. Only the first call counts: a server
// that is served twice answers with the first listener, which is the one a
// caller that waited was waiting for.
//
// A nil addr with a nil err is recorded as ErrNotBound, so a server can settle
// from a deferred call on every exit and a waiter never gets two nils back.
func (a *Address) Settle(addr net.Addr, err error) {
	if addr == nil && err == nil {
		err = ErrNotBound
	}

	a.settled.Do(func() {
		a.addr, a.err = addr, err
		close(a.ch())
	})
}

// Wait blocks until the bind has an outcome, and returns it, or until ctx is
// done, and returns ctx's error.
//
// A failed bind ends the wait with that failure rather than leaving the caller
// to its deadline, because "the port was taken" and "the server has not got
// there yet" are different answers and only one of them is worth waiting out.
func (a *Address) Wait(ctx context.Context) (net.Addr, error) {
	select {
	case <-a.ch():
		return a.addr, a.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
