// Command tam-server is the shared Ticket Auction Manager database that
// several tam-client installations talk to in remote mode.
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"ticket-auction-manager/tam-go/internal/db"
	"ticket-auction-manager/tam-go/internal/env"
	"ticket-auction-manager/tam-go/internal/server"
	"ticket-auction-manager/tam-go/internal/store"
)

func main() {
	addr := flag.String("addr", ":8000", "address to listen on")
	flag.Parse()
	addrSet := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "addr" {
			addrSet = true
		}
	})
	// "dev" binds the loopback interface unless an address was given.
	if flag.Arg(0) == "dev" && !addrSet {
		*addr = "localhost:8000"
	}

	password := os.Getenv("TAM_PWD")
	if password == "" {
		password = "changeme"
		log.Print("WARNING: TAM_PWD is not set; the key-management password is \"changeme\". Set TAM_PWD before exposing this server.")
	}

	dataDir, err := env.DataDir()
	if err != nil {
		log.Fatal(err)
	}
	sqldb, err := db.Open(filepath.Join(dataDir, "tam-remote.db"))
	if err != nil {
		log.Fatal(err)
	}
	defer sqldb.Close()
	if err := db.Migrate(sqldb); err != nil {
		log.Fatal(err)
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           server.NewHandler(store.New(sqldb), password),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       2 * time.Minute,
	}
	log.Printf("tam-server listening on %s (data in %s)", *addr, dataDir)
	log.Fatal(srv.ListenAndServe())
}
