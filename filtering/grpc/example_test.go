package grpc_test

import (
	"fmt"
	"time"

	"github.com/primandproper/primitives-go/v2/filtering"
	"github.com/primandproper/primitives-go/v2/filtering/filteringpb"
	filteringgrpc "github.com/primandproper/primitives-go/v2/filtering/grpc"
)

// A service handler decoding the filter off a request. The page size arrives as
// the uint32 protobuf can carry, and is clamped before it is narrowed — 70000
// is answered with the ceiling rather than with the 4464 it would have wrapped
// to.
//
// The decision is ArchivedDenied because this caller has not been shown to
// hold the grant that archives the noun; a client's include_archived would be
// cleared rather than honored.
func ExampleFromProto() {
	filter, _, err := filteringgrpc.FromProto(&filteringpb.QueryFilter{
		MaxResponseSize: new(uint32(70000)),
		Cursor:          new("row-42"),
	}, filteringgrpc.ArchivedDenied)
	if err != nil {
		// A filter that could not be read in full is still usable. Answer with
		// it, or refuse the request — errors/grpc renders this as
		// InvalidArgument either way.
		fmt.Println("unreadable filter:", err)
	}

	fmt.Println(*filter.MaxResponseSize)
	fmt.Println(*filter.SortBy)
	fmt.Println(*filter.Cursor)

	// Output:
	// 250
	// asc
	// row-42
}

// A surface whose caller holds the read grant but not the one that archives
// the noun. The client asked for archived rows; the read is answered with live
// ones rather than refused, and the surface records the narrowing on its span
// so the missing rows can be explained from a trace.
func ExampleFromProto_archivedDenied() {
	hasArchiveGrant := false // grants.Has(PermissionArchiveWidgets), in a real surface

	filter, cleared, err := filteringgrpc.FromProto(&filteringpb.QueryFilter{
		IncludeArchived: new(true),
	}, filteringgrpc.ArchivedIf(hasArchiveGrant))
	if err != nil {
		panic(err)
	}

	if cleared {
		// A real surface records this on the read's Operation, under
		// keys.FilterIncludeArchivedClearedKey.
		fmt.Println("include_archived cleared")
	}

	fmt.Println(filter.IncludeArchived == nil)

	// Output:
	// include_archived cleared
	// true
}

// An absent filter is the default one, so a client that sent nothing is
// answered under the same rules as one that sent an empty filter.
func ExampleFromProto_absent() {
	filter, _, err := filteringgrpc.FromProto(nil, filteringgrpc.ArchivedDenied)
	if err != nil {
		panic(err)
	}

	fmt.Println(*filter.MaxResponseSize)

	// Output: 50
}

// The response half, on its way back out. CountsKnown is what the flag on the
// wire is for: a store that could not answer the counts reports zeroes, and so
// does a collection with nothing in it.
func ExamplePaginationToProto() {
	pagination := filtering.Pagination{
		AppliedQueryFilter: filtering.DefaultQueryFilter(),
		Cursor:             "row-91",
		PreviousCursor:     "row-42",
		FilteredCount:      7,
		TotalCount:         12,
		MaxResponseSize:    filtering.DefaultQueryFilterLimit,
		CountsKnown:        true,
	}

	out := filteringgrpc.PaginationToProto(pagination)

	fmt.Println(out.GetCursor())
	fmt.Println(out.GetPreviousCursor())
	fmt.Println(out.GetCountsKnown(), out.GetFilteredCount(), out.GetTotalCount())
	fmt.Println(out.GetAppliedQueryFilter().GetMaxResponseSize())

	// Output:
	// row-91
	// row-42
	// true 7 12
	// 50
}

// A client putting a filter on the wire. Fields it did not set stay absent
// rather than crossing as zeroes, so the server can still tell "no page size"
// from "a page size of zero".
func ExampleToProto() {
	filter := &filtering.QueryFilter{
		SortBy:       filtering.SortDescending,
		CreatedAfter: new(time.Date(2026, time.August, 24, 0, 0, 0, 0, time.UTC)),
	}

	out := filteringgrpc.ToProto(filter)

	fmt.Println(out.GetSortBy())
	fmt.Println(out.GetCreatedAfter().AsTime().Format(time.RFC3339))
	fmt.Println(out.MaxResponseSize == nil)

	// Output:
	// desc
	// 2026-08-24T00:00:00Z
	// true
}
