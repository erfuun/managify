// Package inventory stores server profiles in servers.yaml. Secrets never live here.
package inventory

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"managify/internal/appdir"
)

type Auth string

const (
	AuthKey      Auth = "key"
	AuthPassword Auth = "password"
)

// SudoMode applies to key-auth servers; password-auth servers reuse the login password.
// The empty value means none.
type SudoMode string

const (
	SudoSaved SudoMode = "saved"
	SudoAsk   SudoMode = "ask"
	SudoNone  SudoMode = "none"
)

type Server struct {
	ID            string   `yaml:"id"`
	Name          string   `yaml:"name"`
	Host          string   `yaml:"host"`
	Port          int      `yaml:"port"`
	User          string   `yaml:"user"`
	Auth          Auth     `yaml:"auth"`
	KeyPath       string   `yaml:"key_path,omitempty"`
	SavePassword  bool     `yaml:"save_password,omitempty"`
	SudoPassword  SudoMode `yaml:"sudo_password,omitempty"`
	LastRemoteDir string   `yaml:"last_remote_dir,omitempty"`
}

func (s Server) Addr() string {
	return net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
}

// ConnKey identifies everything a live connection depends on. When it changes,
// a pooled connection for this server must not be reused.
func (s Server) ConnKey() string {
	return strings.Join([]string{s.ID, s.Host, strconv.Itoa(s.Port), s.User, string(s.Auth), s.KeyPath}, "|")
}

type Inventory struct {
	Servers []Server `yaml:"servers"`
	path    string
}

// Load reads path. A missing file is an empty inventory. A file that does not
// parse is an error, and nothing is written back to it.
func Load(path string) (*Inventory, error) {
	inv := &Inventory{path: path}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return inv, nil
	}
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(b, inv); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return inv, nil
}

// Add validates s, assigns an ID and the default port, and appends it.
func (inv *Inventory) Add(s Server) (Server, error) {
	s = normalize(s)
	s.ID = NewID()
	if err := inv.validate(s); err != nil {
		return Server{}, err
	}
	inv.Servers = append(inv.Servers, s)
	return s, nil
}

func (inv *Inventory) Update(s Server) error {
	i := inv.index(s.ID)
	if i < 0 {
		return fmt.Errorf("no server with id %s", s.ID)
	}
	s = normalize(s)
	if err := inv.validate(s); err != nil {
		return err
	}
	inv.Servers[i] = s
	return nil
}

func (inv *Inventory) Delete(id string) error {
	i := inv.index(id)
	if i < 0 {
		return fmt.Errorf("no server with id %s", id)
	}
	inv.Servers = append(inv.Servers[:i], inv.Servers[i+1:]...)
	return nil
}

func (inv *Inventory) Get(id string) (Server, bool) {
	if i := inv.index(id); i >= 0 {
		return inv.Servers[i], true
	}
	return Server{}, false
}

func (inv *Inventory) Save() error {
	b, err := yaml.Marshal(inv)
	if err != nil {
		return err
	}
	if err := appdir.EnsureDir(filepath.Dir(inv.path)); err != nil {
		return err
	}
	return appdir.WriteFileAtomic(inv.path, b, 0600)
}

// NewID returns 8 random lowercase hex characters. IDs are stable across
// renames, so they key the keychain entries.
func NewID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func (inv *Inventory) index(id string) int {
	for i, s := range inv.Servers {
		if s.ID == id {
			return i
		}
	}
	return -1
}

func normalize(s Server) Server {
	s.Name = strings.TrimSpace(s.Name)
	s.Host = strings.TrimSpace(s.Host)
	s.User = strings.TrimSpace(s.User)
	if s.Port == 0 {
		s.Port = 22
	}
	return s
}

func (inv *Inventory) validate(s Server) error {
	switch {
	case s.Name == "":
		return errors.New("name is required")
	case s.Host == "":
		return errors.New("host is required")
	case s.Port < 1 || s.Port > 65535:
		return errors.New("port must be 1-65535")
	case s.User == "":
		return errors.New("user is required")
	case s.Auth != AuthKey && s.Auth != AuthPassword:
		return errors.New("auth must be key or password")
	}
	// Names become directory names, and Windows paths ignore case.
	for _, o := range inv.Servers {
		if o.ID != s.ID && strings.EqualFold(o.Name, s.Name) {
			return fmt.Errorf("name already used: %s", s.Name)
		}
	}
	return nil
}
