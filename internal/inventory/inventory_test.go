package inventory

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func tmpPath(t *testing.T) string {
	return filepath.Join(t.TempDir(), "servers.yaml")
}

func web1() Server {
	return Server{Name: "web-1", Host: "203.0.113.10", User: "root", Auth: AuthKey}
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	inv, err := Load(tmpPath(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Servers) != 0 {
		t.Fatalf("got %d servers", len(inv.Servers))
	}
}

func TestAddAssignsIDAndDefaultPort(t *testing.T) {
	inv, _ := Load(tmpPath(t))
	s, err := inv.Add(web1())
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}$`).MatchString(s.ID) {
		t.Fatalf("id %q", s.ID)
	}
	if s.Port != 22 {
		t.Fatalf("port %d", s.Port)
	}
	if got, ok := inv.Get(s.ID); !ok || got != s {
		t.Fatalf("Get = %+v %v", got, ok)
	}
}

func TestValidation(t *testing.T) {
	cases := []struct {
		name string
		mod  func(*Server)
		want string
	}{
		{"no name", func(s *Server) { s.Name = "  " }, "name is required"},
		{"dup name", func(s *Server) { s.Name = "WEB-1" }, "name already used: WEB-1"},
		{"no host", func(s *Server) { s.Host = "" }, "host is required"},
		{"port high", func(s *Server) { s.Port = 70000 }, "port must be 1-65535"},
		{"port negative", func(s *Server) { s.Port = -1 }, "port must be 1-65535"},
		{"no user", func(s *Server) { s.User = "" }, "user is required"},
		{"bad auth", func(s *Server) { s.Auth = "token" }, "auth must be key or password"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			inv, _ := Load(tmpPath(t))
			if _, err := inv.Add(web1()); err != nil {
				t.Fatal(err)
			}
			s := Server{Name: "db-1", Host: "203.0.113.20", User: "admin", Auth: AuthPassword}
			c.mod(&s)
			_, err := inv.Add(s)
			if err == nil || err.Error() != c.want {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
			if len(inv.Servers) != 1 {
				t.Fatalf("invalid server was added")
			}
		})
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	p := tmpPath(t)
	inv, _ := Load(p)
	a, err := inv.Add(Server{Name: "web-1", Host: "203.0.113.10", Port: 22, User: "root", Auth: AuthKey, KeyPath: "~/.ssh/id_ed25519"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := inv.Add(Server{Name: "db-1", Host: "203.0.113.20", Port: 22, User: "admin", Auth: AuthPassword, SavePassword: true, LastRemoteDir: "/tmp/"})
	if err != nil {
		t.Fatal(err)
	}
	if err := inv.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Servers, []Server{a, b}) {
		t.Fatalf("round trip:\n got %+v\nwant %+v", got.Servers, []Server{a, b})
	}
	raw, _ := os.ReadFile(p)
	text := string(raw)
	if !strings.HasPrefix(text, "servers:") || !strings.Contains(text, "auth: password") {
		t.Fatalf("unexpected file:\n%s", text)
	}
	if regexp.MustCompile(`(?m)^\s*-?\s*password:`).MatchString(text) {
		t.Fatalf("file has a password field:\n%s", text)
	}
}

func TestLoadParseErrorNamesLine(t *testing.T) {
	p := tmpPath(t)
	bad := []byte("servers:\n  - name: [unclosed\n")
	os.WriteFile(p, bad, 0600)
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "servers.yaml") || !strings.Contains(err.Error(), "line") {
		t.Fatalf("error %q", err)
	}
	after, _ := os.ReadFile(p)
	if string(after) != string(bad) {
		t.Fatal("file changed after failed load")
	}
}

func TestUpdateKeepsIDAndRenames(t *testing.T) {
	inv, _ := Load(tmpPath(t))
	s, _ := inv.Add(web1())
	s.Name = "web-prod"
	if err := inv.Update(s); err != nil {
		t.Fatal(err)
	}
	got, ok := inv.Get(s.ID)
	if !ok || got.Name != "web-prod" || len(inv.Servers) != 1 {
		t.Fatalf("got %+v", inv.Servers)
	}
	// Renaming to its own name (any case) is not a duplicate.
	s.Name = "WEB-PROD"
	if err := inv.Update(s); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateUnknownID(t *testing.T) {
	inv, _ := Load(tmpPath(t))
	s := web1()
	s.ID = "deadbeef"
	if err := inv.Update(s); err == nil || err.Error() != "no server with id deadbeef" {
		t.Fatalf("err = %v", err)
	}
}

func TestDeleteUnknownID(t *testing.T) {
	inv, _ := Load(tmpPath(t))
	s, _ := inv.Add(web1())
	if err := inv.Delete("deadbeef"); err == nil || err.Error() != "no server with id deadbeef" {
		t.Fatalf("err = %v", err)
	}
	if err := inv.Delete(s.ID); err != nil || len(inv.Servers) != 0 {
		t.Fatalf("delete: %v %d", err, len(inv.Servers))
	}
}

func TestAddrAndConnKey(t *testing.T) {
	s := Server{ID: "7f3a9c21", Host: "2001:db8::1", Port: 2222, User: "root", Auth: AuthKey, KeyPath: "k"}
	if s.Addr() != "[2001:db8::1]:2222" {
		t.Fatal(s.Addr())
	}
	t2 := s
	t2.User = "admin"
	if s.ConnKey() == t2.ConnKey() {
		t.Fatal("ConnKey ignores user")
	}
	t3 := s
	t3.Name, t3.LastRemoteDir = "other", "/opt/"
	if s.ConnKey() != t3.ConnKey() {
		t.Fatal("ConnKey depends on non-connection fields")
	}
}
