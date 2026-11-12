// Package media carries the private ObjectStore port and its filesystem
// adapter (T13). Bytes never travel through GraphQL; they are staged,
// checksummed, fsynced and atomically renamed under a server-generated
// opaque key. Storage keys are never derived from user input, and temp
// paths are never user controlled.
//
// Durability contract (03 "HTTP and upload protocol"):
//   - Stage writes to a staging path inside the store root, hashing while
//     copying, and fsyncs the file before returning.
//   - Commit renames the staged file onto the final path (same
//     filesystem, so rename is atomic) and fsyncs the directory.
//   - A file that reached the final path but whose DB transition failed
//     is an orphan candidate; the sweeper reconciles it. A DB row in
//     PROCESSING without a file is a recoverable failure.
package media

import (
	"errors"
	"io"
)

// Staged describes bytes accepted by Stage.
type Staged struct {
	Size   int64
	SHA256 string // hex, lowercase
	Head   []byte // first 512 bytes, for MIME sniffing (never decoded)
}

// ErrTooLarge means the stream exceeded the byte limit given to Stage.
var ErrTooLarge = errors.New("object exceeds byte limit")

// ErrNotFound means no object exists at the key.
var ErrNotFound = errors.New("object not found")

// ErrInvalidKey means the key is not a server-generated opaque key.
// Adapters must reject anything else so paths can never be steered by
// user-controlled strings.
var ErrInvalidKey = errors.New("invalid storage key")

// Store is the port features depend on; the API process and the future
// processor worker reach bytes only through it.
type Store interface {
	// Stage copies r into a staging slot for key, hashing as it goes.
	// It stops with ErrTooLarge as soon as maxBytes+1 bytes are seen.
	Stage(key string, r io.Reader, maxBytes int64) (Staged, error)
	// Commit atomically renames the staged slot to the final location.
	Commit(key string) error
	// Discard removes a staging slot; missing slots are not an error.
	Discard(key string) error
	// Exists reports whether the final object is present.
	Exists(key string) (bool, error)
	// Open opens the final object for reading.
	Open(key string) (io.ReadCloser, error)
	// Delete removes the final object; missing objects are not an error.
	Delete(key string) error
}
