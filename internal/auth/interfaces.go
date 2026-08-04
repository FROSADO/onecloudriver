package auth

import (
	"github.com/zalando/go-keyring"
)

// Keyring defines the interface for secure storage of secrets.
type Keyring interface {
	Get(service, user string) (string, error)
	Set(service, user, password string) error
	Delete(service, user string) error
}

// realKeyring adapts the zalando/go-keyring library to the Keyring interface,
// allowing injection of mocks in tests.
type realKeyring struct{}

func (r realKeyring) Get(service, user string) (string, error) {
	return keyring.Get(service, user)
}

func (r realKeyring) Set(service, user, password string) error {
	return keyring.Set(service, user, password)
}

func (r realKeyring) Delete(service, user string) error {
	return keyring.Delete(service, user)
}
