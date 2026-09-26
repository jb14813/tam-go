package auth

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
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
		req.Header.Add("TAM-PW", r.Header.Get("TAM-PW"))
		res, err := Client.Do(req)
		if err != nil {
			http.Error(w, "Server is unavailable.", http.StatusBadGateway)
		}
		defer res.Body.Close()
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			http.Error(w, "Server reported an error.", res.StatusCode)
			return
		}
		json.NewDecoder(res.Body).Decode(&authkeys)
	} else {
		http.Error(w, "Client is in standalone there's no point in running this.", http.StatusNotImplemented)
		return
	}

	w.Header().Add("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
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
		newKey := uuid.NewV4().String()
		tx, err := conn.Begin()
		if err != nil {
			http.Error(w, "Error creating transaction.", http.StatusInternalServerError)
			return
		}
		defer tx.Rollback()
		err = tx.QueryRow(`INSERT INTO auth_keys VALUES (?, ?) RETURNING auth_key, description`, newKey, authreq.Description).Scan(&rtnkey.AuthKey, &rtnkey.Description)
		if err != nil {
			http.Error(w, "Unable to insert row. Likely a conflict, please retry.", http.StatusInternalServerError)
		}
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
		defer res.Body.Close()
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			http.Error(w, "Server reported an error.", res.StatusCode)
			return
		}
		fmt.Println(res.StatusCode)
		json.NewDecoder(res.Body).Decode(&rtnkey)
	} else {
		http.Error(w, "Client is in standalone there's no point in running this.", http.StatusNotImplemented)
		return
	}

	w.Header().Add("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(rtnkey)
}

func DelAuthKey(w http.ResponseWriter, r *http.Request) {
	Daemon := env.GetDaemon()
	s := config.ReadConfigFile()

	conn := db.CreateDBConn()
	defer conn.Close()
	KeyToDel := r.URL.Query().Get("key_to_del")
	rtnKey := AuthKey{}

	if Daemon == "Server" {
		tx, err := conn.Begin()
		if err != nil {
			http.Error(w, "Error creating transaction.", http.StatusInternalServerError)
			return
		}
		defer tx.Rollback()
		err = tx.QueryRow("DELETE FROM auth_keys WHERE auth_key = ? RETURNING auth_key, description", KeyToDel).Scan(&rtnKey.AuthKey, &rtnKey.Description)
		if err != nil && err != sql.ErrNoRows {
			http.Error(w, "Unable to run delete query.", http.StatusInternalServerError)
		} else if err != nil && err == sql.ErrNoRows {
			rtnKey = AuthKey{AuthKey: KeyToDel, Description: ""}
		}
		tx.Commit()
	} else if Daemon == "Client" && s.RemoteServer != "" {
		Client := httpclient.CreateClient()
		PathWithQuery := fmt.Sprintf("api/auth?key_to_del=%s", KeyToDel)
		req, _ := http.NewRequest("DELETE", config.GetRemoteURL(s)+PathWithQuery, nil)
		req.Header.Add("TAM-PW", r.Header.Get("TAM-PW"))
		res, err := Client.Do(req)
		if err != nil {
			http.Error(w, "Server is unavailable.", http.StatusBadGateway)
			return
		}
		defer res.Body.Close()
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			http.Error(w, "Server reported an error.", res.StatusCode)
			return
		}
		json.NewDecoder(res.Body).Decode(&rtnKey)
	} else {
		http.Error(w, "Client is in standalone there's no point in running this.", http.StatusNotImplemented)
		return
	}

	w.Header().Add("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(rtnKey)
}
