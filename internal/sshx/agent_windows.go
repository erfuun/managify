//go:build windows

package sshx

import (
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/crypto/ssh/agent"
)

// DefaultAgent connects to the Windows OpenSSH Authentication Agent service.
func DefaultAgent() (agent.Agent, error) {
	timeout := time.Second
	c, err := winio.DialPipe(`\\.\pipe\openssh-ssh-agent`, &timeout)
	if err != nil {
		return nil, err
	}
	return closingAgent{agent.NewClient(c), c}, nil
}
