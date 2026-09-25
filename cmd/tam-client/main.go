// Command tam-client serves the Ticket Auction Manager web app and its API
// on a venue laptop, against a local database or a remote tam-server.
//
//go:generate go-winres simply --icon icon.ico --manifest cli --arch amd64 --product-name "Ticket Auction Manager" --file-description "Ticket Auction Manager client" --original-filename tam-client.exe --file-version 0.0.1 --product-version 0.0.1 --copyright "Copyright (c) 2026 Dilan Gilluly. MIT License." --out rsrc
package main

import (
	"context"
	"embed"
	"flag"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
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
	open := flag.Bool("open", true, "open the web app in the default browser once it is listening")
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

	var srv *http.Server
	stop := func() {
		// Let the "shutting down" answer reach the page, then stop serving.
		time.Sleep(300 * time.Millisecond)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}
	srv = &http.Server{
		Handler:           client.NewHandler(store.New(sqldb), filepath.Join(dataDir, "settings.json"), dist, client.WithShutdown(stop)),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       2 * time.Minute,
	}

	// Listen first so the browser is only opened once the port is really ours.
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	url := "http://" + browserHost(ln.Addr().(*net.TCPAddr)) + "/"
	log.Printf("tam-client listening on %s (data in %s)", url, dataDir)
	if *open {
		go func() {
			time.Sleep(300 * time.Millisecond)
			if err := openBrowser(url); err != nil {
				log.Printf("could not open the browser (%v); open %s yourself", err, url)
			}
		}()
	}
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
	log.Print("tam-client stopped")
}

// browserHost turns the bound address into something a browser on this
// machine can open: an unspecified address becomes localhost.
func browserHost(a *net.TCPAddr) string {
	host := "localhost"
	if a.IP != nil && !a.IP.IsUnspecified() {
		host = a.IP.String()
	}
	return net.JoinHostPort(host, strconv.Itoa(a.Port))
}

// openBrowser asks the operating system to open url in the default browser.
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
