package snippets

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/erfuun/managify/internal/job"
)

// The spec's example file.
const example = `snippets:
  - name: deploy
    upload: { local: C:\work\deploy.sh, remote: /tmp/deploy.sh }
    command: bash /tmp/deploy.sh
    sudo: true
  - name: disk usage
    command: df -h
`

var wantExample = []Snippet{
	{Name: "deploy", Upload: &Upload{Local: `C:\work\deploy.sh`, Remote: "/tmp/deploy.sh"}, Command: "bash /tmp/deploy.sh", Sudo: true},
	{Name: "disk usage", Command: "df -h"},
}

func writeExample(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "snippets.yaml")
	if err := os.WriteFile(p, []byte(example), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRoundTrip(t *testing.T) {
	p := writeExample(t)
	s, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s.Snippets, wantExample) {
		t.Fatalf("loaded %+v", s.Snippets)
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if !strings.HasPrefix(string(b), "snippets:\n") {
		t.Fatalf("saved =\n%s", b)
	}
	again, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again.Snippets, wantExample) {
		t.Fatalf("reloaded %+v", again.Snippets)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(p); st.Mode().Perm() != 0600 {
			t.Fatalf("mode %v", st.Mode().Perm())
		}
	}
}

func TestLoadMissingIsEmpty(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nope", "snippets.yaml")
	s, err := Load(p)
	if err != nil || len(s.Snippets) != 0 {
		t.Fatalf("%v %v", s, err)
	}
	if err := s.Add(Snippet{Name: "up", Command: "uptime"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal(err)
	}
}

func TestParseErrorNamesLine(t *testing.T) {
	p := filepath.Join(t.TempDir(), "snippets.yaml")
	os.WriteFile(p, []byte("snippets:\n  - name: [oops\n"), 0600)
	_, err := Load(p)
	if err == nil || !strings.Contains(err.Error(), p) || !strings.Contains(err.Error(), "line ") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidation(t *testing.T) {
	s, _ := Load(writeExample(t))
	cases := []struct {
		sn   Snippet
		want string
	}{
		{Snippet{Name: " ", Command: "x"}, "name is required"},
		{Snippet{Name: "deploy", Command: "x"}, "name already used: deploy"},
		{Snippet{Name: "new", Command: "  "}, "command is required"},
	}
	for _, c := range cases {
		if err := s.Add(c.sn); err == nil || err.Error() != c.want {
			t.Errorf("Add(%+v) = %v, want %q", c.sn, err, c.want)
		}
	}
	if len(s.Snippets) != 2 {
		t.Fatalf("a failed Add changed the store: %+v", s.Snippets)
	}
}

func TestAddTrimsAndDropsEmptyUpload(t *testing.T) {
	s, _ := Load(filepath.Join(t.TempDir(), "snippets.yaml"))
	if err := s.Add(Snippet{Name: " up ", Command: " uptime ", Upload: &Upload{}}); err != nil {
		t.Fatal(err)
	}
	if got := s.Snippets[0]; got.Name != "up" || got.Command != "uptime" || got.Upload != nil {
		t.Fatalf("%+v", got)
	}
}

func TestUpdateAndDelete(t *testing.T) {
	s, _ := Load(writeExample(t))
	if err := s.Update("disk usage", Snippet{Name: "deploy", Command: "df"}); err == nil || err.Error() != "name already used: deploy" {
		t.Fatalf("rename onto another = %v", err)
	}
	if err := s.Update("disk usage", Snippet{Name: "disk usage", Command: "df -hT"}); err != nil {
		t.Fatalf("keeping its own name = %v", err)
	}
	if err := s.Update("disk usage", Snippet{Name: "df", Command: "df -hT"}); err != nil {
		t.Fatal(err)
	}
	if s.Snippets[1].Name != "df" || s.Snippets[1].Command != "df -hT" {
		t.Fatalf("%+v", s.Snippets[1])
	}
	if err := s.Update("gone", Snippet{Name: "x", Command: "y"}); err == nil {
		t.Fatal("updating a missing snippet succeeded")
	}
	if err := s.Delete("deploy"); err != nil {
		t.Fatal(err)
	}
	if len(s.Snippets) != 1 || s.Snippets[0].Name != "df" {
		t.Fatalf("%+v", s.Snippets)
	}
	if err := s.Delete("deploy"); err == nil {
		t.Fatal("deleting twice succeeded")
	}
}

func TestSpec(t *testing.T) {
	got := wantExample[0].Spec()
	want := job.Spec{Command: "bash /tmp/deploy.sh", Sudo: true,
		Upload: &job.Upload{Local: `C:\work\deploy.sh`, Remote: "/tmp/deploy.sh"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Spec() = %+v", got)
	}
	if got := wantExample[1].Spec(); got.Upload != nil || got.Command != "df -h" || got.Sudo {
		t.Fatalf("Spec() = %+v", got)
	}
}
