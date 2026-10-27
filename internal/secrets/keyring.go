package secrets

import (
	"errors"

	"github.com/zalando/go-keyring"
)

const service = "managify"

type keyringStore struct{}

// NewKeyring stores secrets in Windows Credential Manager, macOS Keychain or
// the Secret Service on Linux.
func NewKeyring() Store { return keyringStore{} }

func (keyringStore) Get(account string) (string, error) {
	v, err := keyring.Get(service, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	return v, err
}

func (keyringStore) Set(account, secret string) error {
	return keyring.Set(service, account, secret)
}

func (keyringStore) Delete(account string) error {
	err := keyring.Delete(service, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}
