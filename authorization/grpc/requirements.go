package grpc

import (
	"maps"
	"slices"

	"github.com/primandproper/primitives-go/v2/authorization"
	"github.com/primandproper/primitives-go/v2/errors"
)

var (
	// ErrEmptyMethod indicates a requirement was declared for an empty method name.
	ErrEmptyMethod = errors.New("empty method name")
	// ErrDuplicateMethod indicates the same method was declared more than once.
	ErrDuplicateMethod = errors.New("method declared more than once")
	// ErrNoPermissionsRequired indicates Require was called with no permissions,
	// which would authorize every caller for that method.
	ErrNoPermissionsRequired = errors.New("method required with no permissions")
	// ErrEmptyPermission indicates a requirement listed an empty permission.
	ErrEmptyPermission = errors.New("empty permission required")
	// ErrOverrideUndeclared indicates Override named a method that nothing
	// declared, which is almost always a typo or a method the surface renamed.
	ErrOverrideUndeclared = errors.New("override of a method nothing declared")
	// ErrDuplicateOverride indicates the same method was overridden more than once.
	ErrDuplicateOverride = errors.New("method overridden more than once")
)

// Requirements is the frozen table of what each RPC method demands.
//
// It is immutable once built, which is why the interceptors need no lock. A
// mutable table guarded by a mutex costs a lock acquisition on every RPC to
// protect a map that is never written after startup.
type Requirements struct {
	byMethod map[string][]authorization.Permission
	public   map[string]struct{}
}

// RequirementsBuilder accumulates method requirements and validates them as a
// whole.
type RequirementsBuilder struct {
	byMethod   map[string][]authorization.Permission
	public     map[string]struct{}
	declared   map[string]int
	overrides  map[string][]authorization.Permission
	overridden map[string]int
	errs       []error
}

// NewRequirements returns a builder for a Requirements table.
func NewRequirements() *RequirementsBuilder {
	return &RequirementsBuilder{
		byMethod:   map[string][]authorization.Permission{},
		public:     map[string]struct{}{},
		declared:   map[string]int{},
		overrides:  map[string][]authorization.Permission{},
		overridden: map[string]int{},
	}
}

// Require declares that fullMethod demands every permission in perms.
//
// Requiring zero permissions is an error rather than a way to say "any
// authenticated caller" — it reads as a requirement while behaving as an
// allow, and that gap is where an authorization hole hides. Say Public
// instead, which means the same thing and looks like it.
func (b *RequirementsBuilder) Require(fullMethod string, perms ...authorization.Permission) *RequirementsBuilder {
	b.declared[fullMethod]++

	switch {
	case fullMethod == "":
		b.errs = append(b.errs, ErrEmptyMethod)
	case len(perms) == 0:
		b.errs = append(b.errs, errors.Wrapf(ErrNoPermissionsRequired, "method %q", fullMethod))
	}

	for _, p := range perms {
		if p == "" {
			b.errs = append(b.errs, errors.Wrapf(ErrEmptyPermission, "method %q", fullMethod))
		}
	}

	if fullMethod != "" && len(perms) > 0 {
		b.byMethod[fullMethod] = slices.Clone(perms)
	}

	return b
}

// RequireAll declares requirements from a map, which is the shape a service
// package naturally exports for its own methods. Several such maps merge into
// one table, and a method declared by two of them is reported as a duplicate
// rather than silently taking whichever was applied last.
func (b *RequirementsBuilder) RequireAll(m map[string][]authorization.Permission) *RequirementsBuilder {
	for _, method := range slices.Sorted(maps.Keys(m)) {
		b.Require(method, m[method]...)
	}

	return b
}

// Public declares that fullMethod requires no authorization.
//
// Being public is a declaration, never an omission. An undeclared method is
// denied, so forgetting to register a route fails closed and loudly, while
// forgetting to mark one public fails closed and obviously.
func (b *RequirementsBuilder) Public(fullMethod string) *RequirementsBuilder {
	b.declared[fullMethod]++

	if fullMethod == "" {
		b.errs = append(b.errs, ErrEmptyMethod)

		return b
	}

	b.public[fullMethod] = struct{}{}

	return b
}

