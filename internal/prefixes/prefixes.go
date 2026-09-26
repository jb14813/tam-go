package prefixes

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"ticket-auction-manager/tam-go/internal/config"
	"ticket-auction-manager/tam-go/internal/db"
	"ticket-auction-manager/tam-go/internal/env"
	"ticket-auction-manager/tam-go/internal/httpclient"
)

type Prefix struct {
	Prefix string `json:"prefix"`
	Color  string `json:"color"`
	Weight int    `json:"weight"`
}

func GetAllPrefixes(w http.ResponseWriter, r *http.Request) {
	s := config.ReadConfigFile()
	Daemon := env.GetDaemon()
	var prefixes []Prefix = []Prefix{}
	conn := db.CreateDBConn()
	defer conn.Close()

	if Daemon == "Client" && s.RemoteServer != "" {
		Client := httpclient.CreateClient()
		req, _ := http.NewRequest("GET", config.GetRemoteURL(s)+"api/prefixes", nil)
		req.Header.Add("TAM-KEY", s.RemoteKey)
		res, err := Client.Do(req)
		if err != nil {
			http.Error(w, "Server is unavailable.", http.StatusBadGateway)
			return
		}
		defer res.Body.Close()
		json.NewDecoder(res.Body).Decode(&prefixes)
	} else {
		results, err := conn.Query("SELECT prefix, color, weight FROM prefixes ORDER BY weight, prefix")
		if err != nil && err != sql.ErrNoRows {
			http.Error(w, "Error running query.", http.StatusInternalServerError)
			return
		}
		for results.Next() {
			var prefix Prefix
			results.Scan(&prefix.Prefix, &prefix.Color, &prefix.Weight)
			prefixes = append(prefixes, prefix)
		}
		if err := results.Err(); err != nil {
			http.Error(w, "Error iterating rows.", http.StatusInternalServerError)
			return
		}
	}
	w.Header().Add("Content-Type", "application/json")
	w.WriteHeader(200)
	json.NewEncoder(w).Encode(prefixes)
}

func PostPrefixes(w http.ResponseWriter, r *http.Request) {
	s := config.ReadConfigFile()
	Daemon := env.GetDaemon()
	conn := db.CreateDBConn()
	defer conn.Close()

	var prefixes []Prefix

	err := json.NewDecoder(r.Body).Decode(&prefixes)
	if err != nil {
		http.Error(w, "Error decoding json data of request.", http.StatusBadRequest)
		return
	}
	tx, err := conn.Begin()
	if err != nil {
		http.Error(w, "Error creating transaction.", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	for i := 0; i < len(prefixes); i++ {
		tx.Exec(`INSERT INTO prefixes VALUES (?, ?, ?) ON CONFLICT (prefix) DO UPDATE SET
			color = EXCLUDED.color, weight = EXCLUDED.weight`, prefixes[i].Prefix, prefixes[i].Color, prefixes[i].Weight)
	}
	tx.Commit()

	if Daemon == "Client" && s.RemoteServer != "" {
		Client := httpclient.CreateClient()
		jsonData, _ := json.Marshal(prefixes)
		req, _ := http.NewRequest("POST", config.GetRemoteURL(s)+"api/prefixes", bytes.NewBuffer(jsonData))
		req.Header.Add("Content-Type", "application/json")
		req.Header.Add("TAM-KEY", s.RemoteKey)
		res, err := Client.Do(req)
		if err != nil {
			http.Error(w, "Server is unavailable.", http.StatusBadGateway)
			return
		}
		defer res.Body.Close()
	}

	w.Header().Add("Content-Type", "application/json")
	w.WriteHeader(200)
	json.NewEncoder(w).Encode(prefixes)
}

func DelPrefix(w http.ResponseWriter, r *http.Request) {
	s := config.ReadConfigFile()
	Daemon := env.GetDaemon()
	conn := db.CreateDBConn()
	defer conn.Close()

	PrefixName := r.URL.Query().Get("p")

	tx, err := conn.Begin()
	if err != nil {
		http.Error(w, "Error creating transaction.", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	tx.Exec("DELETE FROM prefixes WHERE prefix = ?", PrefixName)
	tx.Commit()

	rtnPrefix := Prefix{Prefix: PrefixName, Color: "", Weight: 0}

	if Daemon == "Client" && s.RemoteServer != "" {
		Client := httpclient.CreateClient()
		req, _ := http.NewRequest("DELETE", config.GetRemoteURL(s)+"api/prefixes", nil)
		req.Header.Add("Content-Type", "application/json")
		req.Header.Add("TAM-KEY", s.RemoteKey)
		params := url.Values{}
		params.Add("p", PrefixName)
		req.URL.RawQuery = params.Encode()
		res, err := Client.Do(req)
		if err != nil {
			http.Error(w, "Server is unavailable.", http.StatusBadGateway)
			return
		}
		defer res.Body.Close()
	}

	w.Header().Add("Content-Type", "application/json")
	w.WriteHeader(200)
	json.NewEncoder(w).Encode(rtnPrefix)
}
