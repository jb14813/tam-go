// Command tam-server is the shared Ticket Auction Manager database that
// several tam-client installations talk to in remote mode.
//
//go:generate go-winres simply --icon icon.ico --manifest cli --arch amd64 --product-name "Ticket Auction Manager" --file-description "Ticket Auction Manager server" --original-filename tam-server.exe --file-version 0.0.1 --product-version 0.0.1 --copyright "Copyright (c) 2026 Dilan Gilluly. MIT License." --out rsrc
package main

import (
	"context"
	_ "embed"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"ticket-auction-manager/tam-go/internal/db"
	"ticket-auction-manager/tam-go/internal/desktop"
	"ticket-auction-manager/tam-go/internal/env"
	"ticket-auction-manager/tam-go/internal/server"
	"ticket-auction-manager/tam-go/internal/store"
	"ticket-auction-manager/tam-go/internal/tlscert"
)

//go:embed icon.ico
var iconICO []byte

func main() {
	addr := flag.String("addr", "", "address to listen on (default :8000, or :8443 with -tls)")
	useTLS := flag.Bool("tls", false, "serve HTTPS; a self-signed certificate is created in the data directory when none is given")
	certFile := flag.String("cert", "", "TLS certificate file (default <data dir>/server.crt)")
	keyFile := flag.String("key", "", "TLS key file (default <data dir>/server.key)")
	useTray := flag.Bool("tray", desktop.TraySupported, "show a TAM icon in the notification area with a Shut Down entry (Windows)")
	flag.Parse()
	desktop.SetConsoleTitle("Ticket Auction Manager - server")

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

	// stop ends the program cleanly: the tray icon, Ctrl+C, and closing the
	// console window all come through here.
	var (
		srv      *http.Server
		stopOnce sync.Once
	)
	stop := func() {
		stopOnce.Do(func() {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				srv.Shutdown(ctx)
			}()
		})
	}
	srv = &http.Server{
		Handler:           server.NewHandler(store.New(sqldb), password),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       2 * time.Minute,
	}

	scheme := "http"
	if *useTLS {
		scheme = "https"
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
	}

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("tam-server listening on %s://%s (data in %s)", scheme, *addr, dataDir)

	done := make(chan struct{})
	var serveErr error
	go func() {
		defer close(done)
		var err error
		if *useTLS {
			err = srv.ServeTLS(ln, *certFile, *keyFile)
		} else {
			err = srv.Serve(ln)
		}
		if err != nil && err != http.ErrServerClosed {
			serveErr = err
		}
	}()
	go desktop.StopOnSignal(stop, done)

	if *useTray {
		desktop.Tray(desktop.Options{
			Tooltip:   "Ticket Auction Manager - server on " + *addr,
			Icon:      iconICO,
			QuitLabel: "Shut Down TAM Server",
			Quit:      stop,
		}, done)
	} else {
		<-done
	}
	if serveErr != nil {
		log.Fatal(serveErr)
	}
	log.Print("tam-server stopped")
}
