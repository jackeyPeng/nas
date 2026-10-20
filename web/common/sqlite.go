package common

import "database/sql"

// OpenSQLite opens a SQLite database file with WAL + busy_timeout enabled,
// so every panel SQLite database gets the same concurrency behavior instead
// of each module picking its own (or none, which is what caused SQLITE_BUSY
// under concurrent access).
//
// ⚠️ P2 审计发现：common/logger.go 和 common/eventbus.go 原有的 DSN 写法
// "?_journal_mode=WAL&_busy_timeout=5000" 对 modernc.org/sqlite v1.36.0
// 实测不生效（参数被静默忽略，sql.Open 不报错，但写锁冲突时立即返回
// SQLITE_BUSY，完全不等待）——用 t.TempDir() 沙箱内的最小复现测试验证过：
// 一个连接持有写事务未提交，另一个连接立刻 Exec 写入，<1ms 内就拿到
// "database is locked (5) (SQLITE_BUSY)"，而不是等到 busy_timeout 到期。
// modernc.org/sqlite 实际认的是 "_pragma=xxx(...)" 形式的 DSN 参数，不是
// mattn/go-sqlite3 那种 "_busy_timeout=" 平铺参数名。本函数改用
// "_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"，同样用沙箱内
// 最小复现测试验证过能正确等待后成功写入。本次顺带把 logger.go/eventbus.go
// 也迁到这个函数，不然三处 DSN 写法不一致、且其中两处的 WAL/busy_timeout
// 从未真正生效过（P0/P1 审计都没发现，这次排查 OpenSQLite 时连带挖出）。
//
// SetMaxOpenConns(1) is set because modernc.org/sqlite (pure-Go, no cgo)
// does not benefit from multiple connections writing concurrently — WAL
// still allows one writer at a time, and capping at 1 connection avoids
// the driver juggling multiple file handles/locks for no gain, matching
// logger.go's existing single-connection usage pattern.
//
// Callers that genuinely want an in-memory DB (":memory:") or that open
// their own test fixtures directly with database/sql should keep doing so;
// this helper is only for the on-disk, concurrently-accessed panel DBs.
func OpenSQLite(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}
