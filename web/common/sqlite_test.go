package common

import (
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
)

// TestOpenSQLiteConcurrentWrites 验证 OpenSQLite 打开的两个连接在并发写入时
// 不会报 SQLITE_BUSY：WAL + busy_timeout=5000 应让第二个写者排队等待而不是
// 立即失败。裸 sql.Open 不带这两个参数在高并发下会报 "database is locked"。
func TestOpenSQLiteConcurrentWrites(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "concurrent.db")

	db1, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatalf("OpenSQLite db1: %v", err)
	}
	defer db1.Close()

	db2, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatalf("OpenSQLite db2: %v", err)
	}
	defer db2.Close()

	if _, err := db1.Exec(`CREATE TABLE IF NOT EXISTS t (id INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	const n = 50
	var wg sync.WaitGroup
	errs := make(chan error, n*2)

	writer := func(db *sql.DB, prefix string) {
		defer wg.Done()
		for i := 0; i < n; i++ {
			if _, err := db.Exec(`INSERT INTO t (v) VALUES (?)`, prefix); err != nil {
				errs <- err
			}
		}
	}

	wg.Add(2)
	go writer(db1, "a")
	go writer(db2, "b")
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("并发写入报错（期望 WAL+busy_timeout 消化掉锁等待）: %v", err)
	}

	var count int
	if err := db1.QueryRow(`SELECT COUNT(*) FROM t`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != n*2 {
		t.Errorf("写入总数 = %d, want %d", count, n*2)
	}
}
