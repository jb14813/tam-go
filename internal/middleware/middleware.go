package middleware

import (
	"net/http"
	"os"
	"ticket-auction-manager/tam-go/internal/db"
)

func ServerMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		TAMPW := r.Header.Get("TAM-PW")
		TAMKEY := r.Header.Get("TAM-KEY")
		SERVERPW := os.Getenv("TAM_PW")
		if r.URL.Path == "/api/auth" && TAMPW == SERVERPW {
			next.ServeHTTP(w, r)
		} else if r.URL.Path == "/api/auth" && TAMPW != SERVERPW {
			http.Error(w, "Password does not check out.", http.StatusUnauthorized)
			return
		} else if r.URL.Path != "/api/auth" {
			var count int
			conn := db.CreateDBConn()
			err := conn.QueryRow("SELECT COUNT(*) FROM auth_keys WHERE auth_key = ?", TAMKEY).Scan(&count)
			if err != nil {
				http.Error(w, "Unable to verify key.", http.StatusInternalServerError)
				return
			}
			if count > 0 {
				next.ServeHTTP(w, r)
			} else {
				http.Error(w, "Invalid key.", http.StatusUnauthorized)
			}
		} else {
			http.Error(w, "Invalid authorization.", http.StatusUnauthorized)
		}
	})
}
