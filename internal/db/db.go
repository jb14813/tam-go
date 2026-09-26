package db

import (
	"database/sql"
	"ticket-auction-manager/tam-go/internal/env"

	_ "modernc.org/sqlite"
)

func CreateDBConn() *sql.DB {
	dbPath := env.GetDBPath()
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		panic(err)
	}
	return conn
}

func InitDB() {
	Daemon := env.GetDaemon()
	conn := CreateDBConn()
	defer conn.Close()
	tx, err := conn.Begin()
	if err != nil {
		panic(err)
	}
	defer tx.Rollback()
	if Daemon == "Server" {
		tx.Exec(`CREATE TABLE IF NOT EXISTS auth_keys (
			auth_key TEXT,
			description TEXT,
			PRIMARY KEY (auth_key))`)
	}
	tx.Exec(`CREATE TABLE IF NOT EXISTS prefixes (
		prefix TEXT,
		color TEXT,
		weight INT,
		PRIMARY KEY (prefix))`)
	tx.Commit()
}
