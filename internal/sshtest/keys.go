package sshtest

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
)

// ClientKey makes an ed25519 key, writes it unencrypted in OpenSSH format to
// a temp file, and returns its signer and path.
func ClientKey(t testing.TB) (ssh.Signer, string) {
	return clientKey(t, "")
}

// EncryptedClientKey is ClientKey with the file protected by passphrase.
func EncryptedClientKey(t testing.TB, passphrase string) (ssh.Signer, string) {
	return clientKey(t, passphrase)
}

func clientKey(t testing.TB, passphrase string) (ssh.Signer, string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var block *pem.Block
	if passphrase == "" {
		block, err = ssh.MarshalPrivateKey(priv, "")
	} else {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(passphrase))
	}
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0600); err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return signer, path
}
