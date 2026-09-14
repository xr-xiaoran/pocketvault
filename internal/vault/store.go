// Package vault implements tenant-scoped content-addressed storage and revocable tickets.
package vault

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	_ "modernc.org/sqlite"
	"strings"
	"time"
	"unicode/utf8"
)

const MaxBytes = 4 << 20

var (
	ErrInvalid  = errors.New("invalid input")
	ErrNotFound = errors.New("not found")
	ErrGone     = errors.New("share expired, revoked, or exhausted")
	ErrQuota    = errors.New("owner quota exceeded")
)

type Store struct{ db *sql.DB }
type File struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Hash    string `json:"sha256"`
	Size    int64  `json:"size"`
	Created int64  `json:"created_at"`
}
type Share struct {
	ID        int64  `json:"id"`
	Token     string `json:"token,omitempty"`
	FileID    int64  `json:"file_id"`
	Remaining int    `json:"remaining"`
	Expires   int64  `json:"expires_at"`
	Revoked   bool   `json:"revoked"`
}
type Stats struct {
	Files         int64 `json:"files"`
	Blobs         int64 `json:"blobs"`
	LogicalBytes  int64 `json:"logical_bytes"`
	PhysicalBytes int64 `json:"physical_bytes"`
	SavedBytes    int64 `json:"saved_bytes"`
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA busy_timeout=5000; PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;
 CREATE TABLE IF NOT EXISTS blobs(owner TEXT NOT NULL,hash TEXT NOT NULL,data BLOB NOT NULL,size INTEGER NOT NULL CHECK(size>0),PRIMARY KEY(owner,hash));
 CREATE TABLE IF NOT EXISTS files(id INTEGER PRIMARY KEY,owner TEXT NOT NULL,name TEXT NOT NULL,hash TEXT NOT NULL,size INTEGER NOT NULL,created_at INTEGER NOT NULL,
 FOREIGN KEY(owner,hash) REFERENCES blobs(owner,hash));
 CREATE INDEX IF NOT EXISTS files_owner_hash ON files(owner,hash);
 CREATE TABLE IF NOT EXISTS shares(id INTEGER PRIMARY KEY,file_id INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
 token_hash TEXT NOT NULL UNIQUE,remaining INTEGER NOT NULL CHECK(remaining>=0),expires_at INTEGER NOT NULL,revoked INTEGER NOT NULL DEFAULT 0 CHECK(revoked IN (0,1)));
 CREATE INDEX IF NOT EXISTS shares_file ON shares(file_id);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}
func (s *Store) Close() error                   { return s.db.Close() }
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }
func digest(data []byte) string                 { v := sha256.Sum256(data); return hex.EncodeToString(v[:]) }
func validName(name string) bool {
	return len(name) > 0 && len(name) <= 180 && utf8.ValidString(name) && !strings.ContainsAny(name, "/\\\r\n\x00") && strings.TrimSpace(name) != ""
}

