/*
Package tierguard holds the one rule this module cannot state in Go's type
system, and it holds nothing else: nothing here imports platform-go.

The split that produced this module bought exactly one thing, and it is a
direction. platform-go imports primitives-go; primitives-go imports platform-go
from nowhere, ever. A primitive that finds it needs a domain package has found a
seam to invert — the domain package exports the value and platform-go's service
registers it, which is how the error mappers work — rather than a dependency to
add.

Go will not catch a violation on its own. An import of platform-go compiles
here perfectly well; what it does is make the two modules require each other,
which go will resolve against whatever platform-go version is published rather
than the one in the tree, and which turns every platform-go major into a
primitives-go major. That is the whole cost the split was paid to avoid, and it
would arrive as a passing build.

platform-go has the mirror of this in its own internal/tiercheck, and its
package doc records why an enumeration beat a convention there: two hand audits
of the crossings were published as complete and neither was. This side is the
easier half — there is no roster to keep, because the answer is the same for
every package in the module — so it is one walk of the tree and one string.

Test files count, and so does go.mod. A test that imports platform-go is
compiled by this module exactly as production code is, and a require line with
no import behind it is a version constraint this module has no business
carrying.
*/
package tierguard
