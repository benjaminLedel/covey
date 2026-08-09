// Several values under one secret key — the storage endpoints.
//
// What a value is USED for, what it may consume and who sits on it belongs to
// the runtime (runtimes.go, spec/18). Here it is only: which values does this
// key carry, add one, remove one.
package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"covey/internal/secrets"
)

func (s *Server) handleAddSecretValue(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	key := r.PathValue("key")
	var in struct {
		Value string `json:"value"`
	}
	if err := readJSON(r, &in); err != nil || key == "" || in.Value == "" {
		writeErr(w, http.StatusBadRequest, "value missing")
		return
	}
	slot, err := s.Secrets.AddValue(r.Context(), p.OrgID, key, in.Value)
	if err != nil {
		mapErr(w, err)
		return
	}
	// A further value of an LLM credential becomes further capacity on the
	// workplace — the second seat should not need a second place to configure.
	s.syncDefaultRuntime(r.Context(), p.OrgID, key)
	// The same live check as when depositing a secret: a dead token should show
	// up here and not first at the 401 inside the sandbox.
	check := checkCredential(r.Context(), key, in.Value)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "slot": slot, "check": check})
}

func (s *Server) handleDeleteSecretValue(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	slot, err := strconv.Atoi(r.PathValue("slot"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid slot")
		return
	}
	if err := s.Secrets.DeleteValue(r.Context(), p.OrgID, r.PathValue("key"), slot); err != nil {
		// The state refuses, the request was fine — that is a 409 and not the
		// 500 a generic mapping would make of it.
		if errors.Is(err, secrets.ErrLastValue) {
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
