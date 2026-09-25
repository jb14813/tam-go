// Command tam-client serves the Ticket Auction Manager web app and its API
// on a venue laptop, against a local database or a remote tam-server.
package main

import (
	"embed"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"path/filepath"
	"time"

	"ticket-auction-manager/tam-go/internal/client"
	"ticket-auction-manager/tam-go/internal/db"
	"ticket-auction-manager/tam-go/internal/env"
	"ticket-auction-manager/tam-go/internal/store"
)

//go:embed all:dist
var distFS embed.FS

func main() {
	addr := flag.String("addr", "localhost:3080", "address to listen on")
	flag.Parse()

	dataDir, err := env.DataDir()
	if err != nil {
		log.Fatal(err)
	}
	sqldb, err := db.Open(filepath.Join(dataDir, "tam-local.db"))
	if err != nil {
		log.Fatal(err)
	}
	defer sqldb.Close()
	if err := db.Migrate(sqldb); err != nil {
		log.Fatal(err)
	}
	dist, err := fs.Sub(distFS, "dist")
	if err != nil {
		log.Fatal(err)
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           client.NewHandler(store.New(sqldb), filepath.Join(dataDir, "settings.json"), dist),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("tam-client listening on http://%s/ (data in %s)", *addr, dataDir)
	log.Fatal(srv.ListenAndServe())
}
