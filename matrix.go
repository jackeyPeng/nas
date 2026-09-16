package main

import (
	"database/sql"
	"fmt"
)

func buildPermissionMatrix() (matrix map[string]map[string]string, err error) {
	matrix = make(map[string]map[string]string)

	// 从 folders.db 读取权限信息
	db, err := sql.Open("sqlite3", "folders.db")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query("SELECT path, permission, valid_users, write_users, samba_share, nfs_export, webdav_export, s3_export FROM folders")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var path, permission, validUsers, writeUsers, sambaShare, nfsExport, webdavExport, s3Export string
		if err := rows.Scan(&path, &permission, &validUsers, &writeUsers, &sambaShare, &nfsExport, &webdavExport, &s3Export); err != nil {
			return nil, err
		}

		matrix[path] = map[string]string{
			"SMB":       fmt.Sprintf("valid users: %s, write users: %s", validUsers, writeUsers),
			"NFS":       fmt.Sprintf("export: %s", nfsExport),
			"WebDAV":    fmt.Sprintf("export: %s", webdavExport),
			"S3":        fmt.Sprintf("export: %s", s3Export),
		}
	}

	return matrix, nil
}
