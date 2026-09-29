package main

import (
	"encoding/json"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"ticket-auction-manager/tam-go/internal/store"
)

func TestStormChecksWholeRowsWhenSomeTargetsAreNeverWritten(t *testing.T) {
	for _, mode := range []string{"no writes", "slow whole write", "torn write", "ignored write", "corrupt baseline"} {
		t.Run(mode, func(t *testing.T) {
			o := options{seed: 1, tickets: 400, baskets: 60, prefixes: 5, page: 25, storm: 250 * time.Millisecond, settle: time.Second}
			rejectWrites := mode == "no writes" || mode == "corrupt baseline"
			ev := newEvent(o)
			// This is exactly the complete B1 stub reported by the Windows CI
			// failure, before any timed storm writer reaches it.
			wantB1 := store.Ticket{Prefix: "B", TID: 1, FirstName: "Chloe", LastName: "Müller", PhoneNumber: "555-860-5830", Pref: "TEXT"}
			if got := ev.stub[key{"B", 1}]; got != wantB1 {
				t.Fatalf("CI seed B1 = %+v, want %+v", got, wantB1)
			}
			actual := map[key]store.Ticket{}
			for k, row := range ev.stub {
				ev.savedTickets(1, []store.Ticket{row})
				actual[k] = row
			}
			if mode == "corrupt baseline" {
				row := actual[key{"B", 1}]
				row.PhoneNumber = "never saved"
				actual[key{"B", 1}] = row
			}
			var mu sync.Mutex
			writes := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/status" {
					w.Write([]byte(`{"state":"connected","pending":0}`))
					return
				}
				if r.Method == http.MethodPost {
					if rejectWrites {
						// No write is acknowledged or applied, regardless of the
						// platform's clock resolution or when its writer starts.
						time.Sleep(2 * o.storm)
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
					var rows []store.Ticket
					if err := json.NewDecoder(r.Body).Decode(&rows); err != nil {
						t.Error(err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					mu.Lock()
					writes++
					for _, row := range rows {
						k := key{row.Prefix, row.TID}
						switch mode {
						case "torn write":
							old := actual[k]
							old.FirstName = row.FirstName
							actual[k] = old
						case "ignored write":
						default:
							actual[k] = row
						}
					}
					mu.Unlock()
					// A single request consumes the remaining storm window, so
					// at least nine of its ten targets retain their baseline.
					time.Sleep(2 * o.storm)
					w.Write([]byte(`[]`))
					return
				}
				parts := strings.Split(r.URL.Path, "/")
				id, err := strconv.Atoi(parts[4])
				if err != nil {
					t.Error(err)
				}
				mu.Lock()
				row := actual[key{parts[3], id}]
				mu.Unlock()
				json.NewEncoder(w).Encode(row)
			}))
			defer server.Close()
			run := &test{o: o, ev: ev, clients: []*client{{n: 1, prog: &program{url: server.URL, name: "slow desk"}, rng: rand.New(rand.NewPCG(1, 2))}}}
			run.storm()
			mu.Lock()
			count := writes
			mu.Unlock()
			wantWrites := 1
			if rejectWrites {
				wantWrites = 0
			}
			if count != wantWrites {
				t.Fatalf("storm saved %d targets, want %d", count, wantWrites)
			}
			wantWhole := mode == "no writes" || mode == "slow whole write"
			wantProblems := 1
			if wantWhole {
				wantProblems = 0
			}
			if got, _ := run.problems.get("storm"); got != wantProblems {
				t.Fatalf("storm problems = %d, want %d: %s", got, wantProblems, run.problemText("storm"))
			}
		})
	}
}
