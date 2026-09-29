package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"ticket-auction-manager/tam-go/internal/store"
)

type conflictChoice struct {
	Hash   string
	Fields []conflictField
}
type conflictField struct{ Label, Value string }
type conflictRow struct {
	Kind, Prefix, Label, Token string
	ID                         int
	Choices                    []conflictChoice
}

func conflictFields(candidate store.RecordCandidate) ([]conflictField, error) {
	switch candidate.Revision.Kind {
	case "ticket":
		var ticket store.Ticket
		if err := json.Unmarshal(candidate.Value, &ticket); err != nil {
			return nil, err
		}
		return []conflictField{{"First name", ticket.FirstName}, {"Last name", ticket.LastName}, {"Phone", ticket.PhoneNumber}, {"Contact preference", ticket.Pref}}, nil
	case "metadata":
		var values []string
		if err := json.Unmarshal(candidate.Value, &values); err != nil {
			return nil, err
		}
		if len(values) != 2 {
			return nil, fmt.Errorf("invalid basket details in data review")
		}
		return []conflictField{{"Description", values[0]}, {"Donors", values[1]}}, nil
	case "drawing":
		var winner int
		if err := json.Unmarshal(candidate.Value, &winner); err != nil {
			return nil, err
		}
		label := strconv.Itoa(winner)
		if winner == 0 {
			label = "Undrawn / cleared"
		}
		return []conflictField{{"Winning ticket", label}}, nil
	case "prefix":
		if string(candidate.Value) == "null" {
			return []conflictField{{"Prefix", "Deleted"}}, nil
		}
		var prefix store.Prefix
		if err := json.Unmarshal(candidate.Value, &prefix); err != nil {
			return nil, err
		}
		return []conflictField{{"Color", prefix.Color}, {"Sort order", strconv.Itoa(prefix.Weight)}}, nil
	default:
		return nil, fmt.Errorf("unknown data review kind %q", candidate.Revision.Kind)
	}
}

func (h *handler) conflicts(w http.ResponseWriter, r *http.Request, session *session) {
	h.renderConflicts(w, r, session, http.StatusOK, "")
}

func (h *handler) renderConflicts(w http.ResponseWriter, r *http.Request, session *session, status int, problem string) {
	conflicts, err := h.st.Conflicts()
	if err != nil {
		h.internal(w, r, err)
		return
	}
	rows := []conflictRow{}
	labels := map[string]string{"ticket": "Ticket", "metadata": "Basket details", "drawing": "Basket winner", "prefix": "Prefix"}
	for _, conflict := range conflicts {
		row := conflictRow{Kind: conflict.Kind, Prefix: conflict.Prefix, ID: conflict.ID, Label: labels[conflict.Kind], Token: store.ConflictToken(conflict)}
		for _, candidate := range conflict.Candidates {
			fields, err := conflictFields(candidate)
			if err != nil {
				h.internal(w, r, err)
				return
			}
			row.Choices = append(row.Choices, conflictChoice{Hash: candidate.Revision.Hash, Fields: fields})
		}
		rows = append(rows, row)
	}
	v := h.view(session, "conflicts", rows)
	v.Error = problem
	h.render(w, status, "conflicts", v)
}

func (h *handler) resolveConflict(w http.ResponseWriter, r *http.Request, session *session) {
	id, err := strconv.Atoi(r.PostFormValue("id"))
	if err != nil || id < 0 || r.PostFormValue("choice") == "" || r.PostFormValue("expected") == "" || r.PostFormValue("confirm") != "yes" {
		h.renderConflicts(w, r, session, http.StatusBadRequest, "Choose a saved version and confirm that you checked it against the event's records.")
		return
	}
	err = h.st.ResolveConflictIfCurrent(r.PostFormValue("kind"), r.PostFormValue("prefix"), id, r.PostFormValue("choice"), r.PostFormValue("expected"))
	if errors.Is(err, store.ErrConflictChanged) {
		h.renderConflicts(w, r, session, http.StatusConflict, "The saved alternatives changed while this page was open. Review the current versions before choosing again.")
		return
	}
	if err != nil {
		h.internal(w, r, err)
		return
	}
	h.ss.setFlash(session.id, "Saved the selected version. Clients will see it when their next lookup completes.")
	http.Redirect(w, r, "/admin/conflicts", http.StatusSeeOther)
}
