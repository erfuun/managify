// Package snippets stores named commands, optionally with an upload first,
// in snippets.yaml.
package snippets

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/erfuun/managify/internal/appdir"
	"github.com/erfuun/managify/internal/job"
)

type Upload struct {
	Local  string `yaml:"local"`
	Remote string `yaml:"remote"`
}

type Snippet struct {
	Name    string  `yaml:"name"`
	Upload  *Upload `yaml:"upload,omitempty"`
	Command string  `yaml:"command"`
	Sudo    bool    `yaml:"sudo,omitempty"`
}

type Store struct {
	Snippets []Snippet `yaml:"snippets"`
	path     string
}

// Load reads path. A missing file is an empty store. A file that does not
// parse is an error, and nothing is written back to it.
func Load(path string) (*Store, error) {
	s := &Store{path: path}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(b, s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

func (s *Store) Add(sn Snippet) error {
	sn, err := s.validate(sn, -1)
	if err != nil {
		return err
	}
	s.Snippets = append(s.Snippets, sn)
	return nil
}

func (s *Store) Update(oldName string, sn Snippet) error {
	i := s.index(oldName)
	if i < 0 {
		return fmt.Errorf("no snippet named %s", oldName)
	}
	sn, err := s.validate(sn, i)
	if err != nil {
		return err
	}
	s.Snippets[i] = sn
	return nil
}

func (s *Store) Delete(name string) error {
	i := s.index(name)
	if i < 0 {
		return fmt.Errorf("no snippet named %s", name)
	}
	s.Snippets = append(s.Snippets[:i], s.Snippets[i+1:]...)
	return nil
}

func (s *Store) Save() error {
	b, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	if err := appdir.EnsureDir(filepath.Dir(s.path)); err != nil {
		return err
	}
	return appdir.WriteFileAtomic(s.path, b, 0600)
}

func (sn Snippet) Spec() job.Spec {
	spec := job.Spec{Command: sn.Command, Sudo: sn.Sudo}
	if sn.Upload != nil {
		spec.Upload = &job.Upload{Local: sn.Upload.Local, Remote: sn.Upload.Remote}
	}
	return spec
}

// validate trims sn and checks it; self is its own index when updating.
// An upload with no local file is dropped.
func (s *Store) validate(sn Snippet, self int) (Snippet, error) {
	sn.Name = strings.TrimSpace(sn.Name)
	sn.Command = strings.TrimSpace(sn.Command)
	if sn.Upload != nil {
		u := Upload{Local: strings.TrimSpace(sn.Upload.Local), Remote: strings.TrimSpace(sn.Upload.Remote)}
		sn.Upload = &u
		if u.Local == "" {
			sn.Upload = nil
		}
	}
	switch {
	case sn.Name == "":
		return sn, errors.New("name is required")
	case sn.Command == "":
		return sn, errors.New("command is required")
	}
	if i := s.index(sn.Name); i >= 0 && i != self {
		return sn, fmt.Errorf("name already used: %s", sn.Name)
	}
	return sn, nil
}

func (s *Store) index(name string) int {
	for i, sn := range s.Snippets {
		if sn.Name == name {
			return i
		}
	}
	return -1
}
