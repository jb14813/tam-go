package main

import (
	_ "embed"
	"fmt"
	"net/http"
	"os"
	"ticket-auction-manager/tam-go/internal/auth"
	"ticket-auction-manager/tam-go/internal/db"
	"ticket-auction-manager/tam-go/internal/prefixes"
)

//go:embed asciiart.txt
var ASCIIart string

func init() {
	os.Setenv("TAM_DAEMON", "Server")
	db.InitDB()
	fmt.Println("Database initialized.")
	fmt.Println(ASCIIart)
}

func main() {
	apiSrv := http.NewServeMux()

	apiSrv.HandleFunc("GET /api/auth", auth.GetKeys)
	apiSrv.HandleFunc("POST /api/auth", auth.PostAuthKey)
	apiSrv.HandleFunc("GET /api/prefixes", prefixes.GetAllPrefixes)
	apiSrv.HandleFunc("POST /api/prefixes", prefixes.PostPrefixes)
	apiSrv.HandleFunc("DELETE /api/prefixes", prefixes.DelPrefix)

	if len(os.Args) > 1 && os.Args[1] == "dev" {
		fmt.Println("http://localhost:8000/")
		err := http.ListenAndServe("localhost:8000", apiSrv)
		if err != nil {
			panic(err)
		}
	} else {
		fmt.Println("http://0.0.0.0:8000/")
		err := http.ListenAndServe(":8000", apiSrv)
		if err != nil {
			panic(err)
		}
	}
}

type ApiRootResp struct {
	WhoAmI  string `json:"whoami"`
	Auth    bool   `json:"authenticated"`
	Healthy bool   `json:"healthy"`
}
