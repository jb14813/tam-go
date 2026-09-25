package prefixes

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"ticket-auction-manager/tam-go/internal/db"
)

type Prefix struct {
	Prefix string `json:"prefix"`
	Color  string `json:"color"`
	Weight int    `json:"weight"`
}

func GetAllPrefixes(w http.ResponseWriter, r *http.Request) {
	conn := db.CreateDBConn()
	defer conn.Close()

	var prefixes []Prefix = []Prefix{}
	results, err := conn.Query("SELECT prefix, color, weight FROM prefixes ORDER BY weight, prefix")
	if err != nil && err != sql.ErrNoRows {
		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"detail": err.Error()})
	}
	for results.Next() {
		var prefix Prefix
		results.Scan(&prefix.Prefix, &prefix.Color, &prefix.Weight)
		prefixes = append(prefixes, prefix)
	}
	if err := results.Err(); err != nil {
		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"detail": err.Error()})
	}
	w.Header().Add("Content-Type", "application/json")
	w.WriteHeader(200)
	json.NewEncoder(w).Encode(prefixes)
}

func PostPrefixes(w http.ResponseWriter, r *http.Request) {
	conn := db.CreateDBConn()
	defer conn.Close()

	var prefixes []Prefix

	err := json.NewDecoder(r.Body).Decode(&prefixes)
	if err != nil {
		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"detail": err.Error()})
	}
	tx, err := conn.Begin()
	if err != nil {
		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"detail": err.Error()})
	}
	defer tx.Rollback()
	for i := 0; i < len(prefixes); i++ {
		tx.Exec(`INSERT INTO prefixes VALUES (?, ?, ?) ON CONFLICT (prefix) DO UPDATE SET
			color = EXCLUDED.color, weight = EXCLUDED.weight`, prefixes[i].Prefix, prefixes[i].Color, prefixes[i].Weight)
	}
	tx.Commit()

	w.Header().Add("Content-Type", "application/json")
	w.WriteHeader(200)
	json.NewEncoder(w).Encode(prefixes)
}

func DelPrefix(w http.ResponseWriter, r *http.Request) {
	conn := db.CreateDBConn()
	defer conn.Close()

	PrefixName := r.URL.Query().Get("p")

	tx, err := conn.Begin()
	if err != nil {
		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"detail": err.Error()})
	}
	defer tx.Rollback()
	tx.Exec("DELETE FROM prefixes WHERE prefix = ?", PrefixName)
	tx.Commit()

	rtnPrefix := Prefix{Prefix: PrefixName, Color: "", Weight: 0}

	w.Header().Add("Content-Type", "application/json")
	w.WriteHeader(200)
	json.NewEncoder(w).Encode(rtnPrefix)
}
