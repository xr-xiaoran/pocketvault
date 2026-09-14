package vault

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

var ctx = context.Background()
var epoch = time.Unix(1700000000, 0)

func fixture(t *testing.T) *Store {
	t.Helper()
	s, e := Open(filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func put(t *testing.T, s *Store, owner, name, text string) File {
	t.Helper()
	f, _, e := s.Put(ctx, owner, name, []byte(text), epoch)
	if e != nil {
		t.Fatal(e)
	}
	return f
}
func share(t *testing.T, s *Store, f File, n int) Share {
	t.Helper()
	v, e := s.Share(ctx, "alice", f.ID, n, 60, epoch)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func TestDedupAndReferenceCleanup(t *testing.T) {
	s := fixture(t)
	a := put(t, s, "alice", "a.txt", "same bytes")
	b, dedup, e := s.Put(ctx, "alice", "b.txt", []byte("same bytes"), epoch)
	if e != nil || !dedup || a.ID == b.ID {
		t.Fatal(b, dedup, e)
	}
	st, e := s.Stats(ctx, "alice")
	if e != nil || st.Files != 2 || st.Blobs != 1 || st.SavedBytes != 10 {
		t.Fatal(st, e)
	}
	if e = s.Delete(ctx, "alice", a.ID); e != nil {
		t.Fatal(e)
	}
	ticket := share(t, s, b, 1)
	_, data, e := s.Consume(ctx, ticket.Token, epoch)
	if e != nil || string(data) != "same bytes" {
		t.Fatal(string(data), e)
	}
	if e = s.Delete(ctx, "alice", b.ID); e != nil {
		t.Fatal(e)
	}
	st, e = s.Stats(ctx, "alice")
	if e != nil || st.Files != 0 || st.Blobs != 0 {
		t.Fatal(st, e)
	}
}
func TestOwnershipIsolation(t *testing.T) {
	s := fixture(t)
	f := put(t, s, "alice", "a.txt", "hello")
	if e := s.Delete(ctx, "bob", f.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	if _, e := s.Share(ctx, "bob", f.ID, 1, 60, epoch); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	sh := share(t, s, f, 1)
	if e := s.Revoke(ctx, "bob", sh.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	items, e := s.List(ctx, "bob")
	if e != nil || len(items) != 0 {
		t.Fatal(items, e)
	}
	_, dedup, e := s.Put(ctx, "bob", "copy.txt", []byte("hello"), epoch)
	if e != nil || dedup {
		t.Fatal("must not expose cross-owner dedup", dedup, e)
	}
}
func TestOneShotAndNoRawTokenAtRest(t *testing.T) {
	s := fixture(t)
	f := put(t, s, "alice", "a.txt", "abc")
	sh := share(t, s, f, 1)
	var stored string
	if e := s.db.QueryRow(`SELECT token_hash FROM shares WHERE id=?`, sh.ID).Scan(&stored); e != nil {
		t.Fatal(e)
	}
	if stored == sh.Token || stored != digest([]byte(sh.Token)) {
		t.Fatal("raw ticket stored")
	}
	if _, _, e := s.Consume(ctx, sh.Token, epoch); e != nil {
		t.Fatal(e)
	}
	if _, _, e := s.Consume(ctx, sh.Token, epoch); !errors.Is(e, ErrGone) {
		t.Fatal(e)
	}
}
func TestExpiredAtExactBoundary(t *testing.T) {
	s := fixture(t)
	f := put(t, s, "alice", "a.txt", "abc")
	sh := share(t, s, f, 1)
	if _, _, e := s.Consume(ctx, sh.Token, epoch.Add(60*time.Second)); !errors.Is(e, ErrGone) {
		t.Fatal(e)
	}
}
func TestRevokeIsIdempotentAndDeniesDownload(t *testing.T) {
	s := fixture(t)
	f := put(t, s, "alice", "a.txt", "abc")
	sh := share(t, s, f, 3)
	for i := 0; i < 2; i++ {
		if e := s.Revoke(ctx, "alice", sh.ID); e != nil {
			t.Fatal(e)
		}
	}
	if _, _, e := s.Consume(ctx, sh.Token, epoch); !errors.Is(e, ErrGone) {
		t.Fatal(e)
	}
}
func TestDeleteInvalidatesShares(t *testing.T) {
	s := fixture(t)
	f := put(t, s, "alice", "a.txt", "abc")
	sh := share(t, s, f, 3)
	if e := s.Delete(ctx, "alice", f.ID); e != nil {
		t.Fatal(e)
	}
	if _, _, e := s.Consume(ctx, sh.Token, epoch); !errors.Is(e, ErrGone) {
		t.Fatal(e)
	}
}
func TestConcurrentLimitedTicketsAcrossConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.db")
	a, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	b, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	f := put(t, a, "alice", "a.txt", "abc")
	sh := share(t, a, f, 3)
	var wg sync.WaitGroup
	results := make(chan error, 40)
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := a
			if i%2 == 0 {
				s = b
			}
			_, _, e := s.Consume(ctx, sh.Token, epoch)
			results <- e
		}(i)
	}
	wg.Wait()
	close(results)
	ok := 0
	for e := range results {
		if e == nil {
			ok++
		} else if !errors.Is(e, ErrGone) {
			t.Fatal(e)
		}
	}
	if ok != 3 {
		t.Fatalf("want exactly 3 successful admissions, got %d", ok)
	}
}
func TestInvalidNamesAndSizes(t *testing.T) {
	s := fixture(t)
	for _, name := range []string{"", "../secret", "a\\b", "a\r\nheader", "\x00"} {
		if _, _, e := s.Put(ctx, "alice", name, []byte("abc"), epoch); !errors.Is(e, ErrInvalid) {
			t.Fatalf("name=%q err=%v", name, e)
		}
	}
	for _, data := range [][]byte{nil, make([]byte, MaxBytes+1)} {
		if _, _, e := s.Put(ctx, "alice", "a", data, epoch); !errors.Is(e, ErrInvalid) {
			t.Fatal(e)
		}
	}
}
func TestInvalidShareLimits(t *testing.T) {
	s := fixture(t)
	f := put(t, s, "alice", "a.txt", "abc")
	for _, v := range [][2]int{{0, 60}, {101, 60}, {1, 0}, {1, 86401}} {
		if _, e := s.Share(ctx, "alice", f.ID, v[0], v[1], epoch); !errors.Is(e, ErrInvalid) {
			t.Fatal(e)
		}
	}
}
func TestPersistenceAfterReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "durable.db")
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	f := put(t, s, "alice", "a.txt", "hello")
	sh := share(t, s, f, 1)
	s.Close()
	s, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	_, data, e := s.Consume(ctx, sh.Token, epoch)
	if e != nil || string(data) != "hello" {
		t.Fatal(string(data), e)
	}
}
func TestUnknownTicket(t *testing.T) {
	s := fixture(t)
	if _, _, e := s.Consume(ctx, "not-a-ticket", epoch); !errors.Is(e, ErrGone) {
		t.Fatal(e)
	}
}
