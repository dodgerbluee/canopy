package identity

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
)

// OIDC secrets have a separate persistent key, never included in DB responses.
type credentialBox struct{ key []byte }

func loadCredentialBox(path string) (*credentialBox, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		f, err := os.CreateTemp(filepath.Dir(path), ".oidc-key-*")
		if err != nil {
			return nil, err
		}
		defer os.Remove(f.Name())
		b := make([]byte, 32)
		_, err = rand.Read(b)
		if err == nil {
			_, err = f.Write(b)
		}
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return nil, err
		}
		if err = os.Link(f.Name(), path); err != nil && !os.IsExist(err) {
			return nil, err
		}
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("OIDC key must be a private regular file")
	}
	b, err := os.ReadFile(path)
	if err != nil || len(b) != 32 {
		return nil, errors.New("OIDC key unavailable")
	}
	return &credentialBox{b}, nil
}
func (b *credentialBox) cipher() (cipher.AEAD, error) {
	c, err := aes.NewCipher(b.key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(c)
}
func (b *credentialBox) seal(secret, aad string) ([]byte, error) {
	a, err := b.cipher()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, a.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	return a.Seal(nonce, nonce, []byte(secret), []byte(aad)), nil
}
func (b *credentialBox) open(sealed []byte, aad string) (string, error) {
	a, err := b.cipher()
	if err != nil || len(sealed) < a.NonceSize() {
		return "", errors.New("OIDC credential unavailable")
	}
	v, err := a.Open(nil, sealed[:a.NonceSize()], sealed[a.NonceSize():], []byte(aad))
	return string(v), err
}
