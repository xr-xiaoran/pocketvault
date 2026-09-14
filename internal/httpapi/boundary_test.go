package httpapi

import (
	"context"
	"github.com/xr-xiaoran/pocketvault/internal/vault"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestRawKeyWithoutBearerIsRejected(t *testing.T) {
	key := "local-test-only-key-12345"
	req := httptest.NewRequest(http.MethodGet, "/api/files", nil)
	req.Header.Set("Authorization", key)
	w := httptest.NewRecorder()
	Router(nil, map[string]string{"alice": key}).ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("bare key accepted: %d", w.Code)
	}
}

func TestRangeDoesNotConsumeTicket(t *testing.T) {
	s, err := vault.Open(filepath.Join(t.TempDir(), "range.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now()
	f, _, err := s.Put(context.Background(), "alice", "a.txt", []byte("abc"), now)
	if err != nil {
		t.Fatal(err)
	}
	sh, err := s.Share(context.Background(), "alice", f.ID, 1, 60, now)
	if err != nil {
		t.Fatal(err)
	}
	h := Router(s, map[string]string{"alice": "test-only-key-12345"})
	req := httptest.NewRequest("GET", "/s/"+sh.Token, nil)
	req.Header.Set("Range", "bytes=0-1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 416 {
		t.Fatalf("range status=%d", w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/s/"+sh.Token, nil))
	if w.Code != 200 || w.Body.String() != "abc" {
		t.Fatalf("range spent ticket: %d %s", w.Code, w.Body.String())
	}
}
