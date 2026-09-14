package vault

import (
	"errors"
	"testing"
)

func TestFileCountQuotaRollsBackNewBlob(t *testing.T) {
	s := fixture(t)
	f := put(t, s, "alice", "seed.txt", "seed")
	_, err := s.db.Exec(`WITH RECURSIVE seq(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM seq WHERE n<999)
 INSERT INTO files(owner,name,hash,size,created_at) SELECT 'alice','copy',?,4,0 FROM seq`, f.Hash)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.Put(ctx, "alice", "new.txt", []byte("new content that should roll back"), epoch)
	if !errors.Is(err, ErrQuota) {
		t.Fatalf("quota error=%v", err)
	}
	st, err := s.Stats(ctx, "alice")
	if err != nil || st.Files != 1000 || st.Blobs != 1 || st.PhysicalBytes != 4 {
		t.Fatalf("rollback failed: %+v %v", st, err)
	}
}
