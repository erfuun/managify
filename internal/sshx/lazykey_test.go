package sshx

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/erfuun/managify/internal/secrets"
	"github.com/erfuun/managify/internal/sshtest"
)

func agentWith(t *testing.T, keys ...any) func() (agent.Agent, error) {
	t.Helper()
	kr := agent.NewKeyring()
	for _, k := range keys {
		if err := kr.Add(agent.AddedKey{PrivateKey: k}); err != nil {
			t.Fatal(err)
		}
	}
	return func() (agent.Agent, error) { return kr, nil }
}

// rawKey decrypts the key file written by sshtest, for loading into an agent.
func rawKey(t *testing.T, path, phrase string) any {
	t.Helper()
	b, _ := os.ReadFile(path)
	k, err := ssh.ParseRawPrivateKeyWithPassphrase(b, []byte(phrase))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func otherKey(t *testing.T) ed25519.PrivateKey {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return priv
}

// The spec's own example: the agent already holds the key that key_path
// points at. Nothing should ask for its passphrase.
func TestDialAgentHoldsEncryptedKeyNoPrompt(t *testing.T) {
	signer, path := sshtest.EncryptedClientKey(t, "phrase")
	srv := sshtest.Start(t, sshtest.Options{AuthorizedKey: signer.PublicKey()})
	p := &fakePrompter{Answer: "phrase"}
	d := seededDialer(t, srv, newCreds(secrets.NewMem(), p))
	d.Agent = agentWith(t, rawKey(t, path, "phrase"))
	mustDial(t, d, keyServer(srv, path))
	if n := p.count(PromptPassphrase); n != 0 {
		t.Fatalf("passphrase prompts = %d", n)
	}
}

func TestDialAgentKeyRejectedFileKeyPromptedOnce(t *testing.T) {
	signer, path := sshtest.EncryptedClientKey(t, "phrase")
	srv := sshtest.Start(t, sshtest.Options{AuthorizedKey: signer.PublicKey()})
	p := &fakePrompter{Answer: "phrase"}
	d := seededDialer(t, srv, newCreds(secrets.NewMem(), p))
	d.Agent = agentWith(t, otherKey(t))
	mustDial(t, d, keyServer(srv, path))
	if n := p.count(PromptPassphrase); n != 1 {
		t.Fatalf("passphrase prompts = %d", n)
	}
}

// A key the server does not accept is never decrypted.
func TestDialRejectedEncryptedKeyNotPrompted(t *testing.T) {
	_, path := sshtest.EncryptedClientKey(t, "phrase")
	accepted := otherKey(t)
	pub, _ := ssh.NewPublicKey(accepted.Public())
	srv := sshtest.Start(t, sshtest.Options{AuthorizedKey: pub})
	p := &fakePrompter{Answer: "phrase"}
	d := seededDialer(t, srv, newCreds(secrets.NewMem(), p))
	d.Agent = agentWith(t, accepted)
	mustDial(t, d, keyServer(srv, path))
	if n := p.count(PromptPassphrase); n != 0 {
		t.Fatalf("passphrase prompts = %d", n)
	}
}

func TestDialMissingKeyFileAgentStillUsed(t *testing.T) {
	priv := otherKey(t)
	pub, _ := ssh.NewPublicKey(priv.Public())
	srv := sshtest.Start(t, sshtest.Options{AuthorizedKey: pub})
	d := seededDialer(t, srv, newCreds(secrets.NewMem(), &fakePrompter{}))
	d.Agent = agentWith(t, priv)
	mustDial(t, d, keyServer(srv, filepath.Join(t.TempDir(), "gone")))
}

func TestDialMissingKeyFileNoAgentSaysSo(t *testing.T) {
	_, path := sshtest.ClientKey(t)
	srv := sshtest.Start(t, sshtest.Options{AuthorizedKey: otherSigner(t).PublicKey()})
	d := seededDialer(t, srv, newCreds(secrets.NewMem(), &fakePrompter{}))
	missing := path + ".gone"
	_, err := d.Dial(t.Context(), keyServer(srv, missing))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v", err)
	}
}

