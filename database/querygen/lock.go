package querygen

import (
	"fmt"

	platformerrors "github.com/primandproper/primitives-go/v2/errors"
)

// ErrUnknownLockMode indicates a [LockMode] outside the declared set.
//
// It is a panic rather than a statement rendered without a lock, because an
// unlocked read is the one outcome a caller who asked for a lock can never be
// handed quietly: the statement would parse, run, and give up the mutual
// exclusion the rest of the transaction was written against.
var ErrUnknownLockMode = platformerrors.New("unknown lock mode")

// LockMode is the row lock a read takes on the rows it returns.
//
// It is a field on [Read] and [Sweep] rather than text a caller appends to a
// rendered statement, and that is the whole of what it is for. The suffix is
// small and it differs by dialect — which servers take it at all, where it goes
// relative to the terminator, whether SKIP LOCKED is on offer — so every store
// that appended its own came to answer those questions its own way, and nothing
// compared the answers. Rendered here, the lock is one decision made once.
//
// The zero value is [LockNone], which is what every read wanted before this
// existed.
//
// # On a dialect without row locks
//
// Every mode renders nothing on a dialect that has no row locks, which of the
// supported three is SQLite. That is not a missing lock but the correct
// statement: one writer at a time is SQLite's storage model, so the transaction
// a lock would hold off cannot be running, and a write the caller makes after
// the read is already serialized behind this one.
//
// # On a dialect without SKIP LOCKED
//
// [LockExclusiveSkipLocked] on a dialect that locks rows but cannot skip them
// renders a plain FOR UPDATE. The mutual exclusion is the property a caller is
// relying on for correctness; skipping is the one it relies on for throughput,
// and a claim that waits for a held row is slower than one that steps past it,
// where one that takes no lock is wrong. None of the supported dialects reaches
// this — Postgres and MySQL both skip — so it is the rule for whichever arrives
// next, pinned in lockClause's tests rather than left to be rediscovered.
type LockMode int

const (
	// LockNone takes no lock: the read is a plain SELECT.
	LockNone LockMode = iota
	// LockExclusive takes a write lock on every row returned, FOR UPDATE, so
	// no other transaction may lock, update or delete them until this one ends.
	//
	// It is the read-then-write lock: the row a transaction is about to replace
	// on the strength of what it just read.
	LockExclusive
	// LockShared takes a read lock on every row returned, FOR SHARE, so no other
	// transaction may update or delete them until this one ends, though others
	// may share the lock.
	//
	// It is for the row a transaction depends on without writing: the parent a
	// child is being inserted under, which must not be removed in the meantime.
	LockShared
	// LockExclusiveSkipLocked takes [LockExclusive]'s lock on every row it can
	// and leaves out the rows another transaction already holds, FOR UPDATE
	// SKIP LOCKED.
	//
	// It is the claim a pool of workers makes on one queue: each takes a batch
	// nobody else has, rather than all of them queueing behind whichever took
	// the first. The rows it leaves out are not an error — they are the rows
	// somebody else is working.
	LockExclusiveSkipLocked
)

// String names the mode, for the panic messages the misuse checks raise.
func (m LockMode) String() string {
	switch m {
	case LockNone:
		return "no lock"
	case LockExclusive:
		return "exclusive"
	case LockShared:
		return "shared"
	case LockExclusiveSkipLocked:
		return "exclusive, skipping locked rows"
	default:
		return fmt.Sprintf("unknown lock mode %d", int(m))
	}
}

// lockClause renders the lock a read takes on this generator's dialect, with
// the newline that sets it on a line of its own, or nothing at all.
//
// It is appended after everything else in the SELECT — after the ORDER BY and
// the LIMIT, which is where all of Postgres, MySQL 8 and the SQL standard put a
// locking clause — and before the terminator, so a caller never handles the
// rendered text to place it.
func (g *Generator) lockClause(mode LockMode) string {
	return lockClause(mode, g.dialect.SupportsRowLocking(), g.dialect.SupportsSkipLocked())
}

// lockClause is Generator.lockClause over the two capabilities rather than the
// dialect, so that the degradations — no row locks, and row locks without SKIP
// LOCKED — can be asserted on combinations no supported dialect has yet.
//
// FOR SHARE is spelled the same on both servers that lock. MySQL's older LOCK IN
// SHARE MODE still parses there, but dialect.MySQL targets 8.0+, where FOR SHARE
// is the documented spelling and the one that composes with SKIP LOCKED.
func lockClause(mode LockMode, rowLocking, skipLocked bool) string {
	var clause string

	switch mode {
	case LockNone:
		return ""
	case LockExclusive:
		clause = "FOR UPDATE"
	case LockShared:
		clause = "FOR SHARE"
	case LockExclusiveSkipLocked:
		clause = "FOR UPDATE"
		if skipLocked {
			clause += " SKIP LOCKED"
		}
	default:
		panic(platformerrors.Wrapf(ErrUnknownLockMode, "querygen: %s", mode))
	}

	if !rowLocking {
		return ""
	}

	return "\n" + clause
}
