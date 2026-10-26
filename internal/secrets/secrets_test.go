package secrets

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/zalando/go-keyring"
)

func exerciseStore(t *testing.T, s Store) {
	t.Helper()
	if _, err := s.Get("7f3a9c21"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get missing: %v", err)
	}
	if err := s.Set("7f3a9c21", "hunter2"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Get("7f3a9c21"); err != nil || got != "hunter2" {
		t.Fatalf("Get = %q, %v", got, err)
	}
	if err := s.Delete("7f3a9c21"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("7f3a9c21"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after delete: %v", err)
	}
	if err := s.Delete("never-set"); err != nil {
		t.Fatalf("Delete missing: %v", err)
	}
}

func TestMemStore(t *testing.T) {
	exerciseStore(t, NewMem())
}

// Not parallel: MockInit swaps a package-global provider.
func TestKeyringStoreWithMock(t *testing.T) {
	keyring.MockInit()
	exerciseStore(t, NewKeyring())
}

func TestAccounts(t *testing.T) {
	if LoginAccount("7f3a9c21") != "7f3a9c21" {
		t.Fatal(LoginAccount("7f3a9c21"))
	}
	if SudoAccount("7f3a9c21") != "7f3a9c21:sudo" {
		t.Fatal(SudoAccount("7f3a9c21"))
	}
}

func TestSession(t *testing.T) {
	s := NewSession()
	if _, ok := s.Get("a"); ok {
		t.Fatal("empty session has a value")
	}
	s.Set("a", "1")
	if v, ok := s.Get("a"); !ok || v != "1" {
		t.Fatalf("Get = %q %v", v, ok)
	}
	s.Forget("a")
	if _, ok := s.Get("a"); ok {
		t.Fatal("Forget did not forget")
	}
}

func TestSessionConcurrent(t *testing.T) {
	s := NewSession()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			k := fmt.Sprint(i % 5)
			s.Set(k, k)
			s.Get(k)
			s.Forget(k)
		}(i)
	}
	wg.Wait()
}
