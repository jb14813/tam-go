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
	conn := CreateDBConn()
	defer conn.Close()
	conn.Exec(`CREATE TABLE IF NOT EXISTS prefixes (
		prefix VARCHAR(100),
		color VARCHAR(100),
		weight INT,
		PRIMARY KEY (prefix))`)
}
