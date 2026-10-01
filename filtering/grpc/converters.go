package grpc

import (
	"time"

	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/filtering"
	"github.com/primandproper/primitives-go/v2/filtering/filteringpb"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// The proto field names, for error messages that name the field a caller
// actually sent rather than the query parameter with the same meaning. They are
// literals because a descriptor lookup to build an error string is a lot of
// machinery for four words; TestFieldNames is what keeps them from naming a
// field the schema does not have.
const (
	fieldCreatedAfter  = "created_after"
	fieldCreatedBefore = "created_before"
	fieldUpdatedAfter  = "updated_after"
	fieldUpdatedBefore = "updated_before"
	fieldAppliedFilter = "applied_query_filter"
)

// ArchiveDecision is whether the caller decoding a filter may see archived
// rows, which QueryFilterFromProto needs before it will hand one back.
//
// It is a struct rather than a bool so that a call site reads as a decision —
// QueryFilterFromProto(in, ArchivedDenied) — and so that a bare true does not
// compile in its place. Its zero value is ArchivedDenied: a decision nobody made is the
// one that leaks nothing.
type ArchiveDecision struct {
	allowed bool
}

var (
	// ArchivedAllowed honors a client's include_archived as sent. Pass it for a
	// caller holding the grant that archives what the read pages, or for a
	// surface whose noun has no archive to hide.
	ArchivedAllowed = ArchiveDecision{allowed: true}

	// ArchivedDenied clears a client's include_archived, so the read answers
	// with live rows whatever the client asked for.
	ArchivedDenied = ArchiveDecision{}
)

// ArchivedIf is the decision a surface reaches by asking, as in
// ArchivedIf(grants.Has(PermissionArchiveWidgets)).
func ArchivedIf(allowed bool) ArchiveDecision {
	return ArchiveDecision{allowed: allowed}
}

// Allowed reports whether the decision honors include_archived.
func (d ArchiveDecision) Allowed() bool {
	return d.allowed
}

// FromProto converts a wire QueryFilter into the Go one, honoring
// include_archived as the client sent it.
//
// Deprecated: use QueryFilterFromProto, which takes the caller's archive
// decision as an argument. FromProto treats include_archived as an
// instruction, so a surface that forgets to clear it afterward hands archived
// rows to any caller holding the read grant. It is equivalent to
// QueryFilterFromProto(in, ArchivedAllowed) with the cleared result dropped,
// and it goes at the next major version.
func FromProto(in *filteringpb.QueryFilter) (*filtering.QueryFilter, error) {
	qf, _, err := QueryFilterFromProto(in, ArchivedAllowed)

	return qf, err
}

// QueryFilterFromProto converts a wire QueryFilter into the Go one, applying
// the ceiling before the narrowing and then the defaults, and applying the
// caller's archive decision to include_archived.
//
// An absent message is the default filter rather than an empty one: a client
// that sent no filter asked for the first page at the default size, which is
// what every other transport here reads it as.
//
// # Why the archive decision is an argument
//
// include_archived is a request, not an instruction. Whether a caller may see
// archived rows is a question only the surface can answer, and when answering
// it was something each surface did to the filter afterward, the surfaces that
// forgot handed archived rows to anybody holding the read grant — the archive
// grant became a name rather than a policy, and nothing failed to say so. As an
// argument, no surface can decode a filter without making the decision. (This
// is why FromProto, which took no decision, is deprecated.)
//
// ArchivedAllowed leaves the field as the client sent it. ArchivedDenied
// clears it to absent rather than writing false, so the store receives the
// filter of a caller who never asked. That is a narrowing and not a refusal: a
// client that asked for archived rows it may not see is answered with live
// ones, not an error. An absent field stays absent either way.
//
// cleared reports that the client asked for archived rows and the decision
// took them away, which is what a surface records on the read's span (under
// keys.FilterIncludeArchivedClearedKey) so that "my archived rows stopped
// arriving" is answerable from a trace. A denied false is dropped too, but
// silently: it asked for what an absent field asks for, and nothing changed.
//
// The page size is the field this function exists for. Protobuf has no uint16,
// so max_response_size crosses as a uint32 and something has to narrow it;
// doing that before the clamp is silent, because by the time a QueryFilter
// exists the wrapped value is indistinguishable from one a client sent.
// SetMaxResponseSize takes the wide value and applies the ceiling in the order
// that works. Normalize then supplies the default for a page size that was
// absent or zero, which is what the HTTP path does with the same two cases.
//
// The returned filter is always usable. A timestamp protobuf itself considers
// out of range is left absent and reported, and a sort direction nobody
// recognizes is reported by Normalize with ascending left in place — both
// wrapping errors.ErrUnrecognizedInputValue. A caller reporting the failure
// should discard the filter rather than list against a half-applied one.
func QueryFilterFromProto(in *filteringpb.QueryFilter, archived ArchiveDecision) (qf *filtering.QueryFilter, cleared bool, err error) {
	if in == nil {
		return filtering.DefaultQueryFilter(), false, nil
	}

	qf, err = fromProto(in)

	if !archived.allowed && qf.IncludeArchived != nil {
		cleared = *qf.IncludeArchived
		qf.IncludeArchived = nil
	}

	// Normalize supplies the default page size for an absent one, clamps a
	// present one again — harmlessly, since SetMaxResponseSize already did —
	// and reports an unrecognized sort direction. Joined rather than returned
	// on its own so a field that would not decode is not lost behind a sort
	// direction that happened to be fine.
	return qf, cleared, platformerrors.Join(err, qf.Normalize())
}

// fromProto copies the fields across without normalizing, which is what the
// response half wants: a Pagination reports the filter that was applied, and
// applying defaults to it again would describe a page that was never served.
func fromProto(in *filteringpb.QueryFilter) (*filtering.QueryFilter, error) {
	var errs []error

	qf := &filtering.QueryFilter{}

	// readTime records an error only for a timestamp that was sent and that
	// protobuf itself rejects — one outside the year range the type allows.
	// AsTime would hand back a nonsense instant for that rather than fail, and a
	// window nobody asked for excludes rows nobody would think to look for.
	readTime := func(field string, ts *timestamppb.Timestamp, into **time.Time) {
		if ts == nil {
			return
		}

		if err := ts.CheckValid(); err != nil {
			errs = append(errs, platformerrors.Wrapf(
				platformerrors.Join(platformerrors.ErrUnrecognizedInputValue, err),
				"reading %s field %q", field, ts.String(),
			))

			return
		}

		*into = new(ts.AsTime())
	}

	readTime(fieldCreatedAfter, in.GetCreatedAfter(), &qf.CreatedAfter)
	readTime(fieldCreatedBefore, in.GetCreatedBefore(), &qf.CreatedBefore)
	readTime(fieldUpdatedAfter, in.GetUpdatedAfter(), &qf.UpdatedAfter)
	readTime(fieldUpdatedBefore, in.GetUpdatedBefore(), &qf.UpdatedBefore)

	if in.SortBy != nil {
		qf.SortBy = new(in.GetSortBy())
	}

	if in.Cursor != nil {
		qf.Cursor = new(in.GetCursor())
	}

	if in.IncludeArchived != nil {
		qf.IncludeArchived = new(in.GetIncludeArchived())
	}

	// Absent stays absent rather than arriving as a zero. QueryFilterFromProto
	// normalizes both to the default page size a moment later, exactly as the
	// HTTP path does — but nothing normalizes the filter a Pagination reports, and
	// ToSQLArgs reads an explicit zero there as a request for no rows and an
	// absent one as a request for the default.
	if in.MaxResponseSize != nil {
		qf.SetMaxResponseSize(uint64(in.GetMaxResponseSize()))
	}

	return qf, platformerrors.Join(errs...)
}

// ToProto converts a QueryFilter into the wire message.
//
// A nil filter crosses as an absent message rather than as the default one,
// which is where this parts company with ToValues. A url.Values has no way to
// say "no filter", so ToValues writes the defaults out; a message can simply
// not be there, and QueryFilterFromProto reads an absent one as the default
// filter. Nil therefore survives the round trip as the same request, and a
// Pagination that reports no applied filter keeps reporting none rather than
// acquiring one it never applied.
//
// Absent fields stay absent rather than crossing as zeroes: every field on the
// message has explicit presence for that reason, and a page size of zero that
// was set is not the same value as one that was never set.
func ToProto(qf *filtering.QueryFilter) *filteringpb.QueryFilter {
	if qf == nil {
		return nil
	}

	// The pointers are copied rather than shared. SortAscending and
	// SortDescending are package-level pointers every filter in the process
	// points at, so handing one to a message would put a write through
	// out.SortBy in a position to change what "asc" means everywhere.
	out := &filteringpb.QueryFilter{}

	if qf.SortBy != nil {
		out.SortBy = new(*qf.SortBy)
	}

	if qf.Cursor != nil {
		out.Cursor = new(*qf.Cursor)
	}

	if qf.IncludeArchived != nil {
		out.IncludeArchived = new(*qf.IncludeArchived)
	}

	if qf.CreatedAfter != nil {
		out.CreatedAfter = timestamppb.New(*qf.CreatedAfter)
	}

	if qf.CreatedBefore != nil {
		out.CreatedBefore = timestamppb.New(*qf.CreatedBefore)
	}

	if qf.UpdatedAfter != nil {
		out.UpdatedAfter = timestamppb.New(*qf.UpdatedAfter)
	}

	if qf.UpdatedBefore != nil {
		out.UpdatedBefore = timestamppb.New(*qf.UpdatedBefore)
	}

	if qf.MaxResponseSize != nil {
		out.MaxResponseSize = new(uint32(*qf.MaxResponseSize))
	}

	return out
}

// PaginationFromProto converts the response half back.
//
// The applied filter is copied across without being normalized: it is the
// report of what a page was answered under, and defaulting it here would
// describe a page that was never served. Its page size is still clamped before
// it narrows, because that is a property of the types rather than a policy —
// a uint32 that wraps into a uint16 is wrong whichever direction it travels.
//
// A nil message converts to the zero Pagination, which is the first page of
// nothing with its counts unanswered — and CountsKnown false is what says so.
func PaginationFromProto(in *filteringpb.Pagination) (filtering.Pagination, error) {
	if in == nil {
		return filtering.Pagination{}, nil
	}

	out := filtering.Pagination{
		Cursor:          in.GetCursor(),
		PreviousCursor:  in.GetPreviousCursor(),
		FilteredCount:   in.GetFilteredCount(),
		TotalCount:      in.GetTotalCount(),
		MaxResponseSize: filtering.ClampResponseSize(uint64(in.GetMaxResponseSize())),
		CountsKnown:     in.GetCountsKnown(),
	}

	applied := in.GetAppliedQueryFilter()
	if applied == nil {
		return out, nil
	}

	qf, err := fromProto(applied)
	out.AppliedQueryFilter = qf

	if err != nil {
		return out, platformerrors.Wrapf(err, "reading the %s field", fieldAppliedFilter)
	}

	return out, nil
}

// PaginationToProto converts the response half onto the wire.
//
// The counts cross as they are, with counts_known alongside them, because the
// pair is meaningless without it: a store that could not answer them reports 0
// and 0, which is also what an empty collection reports, and the flag is the
// only thing that tells a client which of those it received.
func PaginationToProto(p filtering.Pagination) *filteringpb.Pagination {
	return &filteringpb.Pagination{
		AppliedQueryFilter: ToProto(p.AppliedQueryFilter),
		Cursor:             p.Cursor,
		PreviousCursor:     p.PreviousCursor,
		FilteredCount:      p.FilteredCount,
		TotalCount:         p.TotalCount,
		MaxResponseSize:    uint32(p.MaxResponseSize),
		CountsKnown:        p.CountsKnown,
	}
}
