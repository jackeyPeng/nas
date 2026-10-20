package diskmgmt

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// newTestFoldersDB 建一个内存 sqlite，表结构与 ensureFoldersSchema 保持一致（仅测试用到的列）。
func newTestFoldersDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存 sqlite 失败: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`CREATE TABLE folders (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		path TEXT NOT NULL UNIQUE,
		pool TEXT NOT NULL,
		permission TEXT NOT NULL DEFAULT 'readwrite',
		valid_users TEXT NOT NULL DEFAULT '',
		write_users TEXT NOT NULL DEFAULT '',
		recycle_bin INTEGER NOT NULL DEFAULT 0,
		samba_share INTEGER NOT NULL DEFAULT 1,
		nfs_export INTEGER NOT NULL DEFAULT 0,
		nfs_no_root_squash INTEGER NOT NULL DEFAULT 0,
		quota_gb INTEGER NOT NULL DEFAULT 0
	)`)
	if err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	return db
}

func seedFolder(t *testing.T, db *sql.DB, name, path, pool string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO folders (name, path, pool) VALUES (?, ?, ?)`, name, path, pool)
	if err != nil {
		t.Fatalf("插入测试数据失败: %v", err)
	}
}

func TestDeleteFoldersByMountPrefix(t *testing.T) {
	db := newTestFoldersDB(t)
	seedFolder(t, db, "public", "/data/nas1/public", "/data/nas1")
	seedFolder(t, db, "fm", "/data/nas1/fm", "/data/nas1")
	seedFolder(t, db, "public", "/data/nas2/public", "/data/nas2")

	n, err := deleteFoldersByMountPrefix(db, "/data/nas1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 2 {
		t.Errorf("deleted rows = %d, want 2", n)
	}

	var remaining int
	if err := db.QueryRow("SELECT COUNT(*) FROM folders").Scan(&remaining); err != nil {
		t.Fatalf("查询剩余行失败: %v", err)
	}
	if remaining != 1 {
		t.Errorf("remaining rows = %d, want 1", remaining)
	}

	var pool string
	if err := db.QueryRow("SELECT pool FROM folders").Scan(&pool); err != nil {
		t.Fatalf("查询剩余行失败: %v", err)
	}
	if pool != "/data/nas2" {
		t.Errorf("remaining row pool = %q, want /data/nas2", pool)
	}
}

func TestDeleteFoldersByMountPrefixTrailingSlash(t *testing.T) {
	db := newTestFoldersDB(t)
	seedFolder(t, db, "public", "/data/nas1/public", "/data/nas1")

	// 挂载点带结尾斜杠也应能正确匹配
	n, err := deleteFoldersByMountPrefix(db, "/data/nas1/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 1 {
		t.Errorf("deleted rows = %d, want 1", n)
	}
}

func TestDeleteFoldersByMountPrefixNoMatch(t *testing.T) {
	db := newTestFoldersDB(t)
	seedFolder(t, db, "public", "/data/nas2/public", "/data/nas2")

	n, err := deleteFoldersByMountPrefix(db, "/data/nas1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Errorf("deleted rows = %d, want 0", n)
	}
}

func TestDeleteFoldersByMountPrefixEmpty(t *testing.T) {
	db := newTestFoldersDB(t)
	if _, err := deleteFoldersByMountPrefix(db, ""); err == nil {
		t.Error("expected error for empty mount point, got nil")
	}
}

// 兜底场景：pool 列脏数据（比如历史手动改过）但 path 前缀仍在挂载点下，也应能删除。
func TestDeleteFoldersByMountPrefixPathFallback(t *testing.T) {
	db := newTestFoldersDB(t)
	seedFolder(t, db, "legacy", "/data/nas1/legacy", "mismatched_pool_value")

	n, err := deleteFoldersByMountPrefix(db, "/data/nas1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 1 {
		t.Errorf("deleted rows = %d, want 1", n)
	}
}
