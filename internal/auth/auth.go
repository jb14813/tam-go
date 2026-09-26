package auth

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"ticket-auction-manager/tam-go/internal/config"
	"ticket-auction-manager/tam-go/internal/db"
	"ticket-auction-manager/tam-go/internal/env"
	"ticket-auction-manager/tam-go/internal/httpclient"
	"uuid"
)

type AuthKey struct {
	AuthKey     string `json:"auth_key"`
	Description string `json:"description"`
}

type AuthReq struct {
	Description string `json:"description"`
}

func GetKeys(w http.ResponseWriter, r *http.Request) {
	Daemon := env.GetDaemon()
	s := config.ReadConfigFile()

	conn := db.CreateDBConn()
	defer conn.Close()

	var authkeys []AuthKey

	if Daemon == "Server" {
		results, err := conn.Query("SELECT auth_key, description FROM auth_keys ORDER BY description, auth_key")
		if err != nil && err != sql.ErrNoRows {
			http.Error(w, "Error getting auth keys from database.", http.StatusInternalServerError)
			return
		}
		for results.Next() {
			var authkey AuthKey
			results.Scan(&authkey.AuthKey, &authkey.Description)
			authkeys = append(authkeys, authkey)
		}
		if results.Err() != nil {
			http.Error(w, "Error iterating over auth keys.", http.StatusInternalServerError)
			return
		}

	} else if Daemon == "Client" && s.RemoteServer != "" {
		Client := httpclient.CreateClient()
		req, _ := http.NewRequest("GET", config.GetRemoteURL(s)+"api/auth", nil)
		req.Header.Add("TAM-PW", s.RemoteKey)
		res, err := Client.Do(req)
		if err != nil {
			http.Error(w, "Server is unavailable.", http.StatusBadGateway)
		}
		defer res.Body.Close()
		json.NewDecoder(res.Body).Decode(&authkeys)
	} else {
		http.Error(w, "Client is in standalone and there's no server specified. This is a moot attempt.", http.StatusNotImplemented)
		return
	}

	w.Header().Add("Content-Type", "application/json")
	w.WriteHeader(200)
	json.NewEncoder(w).Encode(authkeys)
}

func PostAuthKey(w http.ResponseWriter, r *http.Request) {
	Daemon := env.GetDaemon()
	s := config.ReadConfigFile()

	conn := db.CreateDBConn()
	defer conn.Close()

	var authreq AuthReq
	var rtnkey AuthKey

	json.NewDecoder(r.Body).Decode(&authreq)

	if Daemon == "Server" {
		rtnkey = AuthKey{AuthKey: uuid.NewV4().String(), Description: authreq.Description}
		tx, err := conn.Begin()
		if err != nil {
			http.Error(w, "Error creating transaction.", http.StatusInternalServerError)
			return
		}
		defer tx.Rollback()
		_, err = tx.Exec(`INSERT INTO auth_keys VALUES (?, ?)`, rtnkey.AuthKey, rtnkey.Description)
		if err != nil {
			http.Error(w, "Error inserting key into DB.", http.StatusInternalServerError)
			return
		}
		tx.Commit()
	} else if Daemon == "Client" && s.RemoteServer != "" {
		Client := httpclient.CreateClient()
		jsonData, _ := json.Marshal(authreq)
		req, _ := http.NewRequest("POST", config.GetRemoteURL(s)+"api/auth", bytes.NewBuffer(jsonData))
		req.Header.Add("Content-Type", "application/json")
		req.Header.Add("TAM-PW", r.Header.Get("TAM-PW"))
		res, err := Client.Do(req)
		if err != nil {
			http.Error(w, "Server is unavailable.", http.StatusBadGateway)
			return
		}
		json.NewDecoder(res.Body).Decode(&rtnkey)
	}

	w.Header().Add("Content-Type", "application/json")
	w.WriteHeader(200)
	json.NewEncoder(w).Encode(rtnkey)
}
