package main

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"ticket-auction-manager/tam-go/internal/auth"
	"ticket-auction-manager/tam-go/internal/config"
	"ticket-auction-manager/tam-go/internal/db"
	"ticket-auction-manager/tam-go/internal/prefixes"
)

//go:embed all:dist
var filesystem embed.FS

//go:embed asciiart.txt
var ASCIIart string

func init() {
	os.Setenv("TAM_DAEMON", "Client")
	db.InitDB()
	fmt.Println("Database initialized.")
	fmt.Println(ASCIIart)
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

	clientSrv.HandleFunc("GET /api/auth", auth.GetKeys)
	clientSrv.HandleFunc("POST /api/auth", auth.PostAuthKey)
	clientSrv.HandleFunc("DELETE /api/auth", auth.DelAuthKey)

	fmt.Println("http://localhost:3080/")
	http.ListenAndServe("localhost:3080", clientSrv)
}
