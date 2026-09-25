package main

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"ticket-auction-manager/tam-go/internal/config"
	"ticket-auction-manager/tam-go/internal/db"
	"ticket-auction-manager/tam-go/internal/prefixes"
)

//go:embed all:dist/*
var filesystem embed.FS

func init() {
	os.Setenv("TAM_DAEMON", "Client")
	db.InitDB()
}

func main() {
	clientSrv := http.NewServeMux()

	subFS, err := fs.Sub(filesystem, "dist")
	if err != nil {
		panic(err)
	}

	clientSrv.Handle("/", http.RedirectHandler("/web", http.StatusPermanentRedirect))
	clientSrv.Handle("/web/", http.StripPrefix("/web", http.FileServer(http.FS(subFS))))

	clientSrv.HandleFunc("GET /api/settings", config.GetAllSettings)
	clientSrv.HandleFunc("POST /api/settings", config.SaveAllSettings)
	clientSrv.HandleFunc("GET /api/prefixes", prefixes.GetAllPrefixes)
	clientSrv.HandleFunc("POST /api/prefixes", prefixes.PostPrefixes)
	clientSrv.HandleFunc("DELETE /api/prefixes", prefixes.DelPrefix)

	fmt.Println("Listening on http://localhost:3080/")
	http.ListenAndServe("localhost:3080", clientSrv)
}
