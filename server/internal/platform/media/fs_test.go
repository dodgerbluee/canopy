package media

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func newStore(t *testing.T) *FSStore {
	t.Helper()
	s, err := NewFSStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

const testKey = "6f1d2c3b-0a01-4b02-8c03-0d0e0f010203"

func TestStageCommitRoundTrip(t *testing.T) {
	s := newStore(t)
	data := make([]byte, 4096)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	staged, err := s.Stage(testKey, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(data)
	if staged.SHA256 != hex.EncodeToString(want[:]) {
		t.Errorf("sha mismatch: %s", staged.SHA256)
	}
	if staged.Size != int64(len(data)) {
		t.Errorf("size = %d", staged.Size)
	}
	if !bytes.Equal(staged.Head, data[:512]) {
		t.Error("head must be the first 512 bytes")
	}
	// nothing at the final path until Commit
	if ok, _ := s.Exists(testKey); ok {
		t.Fatal("final object present before commit")
	}
	if err := s.Commit(testKey); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.Exists(testKey); !ok {
		t.Fatal("final object missing after commit")
	}
	rc, err := s.Open(testKey)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(got, data) {
		t.Error("stored bytes differ")
	}
	if err := s.Delete(testKey); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.Exists(testKey); ok {
		t.Error("object survived delete")
	}
}

func TestStageTooLarge(t *testing.T) {
	s := newStore(t)
	data := make([]byte, 100)
	_, err := s.Stage(testKey, bytes.NewReader(data), 99)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
	// staging slot must not linger
	if _, err := os.Stat(filepath.Join(s.root, ".staged", testKey+".part")); !os.IsNotExist(err) {
		t.Error("staged slot left behind after ErrTooLarge")
	}
}

func TestKeysAreNeverUserControlled(t *testing.T) {
	s := newStore(t)
	for _, bad := range []string{"../evil", "a/b", "..", "", "not-a-uuid",
		"6F1D2C3B-0A01-4B02-8C03-0D0E0F010203", // uppercase is not server-generated shape
	} {
		if _, err := s.Stage(bad, bytes.NewReader([]byte("x")), 10); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("Stage(%q) = %v, want ErrInvalidKey", bad, err)
		}
		if err := s.Commit(bad); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("Commit(%q) = %v", bad, err)
		}
		if _, err := s.Exists(bad); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("Exists(%q) = %v", bad, err)
		}
		if err := s.Delete(bad); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("Delete(%q) = %v", bad, err)
		}
	}
}

func TestDiscardAndDeleteIdempotent(t *testing.T) {
	s := newStore(t)
	if err := s.Discard(testKey); err != nil {
		t.Errorf("Discard of missing slot: %v", err)
	}
	if err := s.Delete(testKey); err != nil {
		t.Errorf("Delete of missing object: %v", err)
	}
	if _, err := s.Open(testKey); !errors.Is(err, ErrNotFound) {
		t.Errorf("Open of missing object: %v", err)
	}
}

func TestStageInterruptedStreamLeavesNoFinal(t *testing.T) {
	s := newStore(t)
	// a reader that fails mid-copy models a client disconnect
	_, err := s.Stage(testKey, io.MultiReader(
		bytes.NewReader(make([]byte, 50)),
		errReader{},
	), 1000)
	if err == nil {
		t.Fatal("want read error")
	}
	if ok, _ := s.Exists(testKey); ok {
		t.Error("final object appeared for a failed stream")
	}
	if _, err := os.Stat(filepath.Join(s.root, ".staged", testKey+".part")); !os.IsNotExist(err) {
		t.Error("staged slot left behind after failed stream")
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("boom") }