// Put keeps bytes and metadata in the same database transaction: no orphan file window.
func (s *Store) Put(ctx context.Context, owner, name string, data []byte, now time.Time) (File, bool, error) {
	if owner == "" || !validName(name) || len(data) == 0 || len(data) > MaxBytes {
		return File{}, false, ErrInvalid
	}
	hash := digest(data)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return File{}, false, err
	}
	defer tx.Rollback()
	// First statement is a write, avoiding deferred read-to-write upgrade races.
	r, err := tx.ExecContext(ctx, `INSERT INTO blobs(owner,hash,data,size) VALUES(?,?,?,?) ON CONFLICT(owner,hash) DO NOTHING`, owner, hash, data, len(data))
	if err != nil {
		return File{}, false, err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return File{}, false, err
	}
	var total, count int64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(size),0) FROM blobs WHERE owner=?`, owner).Scan(&total); err != nil {
		return File{}, false, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM files WHERE owner=?`, owner).Scan(&count); err != nil {
		return File{}, false, err
	}
	if total > 64<<20 || count >= 1000 {
		return File{}, false, ErrQuota
	}
	r, err = tx.ExecContext(ctx, `INSERT INTO files(owner,name,hash,size,created_at) VALUES(?,?,?,?,?)`, owner, name, hash, len(data), now.Unix())
	if err != nil {
		return File{}, false, err
	}
	id, err := r.LastInsertId()
	if err != nil {
		return File{}, false, err
	}
	f := File{id, name, hash, int64(len(data)), now.Unix()}
	return f, n == 0, tx.Commit()
}
func (s *Store) List(ctx context.Context, owner string) ([]File, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,hash,size,created_at FROM files WHERE owner=? ORDER BY id DESC LIMIT 1000`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]File, 0)
	for rows.Next() {
		var f File
		if err = rows.Scan(&f.ID, &f.Name, &f.Hash, &f.Size, &f.Created); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) Stats(ctx context.Context, owner string) (Stats, error) {
	var st Stats
	err := s.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM files WHERE owner=?),
 (SELECT COUNT(*) FROM blobs WHERE owner=?),(SELECT COALESCE(SUM(size),0) FROM files WHERE owner=?),
 (SELECT COALESCE(SUM(size),0) FROM blobs WHERE owner=?)`, owner, owner, owner, owner).Scan(&st.Files, &st.Blobs, &st.LogicalBytes, &st.PhysicalBytes)
	st.SavedBytes = st.LogicalBytes - st.PhysicalBytes
	return st, err
}
func (s *Store) Delete(ctx context.Context, owner string, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var hash string
	err = tx.QueryRowContext(ctx, `DELETE FROM files WHERE id=? AND owner=? RETURNING hash`, id, owner).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM blobs WHERE owner=? AND hash=? AND NOT EXISTS(SELECT 1 FROM files WHERE owner=? AND hash=?)`, owner, hash, owner, hash)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Share(ctx context.Context, owner string, fileID int64, downloads, ttl int, now time.Time) (Share, error) {
	if downloads < 1 || downloads > 100 || ttl < 1 || ttl > 86400 {
		return Share{}, ErrInvalid
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return Share{}, err
	}
	token := hex.EncodeToString(tokenBytes)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Share{}, err
	}
	defer tx.Rollback()
	// INSERT SELECT binds authorization and creation to one statement.
	r, err := tx.ExecContext(ctx, `INSERT INTO shares(file_id,token_hash,remaining,expires_at)
 SELECT id,?,?,? FROM files WHERE id=? AND owner=?`, digest([]byte(token)), downloads, now.Unix()+int64(ttl), fileID, owner)
	if err != nil {
		return Share{}, err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return Share{}, err
	}
	if n != 1 {
		return Share{}, ErrNotFound
	}
	id, err := r.LastInsertId()
	if err != nil {
		return Share{}, err
	}
	return Share{id, token, fileID, downloads, now.Unix() + int64(ttl), false}, tx.Commit()
}
func (s *Store) Revoke(ctx context.Context, owner string, id int64) error {
	r, err := s.db.ExecContext(ctx, `UPDATE shares SET revoked=1 WHERE id=? AND file_id IN(SELECT id FROM files WHERE owner=?)`, id, owner)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrNotFound
	}
	return nil
}
func (s *Store) Shares(ctx context.Context, owner string) ([]Share, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT s.id,s.file_id,s.remaining,s.expires_at,s.revoked FROM shares s JOIN files f ON f.id=s.file_id WHERE f.owner=? ORDER BY s.id DESC LIMIT 100`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Share, 0)
	for rows.Next() {
		var sh Share
		if err = rows.Scan(&sh.ID, &sh.FileID, &sh.Remaining, &sh.Expires, &sh.Revoked); err != nil {
			return nil, err
		}
		out = append(out, sh)
	}
	return out, rows.Err()
}

// Consume spends one admitted GET, not one guaranteed completed network download.
// If the client disconnects after commit, the ticket is still spent (fail closed).
func (s *Store) Consume(ctx context.Context, token string, now time.Time) (File, []byte, error) {
	if len(token) != 64 {
		return File{}, nil, ErrGone
	}
	if _, err := hex.DecodeString(token); err != nil {
		return File{}, nil, ErrGone
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return File{}, nil, err
	}
	defer tx.Rollback()
	var id int64
	err = tx.QueryRowContext(ctx, `UPDATE shares SET remaining=remaining-1 WHERE token_hash=? AND revoked=0 AND expires_at>? AND remaining>0 RETURNING file_id`, digest([]byte(token)), now.Unix()).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return File{}, nil, ErrGone
	}
	if err != nil {
		return File{}, nil, err
	}
	var f File
	var data []byte
	err = tx.QueryRowContext(ctx, `SELECT f.id,f.name,f.hash,f.size,f.created_at,b.data FROM files f JOIN blobs b ON b.owner=f.owner AND b.hash=f.hash WHERE f.id=?`, id).Scan(&f.ID, &f.Name, &f.Hash, &f.Size, &f.Created, &data)
	if err != nil {
		return File{}, nil, fmt.Errorf("load admitted file: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return File{}, nil, err
	}
	return f, data, nil
}
