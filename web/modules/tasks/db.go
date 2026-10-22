package tasks

import (
	"database/sql"
	"os"
	"path/filepath"

	"nas-panel/common"
)

// openTasksDB 打开 <dataDir>/tasks.db（WAL，common.OpenSQLite 内置）。
func openTasksDB(dataDir string) (*sql.DB, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}
	return common.OpenSQLite(filepath.Join(dataDir, "tasks.db"))
}
