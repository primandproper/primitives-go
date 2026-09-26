package database

import (
	"database/sql"
	"time"

	"github.com/primandproper/primitives-go/v2/database/nullable"
)

// The conversions between Go values and the sql.Null types live in nullable,
// which imports only the standard library, so a package that needs them does
// not have to depend on database for them. These delegate, and cannot drift.

// TimeFromNullTime is nullable.TimeFromNullTime.
func TimeFromNullTime(nt sql.NullTime) time.Time {
	return nullable.TimeFromNullTime(nt)
}

// TimePointerFromNullTime is nullable.TimePointerFromNullTime.
func TimePointerFromNullTime(nt sql.NullTime) *time.Time {
	return nullable.TimePointerFromNullTime(nt)
}

// StringPointerFromNullString is nullable.StringPointerFromNullString.
func StringPointerFromNullString(nt sql.NullString) *string {
	return nullable.StringPointerFromNullString(nt)
}

// StringFromNullString is nullable.StringFromNullString.
func StringFromNullString(nt sql.NullString) string {
	return nullable.StringFromNullString(nt)
}

// NullStringFromString is nullable.NullStringFromString.
func NullStringFromString(s string) sql.NullString {
	return nullable.NullStringFromString(s)
}

// NullStringFromStringPointer is nullable.NullStringFromStringPointer.
func NullStringFromStringPointer(s *string) sql.NullString {
	return nullable.NullStringFromStringPointer(s)
}

// NullTimeFromTime is nullable.NullTimeFromTime.
func NullTimeFromTime(t time.Time) sql.NullTime {
	return nullable.NullTimeFromTime(t)
}

// NullTimeFromTimePointer is nullable.NullTimeFromTimePointer.
func NullTimeFromTimePointer(t *time.Time) sql.NullTime {
	return nullable.NullTimeFromTimePointer(t)
}

// NullInt32FromUint8Pointer is nullable.NullInt32FromUint8Pointer.
func NullInt32FromUint8Pointer(i *uint8) sql.NullInt32 {
	return nullable.NullInt32FromUint8Pointer(i)
}

// NullInt32FromUint16Pointer is nullable.NullInt32FromUint16Pointer.
func NullInt32FromUint16Pointer(i *uint16) sql.NullInt32 {
	return nullable.NullInt32FromUint16Pointer(i)
}

// NullInt32FromUint16 is nullable.NullInt32FromUint16.
func NullInt32FromUint16(i uint16) sql.NullInt32 {
	return nullable.NullInt32FromUint16(i)
}

// NullBoolFromBool is nullable.NullBoolFromBool.
func NullBoolFromBool(b bool) sql.NullBool {
	return nullable.NullBoolFromBool(b)
}

// NullBoolFromBoolPointer is nullable.NullBoolFromBoolPointer.
func NullBoolFromBoolPointer(b *bool) sql.NullBool {
	return nullable.NullBoolFromBoolPointer(b)
}

// BoolFromNullBool is nullable.BoolFromNullBool.
func BoolFromNullBool(b sql.NullBool) bool {
	return nullable.BoolFromNullBool(b)
}

// NullInt32FromInt32Pointer is nullable.NullInt32FromInt32Pointer.
func NullInt32FromInt32Pointer(i *int32) sql.NullInt32 {
	return nullable.NullInt32FromInt32Pointer(i)
}

// NullInt32FromUint32Pointer is nullable.NullInt32FromUint32Pointer.
func NullInt32FromUint32Pointer(i *uint32) sql.NullInt32 {
	return nullable.NullInt32FromUint32Pointer(i)
}

// Int32PointerFromNullInt32 is nullable.Int32PointerFromNullInt32.
func Int32PointerFromNullInt32(i sql.NullInt32) *int32 {
	return nullable.Int32PointerFromNullInt32(i)
}

// Float32PointerFromNullString is nullable.Float32PointerFromNullString.
func Float32PointerFromNullString(f sql.NullString) *float32 {
	return nullable.Float32PointerFromNullString(f)
}

// Float64PointerFromNullString is nullable.Float64PointerFromNullString.
func Float64PointerFromNullString(f sql.NullString) *float64 {
	return nullable.Float64PointerFromNullString(f)
}

// StringFromFloat32 is nullable.StringFromFloat32.
func StringFromFloat32(f float32) string {
	return nullable.StringFromFloat32(f)
}

