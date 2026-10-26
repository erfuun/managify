package sshx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"managify/internal/appdir"
	"managify/internal/inventory"
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
			signer, err := d.keySigner(ctx, s, pc)
			if err != nil {
				st.note(err)
				return nil, err
			}
			out = append(out, signer)
		}
		return out, nil
	}
	return []ssh.AuthMethod{ssh.PublicKeysCallback(signers)}
}

func (d *Dialer) agent() (agent.Agent, error) {
	if d.Agent != nil {
		return d.Agent()
	}
	return DefaultAgent()
}

func (d *Dialer) keySigner(ctx context.Context, s inventory.Server, pc *pausableConn) (ssh.Signer, error) {
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
	pc.pause()
	phrase, err := d.Creds.Passphrase(ctx, s, path)
	pc.resume()
	if err != nil {
		return nil, err
	}
	signer, err = ssh.ParsePrivateKeyWithPassphrase(pemBytes, []byte(phrase))
	if err != nil {
		d.Creds.ForgetPassphrase(path)
		return nil, &AuthError{Server: s, Method: inventory.AuthKey, PassphraseWrong: true}
	}
	return signer, nil
}