func TestDialWrongPassphraseAfterAgent(t *testing.T) {
	signer, path := sshtest.EncryptedClientKey(t, "phrase")
	srv := sshtest.Start(t, sshtest.Options{AuthorizedKey: signer.PublicKey()})
	d := seededDialer(t, srv, newCreds(secrets.NewMem(), &fakePrompter{Answer: "wrong"}))
	d.Agent = agentWith(t, otherKey(t))
	_, err := d.Dial(t.Context(), keyServer(srv, path))
	var ae *AuthError
	if !errors.As(err, &ae) || !ae.PassphraseWrong {
		t.Fatalf("err = %v", err)
	}
}

func TestDialCancelledPassphraseAfterAgent(t *testing.T) {
	signer, path := sshtest.EncryptedClientKey(t, "phrase")
	srv := sshtest.Start(t, sshtest.Options{AuthorizedKey: signer.PublicKey()})
	d := seededDialer(t, srv, newCreds(secrets.NewMem(), &fakePrompter{Err: ErrPromptCancelled}))
	d.Agent = agentWith(t, otherKey(t))
	if _, err := d.Dial(t.Context(), keyServer(srv, path)); !errors.Is(err, ErrPromptCancelled) {
		t.Fatalf("err = %v", err)
	}
}

func rsaKeyFile(t *testing.T, phrase string) (*rsa.PrivateKey, string) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(phrase))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "id_rsa")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0600); err != nil {
		t.Fatal(err)
	}
	return priv, path
}

// OpenSSH 8.8+ refuses SHA-1 "ssh-rsa" signatures; a lazily decrypted RSA
// key must still sign with rsa-sha2-*.
func TestDialEncryptedRSAKeyUsesSHA2(t *testing.T) {
	priv, path := rsaKeyFile(t, "phrase")
	pub, _ := ssh.NewPublicKey(&priv.PublicKey)
	srv := sshtest.Start(t, sshtest.Options{AuthorizedKey: pub,
		PublicKeyAlgorithms: []string{ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSASHA512}})
	p := &fakePrompter{Answer: "phrase"}
	d := seededDialer(t, srv, newCreds(secrets.NewMem(), p))
	d.Agent = agentWith(t, otherKey(t))
	mustDial(t, d, keyServer(srv, path))
	if n := p.count(PromptPassphrase); n != 1 {
		t.Fatalf("passphrase prompts = %d", n)
	}
}

// Old encrypted PEM keys do not carry their public key; the .pub next to
// them does.
func TestDialLegacyPEMKeyUsesPubFile(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	//lint:ignore SA1019 the point is to read keys made by old tools
	block, err := x509.EncryptPEMBlock(rand.Reader, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(priv), []byte("phrase"), x509.PEMCipherAES256) //nolint:staticcheck
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "id_rsa")
	os.WriteFile(path, pem.EncodeToMemory(block), 0600)
	pub, _ := ssh.NewPublicKey(&priv.PublicKey)
	os.WriteFile(path+".pub", ssh.MarshalAuthorizedKey(pub), 0600)

	srv := sshtest.Start(t, sshtest.Options{AuthorizedKey: pub})
	p := &fakePrompter{Answer: "phrase"}
	d := seededDialer(t, srv, newCreds(secrets.NewMem(), p))
	d.Agent = agentWith(t, priv)
	mustDial(t, d, keyServer(srv, path))
	if n := p.count(PromptPassphrase); n != 0 {
		t.Fatalf("passphrase prompts = %d", n)
	}
}

func otherSigner(t *testing.T) ssh.Signer {
	s, err := ssh.NewSignerFromKey(otherKey(t))
	if err != nil {
		t.Fatal(err)
	}
	return s
}
