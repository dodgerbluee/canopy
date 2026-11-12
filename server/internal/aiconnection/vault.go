package aiconnection

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
)

var ErrCredential = errors.New("Saved AI credential cannot be opened. Re-enter its API key.")

type Vault struct{ key []byte }

// LoadVault installs one fully written private key atomically. API and worker
// share this file; it is intentionally separate from database/export content.
func LoadVault(path string) (*Vault, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, errors.New("AI credential key directory unavailable")
	}
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		file, err := os.CreateTemp(filepath.Dir(path), ".ai-key-*")
		if err != nil {
			return nil, errors.New("AI credential key could not be created")
		}
		defer os.Remove(file.Name())
		key := make([]byte, 32)
		if _, err = rand.Read(key); err == nil {
			_, err = file.Write(key)
		}
		if err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return nil, errors.New("AI credential key could not be written")
		}
		if err = os.Link(file.Name(), path); err != nil && !os.IsExist(err) {
			return nil, errors.New("AI credential key could not be installed")
		}
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("AI credential key must be a private regular file")
	}
	key, err := os.ReadFile(path)
	if err != nil || len(key) != 32 {
		return nil, errors.New("AI credential key is invalid")
	}
	return &Vault{key: key}, nil
}
func (v *Vault) aead() (cipher.AEAD, error) {
	if v == nil || len(v.key) != 32 {
		return nil, ErrCredential
	}
	block, err := aes.NewCipher(v.key)
	if err != nil {
		return nil, ErrCredential
	}
	return cipher.NewGCM(block)
}
func (v *Vault) seal(secret, aad string) ([]byte, error) {
	if secret == "" {
		return []byte{}, nil
	}
	a, err := v.aead()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, a.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, ErrCredential
	}
	return a.Seal(nonce, nonce, []byte(secret), []byte(aad)), nil
}
func (v *Vault) open(sealed []byte, aad string) (string, error) {
	if len(sealed) == 0 {
		return "", nil
	}
	a, err := v.aead()
	if err != nil || len(sealed) < a.NonceSize() {
		return "", ErrCredential
	}
	plain, err := a.Open(nil, sealed[:a.NonceSize()], sealed[a.NonceSize():], []byte(aad))
	if err != nil {
		return "", ErrCredential
	}
	return string(plain), nil
}
func (v *Vault) fingerprint(input []byte) string {
	mac := hmac.New(sha256.New, v.key)
	_, _ = mac.Write(input)
	return hex.EncodeToString(mac.Sum(nil))
}
