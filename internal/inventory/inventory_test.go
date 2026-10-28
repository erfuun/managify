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

// jumpInv holds web-1, plus db-1 and db-2 jumping through it.
func jumpInv(t *testing.T) (*Inventory, Server, Server, Server) {
	t.Helper()
	inv, _ := Load(tmpPath(t))
	web, err := inv.Add(web1())
	if err != nil {
		t.Fatal(err)
	}
	db1, err := inv.Add(Server{Name: "db-1", Host: "10.0.0.12", User: "postgres", Auth: AuthKey, Jump: web.ID})
	if err != nil {
		t.Fatal(err)
	}
	db2, err := inv.Add(Server{Name: "db-2", Host: "10.0.0.13", User: "postgres", Auth: AuthKey, Jump: web.ID})
	if err != nil {
		t.Fatal(err)
	}
	return inv, web, db1, db2
}

func TestJumpValidation(t *testing.T) {
	inv, web, db1, _ := jumpInv(t)
	app, err := inv.Add(Server{Name: "app-1", Host: "10.0.0.20", User: "root", Auth: AuthKey})
	if err != nil {
		t.Fatal(err)
	}
	solo, err := inv.Add(Server{Name: "solo", Host: "10.0.0.30", User: "root", Auth: AuthKey})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inv.Add(Server{Name: "cache", Host: "10.0.0.40", User: "root", Auth: AuthKey, Jump: solo.ID}); err != nil {
		t.Fatal(err)
	}
	hopped := web
	hopped.Jump = app.ID
	self := db1
	self.Jump = db1.ID
	missing := db1
	missing.Jump = "deadbeef"
	twoHop := app
	twoHop.Jump = db1.ID
	oneDependant := solo
	oneDependant.Jump = app.ID
	cases := []struct {
		name string
		s    Server
		want string
	}{
		{"missing", missing, "jump server not found"},
		{"self", self, "a server can't jump through itself"},
		{"jump has a jump", twoHop, "db-1 itself jumps through web-1; only one hop is supported"},
		{"two dependants", hopped, "db-1 and db-2 jump through this server, so it can't use a jump itself"},
		{"one dependant", oneDependant, "cache jumps through this server, so it can't use a jump itself"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := inv.Update(c.s)
			if err == nil || err.Error() != c.want {
				t.Fatalf("Update err = %v, want %q", err, c.want)
			}
		})
	}
}

func TestDeleteBlockedWhileJumpedThrough(t *testing.T) {
	inv, web, _, _ := jumpInv(t)
	err := inv.Delete(web.ID)
	want := "web-1 is the jump host for db-1, db-2. Change those first."
	if err == nil || err.Error() != want {
		t.Fatalf("Delete err = %v, want %q", err, want)
	}
	if len(inv.Servers) != 3 {
		t.Fatalf("%d servers left", len(inv.Servers))
	}
}

func TestJumpSurvivesRename(t *testing.T) {
	inv, web, db1, _ := jumpInv(t)
	web.Name = "edge"
	if err := inv.Update(web); err != nil {
		t.Fatal(err)
	}
	if got := inv.JumpName(db1); got != "edge" {
		t.Fatalf("JumpName = %q", got)
	}
	if got := inv.JumpName(web); got != "" {
		t.Fatalf("direct JumpName = %q", got)
	}
}

func TestJumpRoundTrip(t *testing.T) {
	inv, _, db1, _ := jumpInv(t)
	if err := inv.Save(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(inv.path)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), "jump:"); n != 2 {
		t.Fatalf("%d jump lines in\n%s", n, b)
	}
	back, err := Load(inv.path)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := back.Get(db1.ID); got.Jump != db1.Jump {
		t.Fatalf("Jump = %q, want %q", got.Jump, db1.Jump)
	}
}

func TestConnKeyIncludesJump(t *testing.T) {
	s := Server{ID: "7f3a9c21", Host: "10.0.0.12", Port: 22, User: "root", Auth: AuthKey}
	j := s
	j.Jump = "3fa07b10"
	if s.ConnKey() == j.ConnKey() {
		t.Fatal("ConnKey ignores Jump")
	}
}

func TestJumpOptions(t *testing.T) {
	inv, web, db1, _ := jumpInv(t)
	solo, err := inv.Add(Server{Name: "solo", Host: "10.0.0.30", User: "root", Auth: AuthKey})
	if err != nil {
		t.Fatal(err)
	}
	names := func(ss []Server) []string {
		var n []string
		for _, s := range ss {
			n = append(n, s.Name)
		}
		return n
	}
	if got := names(inv.JumpOptions(db1.ID)); !reflect.DeepEqual(got, []string{"web-1", "solo"}) {
		t.Fatalf("db-1 options %v", got)
	}
	if got := names(inv.JumpOptions(solo.ID)); !reflect.DeepEqual(got, []string{"web-1"}) {
		t.Fatalf("solo options %v", got)
	}
	if got := inv.JumpOptions(web.ID); len(got) != 0 {
		t.Fatalf("web-1 options %v", names(got))
	}
	if got := names(inv.JumpOptions("")); !reflect.DeepEqual(got, []string{"web-1", "solo"}) {
		t.Fatalf("new server options %v", got)
	}
}