// Override replaces what an already-declared fullMethod demands with perms.
//
// It is how a deployment amends a surface's fragment rather than rebuilding
// it: reserving one method for operators is a line against the fragment the
// surface exports, and a method the surface adds later still arrives through
// that fragment instead of being silently absent from a hand-copied table.
//
// Overrides apply at Build, after every declaration, so it does not matter
// whether Override is called before or after the fragment that declares the
// method. Overriding a Public method makes it require perms instead.
//
// An override of a method nothing declared is ErrOverrideUndeclared, because
// the likeliest cause is a typo or a method the surface renamed, and quietly
// declaring it would leave the real method on its default. Overriding a method
// twice is ErrDuplicateOverride, for the same reason declaring it twice is an
// error. Zero permissions and empty ones are refused as they are by Require.
func (b *RequirementsBuilder) Override(fullMethod string, perms ...authorization.Permission) *RequirementsBuilder {
	b.overridden[fullMethod]++

	switch {
	case fullMethod == "":
		b.errs = append(b.errs, ErrEmptyMethod)
	case len(perms) == 0:
		b.errs = append(b.errs, errors.Wrapf(ErrNoPermissionsRequired, "override of method %q", fullMethod))
	}

	for _, p := range perms {
		if p == "" {
			b.errs = append(b.errs, errors.Wrapf(ErrEmptyPermission, "override of method %q", fullMethod))
		}
	}

	if fullMethod != "" && len(perms) > 0 {
		b.overrides[fullMethod] = slices.Clone(perms)
	}

	return b
}

// Build validates the accumulated declarations and freezes them.
//
// It reports every problem it found rather than the first, because a table
// assembled from a dozen service packages usually has more than one, and
// fixing them one restart at a time is miserable.
func (b *RequirementsBuilder) Build() (*Requirements, error) {
	for _, method := range slices.Sorted(maps.Keys(b.declared)) {
		if b.declared[method] > 1 {
			b.errs = append(b.errs, errors.Wrapf(ErrDuplicateMethod, "method %q", method))
		}
	}

	for _, method := range slices.Sorted(maps.Keys(b.overridden)) {
		switch {
		case method == "":
			// Already reported as ErrEmptyMethod.
		case b.declared[method] == 0:
			b.errs = append(b.errs, errors.Wrapf(ErrOverrideUndeclared, "method %q", method))
		case b.overridden[method] > 1:
			b.errs = append(b.errs, errors.Wrapf(ErrDuplicateOverride, "method %q", method))
		}
	}

	if err := errors.Join(b.errs...); err != nil {
		return nil, err
	}

	byMethod := maps.Clone(b.byMethod)
	public := maps.Clone(b.public)

	for method, perms := range b.overrides {
		byMethod[method] = slices.Clone(perms)
		delete(public, method)
	}

	return &Requirements{
		byMethod: byMethod,
		public:   public,
	}, nil
}

// lookup reports what fullMethod requires: the permissions, whether the method
// is public, and whether it was declared at all.
func (r *Requirements) lookup(fullMethod string) (perms []authorization.Permission, public, declared bool) {
	if _, ok := r.public[fullMethod]; ok {
		return nil, true, true
	}
	perms, ok := r.byMethod[fullMethod]

	return perms, false, ok
}

// Methods returns every declared method name, sorted. It exists so consumers
// can assert their table covers every method their server registers — the
// check that turns "we remembered to declare everything" from a convention
// into a test.
func (r *Requirements) Methods() []string {
	out := make([]string, 0, len(r.byMethod)+len(r.public))
	out = append(out, slices.Collect(maps.Keys(r.byMethod))...)
	out = append(out, slices.Collect(maps.Keys(r.public))...)
	slices.Sort(out)

	return out
}

// UngrantablePermissions reports every permission some method requires that no
// role in roles grants, mapped to the sorted methods that require it. An empty
// result means every requirement is satisfiable by some combination of roles.
//
// roles is each role's effective permission set, inheritance already applied,
// which is what authorization.ExpandInheritance returns. A permission missing
// from all of them makes every method requiring it unreachable for everyone —
// usually an Override naming a permission the role table never learned about —
// and nothing at runtime distinguishes that from ordinary denials.
//
// It checks permissions individually rather than per method: a principal's
// grants are the union of its roles, so a method requiring two permissions held
// by two different roles is still reachable by someone holding both.
func (r *Requirements) UngrantablePermissions(roles map[string]*authorization.PermissionSet) map[authorization.Permission][]string {
	out := map[authorization.Permission][]string{}

	for _, method := range slices.Sorted(maps.Keys(r.byMethod)) {
		for _, perm := range r.byMethod[method] {
			if grantedByAny(roles, perm) || slices.Contains(out[perm], method) {
				continue
			}

			out[perm] = append(out[perm], method)
		}
	}

	return out
}

func grantedByAny(roles map[string]*authorization.PermissionSet, perm authorization.Permission) bool {
	for _, set := range roles {
		if set.Has(perm) {
			return true
		}
	}

	return false
}
