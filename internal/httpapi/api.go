package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"github.com/xr-xiaoran/pocketvault/internal/vault"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Keys map trusted owner labels to operator-provisioned API keys. Never trust an X-User header.
func Router(s *vault.Store, keys map[string]string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		if s.Ping(r.Context()) != nil {
			write(w, 503, map[string]string{"error": "database unavailable"})
			return
		}
		write(w, 200, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /s/{token}", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			write(w, 405, map[string]string{"error": "only GET consumes a ticket"})
			return
		}
		if r.Header.Get("Range") != "" {
			write(w, 416, map[string]string{"error": "range downloads are not supported"})
			return
		}
		f, data, err := s.Consume(r.Context(), r.PathValue("token"), time.Now())
		if err != nil {
			fail(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": f.Name}))
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.Header().Set("Cache-Control", "no-store, private")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		_, _ = w.Write(data)
	})
	mux.Handle("/api/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		owner := ""
		for label, key := range keys {
			if len(key) >= 16 && subtle.ConstantTimeCompare([]byte(token), []byte(key)) == 1 {
				owner = label
			}
		}
		if owner == "" {
			write(w, 401, map[string]string{"error": "unauthorized"})
			return
		}
		private := http.NewServeMux()
		private.HandleFunc("POST /api/files", func(w http.ResponseWriter, r *http.Request) {
			data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, vault.MaxBytes))
			if err != nil {
				write(w, 413, map[string]string{"error": "file exceeds 4 MiB"})
				return
			}
			f, dedup, err := s.Put(r.Context(), owner, r.URL.Query().Get("name"), data, time.Now())
			if err != nil {
				fail(w, err)
				return
			}
			write(w, 201, map[string]any{"file": f, "deduplicated": dedup})
		})
		private.HandleFunc("GET /api/files", func(w http.ResponseWriter, r *http.Request) {
			v, err := s.List(r.Context(), owner)
			if err != nil {
				fail(w, err)
				return
			}
			write(w, 200, v)
		})
		private.HandleFunc("GET /api/stats", func(w http.ResponseWriter, r *http.Request) {
			v, err := s.Stats(r.Context(), owner)
			if err != nil {
				fail(w, err)
				return
			}
			write(w, 200, v)
		})
		private.HandleFunc("DELETE /api/files/{id}", func(w http.ResponseWriter, r *http.Request) {
			id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
			if err != nil {
				fail(w, vault.ErrInvalid)
				return
			}
			if err = s.Delete(r.Context(), owner, id); err != nil {
				fail(w, err)
				return
			}
			w.WriteHeader(204)
		})
		private.HandleFunc("POST /api/files/{id}/shares", func(w http.ResponseWriter, r *http.Request) {
			id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
			if err != nil {
				fail(w, vault.ErrInvalid)
				return
			}
			var in struct {
				Downloads int `json:"downloads"`
				TTL       int `json:"ttl_seconds"`
			}
			d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
			d.DisallowUnknownFields()
			if d.Decode(&in) != nil || d.Decode(&struct{}{}) != io.EOF {
				fail(w, vault.ErrInvalid)
				return
			}
			sh, err := s.Share(r.Context(), owner, id, in.Downloads, in.TTL, time.Now())
			if err != nil {
				fail(w, err)
				return
			}
			write(w, 201, sh)
		})
		private.HandleFunc("GET /api/shares", func(w http.ResponseWriter, r *http.Request) {
			v, err := s.Shares(r.Context(), owner)
			if err != nil {
				fail(w, err)
				return
			}
			write(w, 200, v)
		})
		private.HandleFunc("DELETE /api/shares/{id}", func(w http.ResponseWriter, r *http.Request) {
			id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
			if err != nil {
				fail(w, vault.ErrInvalid)
				return
			}
			if err = s.Revoke(r.Context(), owner, id); err != nil {
				fail(w, err)
				return
			}
			w.WriteHeader(204)
		})
		private.ServeHTTP(w, r)
	}))
	return mux
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, err error) {
	status := 500
	message := "internal error"
	switch {
	case errors.Is(err, vault.ErrInvalid):
		status = 400
		message = err.Error()
	case errors.Is(err, vault.ErrNotFound):
		status = 404
		message = err.Error()
	case errors.Is(err, vault.ErrGone):
		status = 410
		message = err.Error()
	case errors.Is(err, vault.ErrQuota):
		status = 409
		message = err.Error()
	}
	write(w, status, map[string]string{"error": message})
}
