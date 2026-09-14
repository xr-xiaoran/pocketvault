package httpapi

import (
	"encoding/json"
	"github.com/xr-xiaoran/pocketvault/internal/vault"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestHTTPUploadShareHeadDownloadExhaustion(t *testing.T) {
	s, e := vault.Open(filepath.Join(t.TempDir(), "api.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	key := "alice-test-key-123456"
	server := httptest.NewServer(Router(s, map[string]string{"alice": key, "bob": "bob-test-key-123456"}))
	defer server.Close()
	call := func(method, path, body, token string) (int, []byte, http.Header) {
		t.Helper()
		r, _ := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		res, e := server.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		data, _ := io.ReadAll(res.Body)
		return res.StatusCode, data, res.Header
	}
	if code, _, _ := call("GET", "/api/files", "", ""); code != 401 {
		t.Fatal(code)
	}
	code, data, _ := call("POST", "/api/files?name=hello.txt", "hello vault", key)
	if code != 201 {
		t.Fatal(code, string(data))
	}
	var created struct {
		File vault.File `json:"file"`
	}
	if e = json.Unmarshal(data, &created); e != nil {
		t.Fatal(e)
	}
	path := "/api/files/" + strconv.FormatInt(created.File.ID, 10) + "/shares"
	if code, _, _ = call("POST", path, `{"downloads":1,"ttl_seconds":60}`, "bob-test-key-123456"); code != 404 {
		t.Fatal(code)
	}
	code, data, _ = call("POST", path, `{"downloads":1,"ttl_seconds":60}`, key)
	if code != 201 {
		t.Fatal(code, string(data))
	}
	var ticket vault.Share
	if e = json.Unmarshal(data, &ticket); e != nil {
		t.Fatal(e)
	}
	if code, _, _ = call("HEAD", "/s/"+ticket.Token, "", ""); code != 405 {
		t.Fatal(code)
	}
	code, data, h := call("GET", "/s/"+ticket.Token, "", "")
	if code != 200 || string(data) != "hello vault" || h.Get("Content-Disposition") == "" || h.Get("Cache-Control") != "no-store, private" {
		t.Fatal(code, string(data), h)
	}
	if code, _, _ = call("GET", "/s/"+ticket.Token, "", ""); code != 410 {
		t.Fatal(code)
	}
	if code, _, _ = call("POST", "/api/files?name=large", strings.Repeat("x", vault.MaxBytes+1), key); code != 413 {
		t.Fatal(code)
	}
}
