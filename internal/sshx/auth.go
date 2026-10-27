package sshx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/erfuun/managify/internal/appdir"
	"github.com/erfuun/managify/internal/inventory"
)

// closingAgent lets Dial close the agent connection once the handshake is over.
type closingAgent struct {
	agent.ExtendedAgent
	io.Closer
}

// dialState carries what the auth callbacks learned, because x/crypto does
// not reliably pass their errors back through the handshake error.
type dialState struct {
	err    error // AuthError with PassphraseWrong, a key file error, or ErrPromptCancelled
	agents []io.Closer
}

func (st *dialState) note(err error) {
	if st.err == nil {
		st.err = err
	}
}

func (d *Dialer) authMethods(ctx context.Context, s inventory.Server, pc *pausableConn, st *dialState) []ssh.AuthMethod {
	if s.Auth == inventory.AuthPassword {
		password := func() (string, error) {
			// A cancelled prompt ends the dial; keyboard-interactive must not ask again.
			if errors.Is(st.err, ErrPromptCancelled) {
				return "", st.err
			}
			// Paused around the whole lookup: it may wait on another dial's prompt.
			pc.pause()
			defer pc.resume()
			pw, err := d.Creds.LoginPassword(ctx, s)
			if err != nil {
				st.note(err)
			}
			return pw, err
		}
		return []ssh.AuthMethod{
			ssh.PasswordCallback(password),
			ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
				if len(questions) == 0 {
					return nil, nil
				}
				pw, err := password()
				if err != nil {
					return nil, err
				}
				answers := make([]string, len(questions))
				for i := range answers {
					answers[i] = pw
				}
				return answers, nil
			}),
		}
	}
	// x/crypto calls this once and asks the server about each key before
	// signing with it, so the key file goes last and is decrypted only if
	// the server wants it and the agent does not already hold it.
	signers := func() ([]ssh.Signer, error) {
		var out []ssh.Signer
		if ag, err := d.agent(); err == nil && ag != nil {
			if c, ok := ag.(io.Closer); ok {
				st.agents = append(st.agents, c)
			}
			if ss, err := ag.Signers(); err == nil {
				out = append(out, ss...)
			}
		}
		if s.KeyPath != "" {
			signer, err := d.keyFileSigner(ctx, s, pc, st)
			switch {
			case err != nil:
				st.note(err)
				if len(out) == 0 {
					return nil, err
				}
			case !holds(out, signer.PublicKey()):
				out = append(out, signer)
			}
		}
		return out, nil
	}
	return []ssh.AuthMethod{ssh.PublicKeysCallback(signers)}
}

func holds(signers []ssh.Signer, pub ssh.PublicKey) bool {
	for _, s := range signers {
		if bytes.Equal(s.PublicKey().Marshal(), pub.Marshal()) {
			return true
		}
	}
	return false
}

// keyFileSigner reads the key file. An encrypted key becomes a lazyKey when
// its public half is known (OpenSSH format, or a .pub beside an old PEM key);
// otherwise its passphrase is asked now.
func (d *Dialer) keyFileSigner(ctx context.Context, s inventory.Server, pc *pausableConn, st *dialState) (ssh.Signer, error) {
	path := appdir.ExpandHome(s.KeyPath)
	pemBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading key %s: %w", path, err)
	}
	signer, err := ssh.ParsePrivateKey(pemBytes)
	var missing *ssh.PassphraseMissingError
	if !errors.As(err, &missing) {
		return signer, err
	}
	pub := missing.PublicKey
	if pub == nil {
		if b, err := os.ReadFile(path + ".pub"); err == nil {
			pub, _, _, _, _ = ssh.ParseAuthorizedKey(b)
		}
	}
	decrypt := func() (ssh.Signer, error) { return d.decryptKey(ctx, s, pc, path, pemBytes) }
	if pub == nil {
		return decrypt()
	}
	return &lazyKey{pub: pub, load: func() (ssh.Signer, error) {
		signer, err := decrypt()
		if err != nil {
			st.note(err) // keeps "Wrong key passphrase." / "Cancelled." over the handshake's own error
		}
		return signer, err
	}}, nil
}

func (d *Dialer) decryptKey(ctx context.Context, s inventory.Server, pc *pausableConn, path string, pemBytes []byte) (ssh.Signer, error) {
	pc.pause()
	phrase, err := d.Creds.Passphrase(ctx, s, path)
	pc.resume()
	if err != nil {
		return nil, err
	}
	signer, err := ssh.ParsePrivateKeyWithPassphrase(pemBytes, []byte(phrase))
	if err != nil {
		d.Creds.ForgetPassphrase(path)
		return nil, &AuthError{Server: s, Method: inventory.AuthKey, PassphraseWrong: true}
	}
	return signer, nil
}

// lazyKey is an encrypted key file offered by its public key alone. The
// private key is decrypted, asking for the passphrase, on the first Sign,
// which happens only after the server has said it accepts the key. As an
// AlgorithmSigner, an RSA key still signs with rsa-sha2-*.
type lazyKey struct {
	pub  ssh.PublicKey
	load func() (ssh.Signer, error)
	once sync.Once
	s    ssh.Signer
	err  error
}

func (k *lazyKey) PublicKey() ssh.PublicKey { return k.pub }

func (k *lazyKey) signer() (ssh.Signer, error) {
	k.once.Do(func() { k.s, k.err = k.load() })
	return k.s, k.err
}

func (k *lazyKey) Sign(rand io.Reader, data []byte) (*ssh.Signature, error) {
	return k.SignWithAlgorithm(rand, data, "")
}

func (k *lazyKey) SignWithAlgorithm(rand io.Reader, data []byte, algorithm string) (*ssh.Signature, error) {
	s, err := k.signer()
	if err != nil {
		return nil, err
	}
	if as, ok := s.(ssh.AlgorithmSigner); ok {
		return as.SignWithAlgorithm(rand, data, algorithm)
	}
	if algorithm != "" && algorithm != k.pub.Type() {
		return nil, fmt.Errorf("ssh: key does not support algorithm %s", algorithm)
	}
	return s.Sign(rand, data)
}

func (d *Dialer) agent() (agent.Agent, error) {
	if d.Agent != nil {
		return d.Agent()
	}
	return DefaultAgent()
}
