package media

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

// keyRe accepts exactly canonical lowercase UUID strings — the shape the
// media service generates. Anything else (path separators, "..", names)
// is rejected, so a storage key can never steer a filesystem path.
var keyRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// FSStore is the filesystem adapter: final objects live at
// <root>/<key>, staging slots at <root>/.staged/<key>.part. Both
// directories sit under one root, so Commit's rename is atomic.
type FSStore struct {
	root string
}

var _ Store = (*FSStore)(nil)

// NewFSStore prepares (creating if needed) the root and staging
// directories. The root is operator configuration, never user input.
func NewFSStore(root string) (*FSStore, error) {
	if root == "" {
		return nil, errors.New("fsstore: root required")
	}
	if err := os.MkdirAll(filepath.Join(root, ".staged"), 0o750); err != nil {
		return nil, err
	}
	return &FSStore{root: root}, nil
}

func (s *FSStore) finalPath(key string) string { return filepath.Join(s.root, key) }
func (s *FSStore) stagePath(key string) string {
	return filepath.Join(s.root, ".staged", key+".part")
}

func (s *FSStore) Stage(key string, r io.Reader, maxBytes int64) (Staged, error) {
	if !keyRe.MatchString(key) {
		return Staged{}, ErrInvalidKey
	}
	f, err := os.Create(s.stagePath(key))
	if err != nil {
		return Staged{}, err
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	head := make([]byte, 512)
	n, err := io.Copy(io.MultiWriter(f, h, &headWriter{buf: head}), io.LimitReader(r, maxBytes+1))
	if err != nil {
		_ = f.Close()
		_ = os.Remove(s.stagePath(key))
		return Staged{}, err
	}
	if n > maxBytes {
		_ = f.Close()
		_ = os.Remove(s.stagePath(key))
		return Staged{}, ErrTooLarge
	}
	// fsync before the file can be renamed onto the final path
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(s.stagePath(key))
		return Staged{}, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(s.stagePath(key))
		return Staged{}, err
	}
	return Staged{Size: n, SHA256: hex.EncodeToString(h.Sum(nil)), Head: head}, nil
}

func (s *FSStore) Commit(key string) error {
	if !keyRe.MatchString(key) {
		return ErrInvalidKey
	}
	if err := os.Rename(s.stagePath(key), s.finalPath(key)); err != nil {
		return err
	}
	// fsync the directory so the rename itself is durable
	dir, err := os.Open(s.root)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}

func (s *FSStore) Discard(key string) error {
	if !keyRe.MatchString(key) {
		return ErrInvalidKey
	}
	err := os.Remove(s.stagePath(key))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (s *FSStore) Exists(key string) (bool, error) {
	if !keyRe.MatchString(key) {
		return false, ErrInvalidKey
	}
	_, err := os.Stat(s.finalPath(key))
	switch {
	case err == nil:
		return true, nil
	case os.IsNotExist(err):
		return false, nil
	default:
		return false, err
	}
}

func (s *FSStore) Open(key string) (io.ReadCloser, error) {
	if !keyRe.MatchString(key) {
		return nil, ErrInvalidKey
	}
	f, err := os.Open(s.finalPath(key))
	if os.IsNotExist(err) {
		return nil, ErrNotFound
	}
	return f, err
}

func (s *FSStore) Delete(key string) error {
	if !keyRe.MatchString(key) {
		return ErrInvalidKey
	}
	err := os.Remove(s.finalPath(key))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// headWriter captures the first 512 bytes while io.Copy streams.
type headWriter struct {
	buf []byte
	n   int
}

func (w *headWriter) Write(p []byte) (int, error) {
	c := copy(w.buf[w.n:], p)
	w.n += c
	return len(p), nil
}
