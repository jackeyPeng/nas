package main

import (
	"database/sql"
	"fmt"
)

func getFolderPermissionsFromDB(folderPath string) (permissions map[string]string, err error) {
	db, err := sql.Open("sqlite3", "folders.db")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	query := "SELECT permission, valid_users, write_users FROM folders WHERE path = ?"
	var permission, validUsers, writeUsers string
	err = db.QueryRow(query, folderPath).Scan(&permission, &validUsers, &writeUsers)
	if err != nil {
		return nil, err
	}

	permissions = map[string]string{
		"permission":    permission,
		"valid_users":   validUsers,
		"write_users":   writeUsers,
	}
	return permissions, nil
}
