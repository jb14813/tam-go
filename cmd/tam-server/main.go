package main

import (
	"fmt"
	"net/http"
	"os"
	"ticket-auction-manager/tam-go/internal/db"
	"ticket-auction-manager/tam-go/internal/prefixes"
)

func init() {
	os.Setenv("TAM_DAEMON", "Server")
	db.InitDB()
}

func main() {
	apiSrv := http.NewServeMux()

	apiSrv.HandleFunc("/api/prefixes", prefixes.GetAllPrefixes)

	if len(os.Args) > 1 && os.Args[1] == "dev" {
		fmt.Println("Listening on http://localhost:8000")
		err := http.ListenAndServe("localhost:8000", apiSrv)
		if err != nil {
			panic(err)
		}
	} else {
		fmt.Println("Listening on http://0.0.0.0:8000")
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
