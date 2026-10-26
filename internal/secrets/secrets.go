// Package secrets keeps passwords out of config files: in the OS keychain, or
// only in process memory for "ask each session" servers.
package secrets

import "errors"

var ErrNotFound = errors.New("secret not found")

type Store interface {
	Get(account string) (string, error) // ErrNotFound when absent
	Set(account, secret string) error
	Delete(account string) error // absent is not an error
}

// LoginAccount is the keychain account for a server's login password.
func LoginAccount(serverID string) string { return serverID }

// SudoAccount is the keychain account for a key-auth server's sudo password.
func SudoAccount(serverID string) string { return serverID + ":sudo" }
