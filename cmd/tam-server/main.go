// Command tam-server is the shared Ticket Auction Manager database that
// several tam-client installations talk to in remote mode.
//
//go:generate go-winres simply --icon icon.ico --manifest cli --arch amd64 --product-name "Ticket Auction Manager" --file-description "Ticket Auction Manager server" --original-filename tam-server.exe --file-version 0.0.1 --product-version 0.0.1 --copyright "Copyright (c) 2026 Dilan Gilluly. MIT License." --out rsrc
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
	"ticket-auction-manager/tam-go/internal/tlscert"
)

func main() {
	addr := flag.String("addr", "", "address to listen on (default :8000, or :8443 with -tls)")
	useTLS := flag.Bool("tls", false, "serve HTTPS; a self-signed certificate is created in the data directory when none is given")
	certFile := flag.String("cert", "", "TLS certificate file (default <data dir>/server.crt)")
	keyFile := flag.String("key", "", "TLS key file (default <data dir>/server.key)")
	flag.Parse()

	// "dev" binds the loopback interface unless an address was given.
	if *addr == "" {
		host := ""
		if flag.Arg(0) == "dev" {
			host = "localhost"
		}
		if *useTLS {
			*addr = host + ":8443"
		} else {
			*addr = host + ":8000"
		}
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

	if !*useTLS {
		log.Printf("tam-server listening on http://%s (data in %s)", *addr, dataDir)
		log.Fatal(srv.ListenAndServe())
	}

	if *certFile == "" {
		*certFile = filepath.Join(dataDir, "server.crt")
	}
	if *keyFile == "" {
		*keyFile = filepath.Join(dataDir, "server.key")
	}
	hostname, _ := os.Hostname()
	created, err := tlscert.EnsurePair(*certFile, *keyFile, []string{"localhost", hostname, "127.0.0.1", "::1"})
	if err != nil {
		log.Fatal(err)
	}
	if created {
		log.Printf("created a self-signed certificate at %s (clients with Remote TLS on accept it)", *certFile)
	}
	log.Printf("tam-server listening on https://%s (data in %s)", *addr, dataDir)
	log.Fatal(srv.ListenAndServeTLS(*certFile, *keyFile))
}
