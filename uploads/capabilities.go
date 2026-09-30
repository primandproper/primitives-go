package uploads

import (
	"context"
	"io"
	"iter"
	"time"

	platformerrors "github.com/primandproper/primitives-go/v2/errors"
)

// ErrSigningUnsupported is returned by URLSigner.SignedURL when the storage behind it cannot mint
// signed URLs at all: a capability answer about the backend, not a failure of this call.
//
// It exists so a caller can tell the two apart without learning the provider's error vocabulary.
// Before it, the only way to recognize the refusal from objectstorage was to import
// gocloud.dev/gcerrors and compare codes, which put a storage driver's taxonomy inside whatever
// package wanted to fall back to proxying bytes when a URL could not be had.
//
// Implementations join it with the provider's own error rather than replacing it, so errors.Is
// matches this sentinel and an operator still reads what the backend said. Retrying is pointless:
// the answer is a property of how the storage was configured, and it will not change until that
// does.
var ErrSigningUnsupported = platformerrors.New("storage provider cannot sign URLs")

// The interfaces below are optional capabilities. The core UploadManager only guarantees
// Save/Open/Delete/Exists; richer backends (e.g. objectstorage.Uploader) also implement these.
// Callers that need them either accept the specific interface or type-assert an UploadManager.
type (
	// RangeReader can open a byte range of an object, for partial reads such as HTTP Range
	// requests (video) or seeking within columnar files (parquet).
	RangeReader interface {
		// OpenRange returns a reader over length bytes of the object at path, starting at offset.
		// A negative length reads to the end of the object. The caller must close the reader.
		OpenRange(ctx context.Context, path string, offset, length int64) (io.ReadCloser, error)
	}

	// URLSigner can mint a signed URL granting temporary, direct access to an object, letting
	// clients read or write storage without proxying bytes through the service.
	//
	// Satisfying the interface is not the same as being able to sign. A backend that has no way
	// to mint a URL — objectstorage's memory and filesystem providers are the two this module
	// ships — refuses at the call, and the refusal matches ErrSigningUnsupported under
	// errors.Is. Any other error is a failure to sign that the backend could otherwise have
	// performed.
	URLSigner interface {
		SignedURL(ctx context.Context, path string, opts *SignedURLOptions) (string, error)
	}

	// Attributer can fetch an object's stored metadata.
	Attributer interface {
		Attributes(ctx context.Context, path string) (*Attributes, error)
	}

	// Lister can stream the objects stored under a prefix. The returned iterator yields each
	// object lazily; a non-nil error is yielded once and terminates iteration, and the caller may
	// stop early by breaking out of the range loop.
	Lister interface {
		List(ctx context.Context, prefix string) iter.Seq2[ObjectInfo, error]
	}
)

// ListAll drains a Lister into a slice. It is a convenience for small listings; prefer ranging
// Lister.List directly when a prefix may contain a very large number of objects.
func ListAll(ctx context.Context, l Lister, prefix string) ([]ObjectInfo, error) {
	var out []ObjectInfo
	for obj, err := range l.List(ctx, prefix) {
		if err != nil {
			return nil, err
		}

		out = append(out, obj)
	}

	return out, nil
}

type (
	// SignedURLOptions configures a signed URL.
	SignedURLOptions struct {
		_ struct{} `json:"-"`

		// Method is the HTTP method the URL permits: "GET", "PUT", or "DELETE". Empty means "GET".
		Method string
		// ContentType, for PUT URLs, is the exact Content-Type the client must send.
		ContentType string
		// Expiry sets how long the URL is valid. Zero means the provider default.
		Expiry time.Duration
	}

	// Attributes describes a stored object.
	Attributes struct {
		_            struct{} `json:"-"`
		ModTime      time.Time
		ContentType  string
		CacheControl string
		ETag         string
		Size         int64
	}

	// ObjectInfo describes a single entry returned by Lister.List.
	ObjectInfo struct {
		_       struct{} `json:"-"`
		ModTime time.Time
		Path    string
		Size    int64
		IsDir   bool
	}
)