// Float32FromString is nullable.Float32FromString.
func Float32FromString(s string) float32 {
	return nullable.Float32FromString(s)
}

// Float32FromNullString is nullable.Float32FromNullString.
func Float32FromNullString(s sql.NullString) float32 {
	return nullable.Float32FromNullString(s)
}

// NullStringFromFloat32Pointer is nullable.NullStringFromFloat32Pointer.
func NullStringFromFloat32Pointer(f *float32) sql.NullString {
	return nullable.NullStringFromFloat32Pointer(f)
}

// NullStringFromFloat32 is nullable.NullStringFromFloat32.
func NullStringFromFloat32(f float32) sql.NullString {
	return nullable.NullStringFromFloat32(f)
}

// StringFromFloat64 is nullable.StringFromFloat64.
func StringFromFloat64(f float64) string {
	return nullable.StringFromFloat64(f)
}

// NullStringFromFloat64Pointer is nullable.NullStringFromFloat64Pointer.
func NullStringFromFloat64Pointer(f *float64) sql.NullString {
	return nullable.NullStringFromFloat64Pointer(f)
}

// NullInt64FromUint32Pointer is nullable.NullInt64FromUint32Pointer.
func NullInt64FromUint32Pointer(f *uint32) sql.NullInt64 {
	return nullable.NullInt64FromUint32Pointer(f)
}

// Uint16PointerFromNullInt32 is nullable.Uint16PointerFromNullInt32.
func Uint16PointerFromNullInt32(f sql.NullInt32) *uint16 {
	return nullable.Uint16PointerFromNullInt32(f)
}

// Uint32PointerFromNullInt32 is nullable.Uint32PointerFromNullInt32.
func Uint32PointerFromNullInt32(f sql.NullInt32) *uint32 {
	return nullable.Uint32PointerFromNullInt32(f)
}

// Uint32PointerFromNullInt64 is nullable.Uint32PointerFromNullInt64.
func Uint32PointerFromNullInt64(f sql.NullInt64) *uint32 {
	return nullable.Uint32PointerFromNullInt64(f)
}

// CoerceTime normalizes whatever a driver hands back for a timestamp read as
// `any`, reporting whether it recognized one.
//
// Timestamps are scanned as `any` rather than sql.NullTime because the drivers
// disagree. pgx and go-sql-driver return a time.Time, but modernc's SQLite
// driver stores a bound time.Time as Go's own String() rendering, and an
// aggregate over such a column loses the declared DATETIME affinity — so it
// comes back as a plain string that sql.NullTime refuses outright.
//
// A NULL reports false, and callers treat that as "no value" rather than as the
// zero time: an empty backlog is not a row created at the epoch.
func CoerceTime(v any) (time.Time, bool) {
	var s string

	switch typed := v.(type) {
	case nil:
		return time.Time{}, false
	case time.Time:
		return typed, true
	case string:
		s = typed
	case []byte:
		s = string(typed)
	default:
		return time.Time{}, false
	}

	// Go's String() layout comes first: it is what the SQLite path actually
	// produces, and the others are here so a driver change does not silently
	// zero the value.
	for _, layout := range []string{
		"2006-01-02 15:04:05.999999999 -0700 MST",
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	} {
		if parsed, parseErr := time.Parse(layout, s); parseErr == nil {
			return parsed, true
		}
	}

	return time.Time{}, false
}

// BlobOrNil maps an empty encoding to a SQL NULL rather than an empty blob.
//
// "No value" and "an empty value" mean the same thing in every column in this
// module that holds an encoded payload — no request, no failure map, no
// snapshot — and storing two renderings of it would make the round trip depend
// on which call site wrote the row: one reader gets nil back and another gets a
// zero-length slice, from rows that were written to mean the same thing.
func BlobOrNil(b []byte) any {
	if len(b) == 0 {
		return nil
	}

	return b
}

// CursorOrder reports the ORDER BY direction and the comparison operator a
// keyset-paginated read uses for a given sort direction.
//
// It is one function because the two halves have to agree and nothing checks
// that they do. A descending page that kept "id > cursor" reads the wrong side
// of the boundary: the first page comes back, and every page after it skips
// straight past the rows the caller asked for. That failure produces no error
// and no empty result — just a listing quietly missing its middle.
func CursorOrder(descending bool) (direction, comparison string) {
	if descending {
		return "DESC", " < "
	}

	return "ASC", " > "
}
