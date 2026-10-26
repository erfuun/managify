//go:build !windows

package sshx

import (
	"errors"
	"net"
	"os"

	"golang.org/x/crypto/ssh/agent"
)

// DefaultAgent connects to the agent named by SSH_AUTH_SOCK.
func DefaultAgent() (agent.Agent, error) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil, errors.New("SSH_AUTH_SOCK not set")
	}
	c, err := net.Dial("unix", sock)
	if err != nil {
		return nil, err
	}
	return closingAgent{agent.NewClient(c), c}, nil
}
