// Pool endpoints: several values under one secret key (spec/04).
//
// The pool sits UNDER the key — the assignment of a secret to agents stays at
// key level and is handled by the routes in handlers.go. What happens here is
// only: which values does this key carry, what has each one consumed, who is
// sitting on which one.
package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"covey/internal/observability"
	"covey/internal/secrets"
)

// poolDisplayWindow is the period the utilisation is reported over for values
// WITHOUT a limit of their own. A value with a limit is measured over its own
// window — anything else would show a number next to the limit that was not
// measured against it.
const poolDisplayWindow = 24 * time.Hour

type poolValueView struct {
	secrets.PoolValue
	Usage      observability.SlotConsumption `json:"usage"`
	WindowSecs int                           `json:"window_secs"`
}

type poolView struct {
	Key      string            `json:"key"`
	Values   []poolValueView   `json:"values"`
	Bindings []secrets.Binding `json:"bindings"`
}

func (s *Server) handleSecretPool(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	key := r.PathValue("key")

	previews, err := s.Secrets.Previews(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	var values []secrets.PoolValue
	for _, kp := range previews {
		if kp.Key == key {
			values = kp.Values
			break
		}
	}
	if values == nil {
		mapErr(w, secrets.ErrNotFound)
		return
	}

	out := poolView{Key: key, Values: make([]poolValueView, 0, len(values)), Bindings: []secrets.Binding{}}
	for _, v := range values {
		window := poolDisplayWindow
		if v.Limit.Active() {
			window = time.Duration(v.Limit.WindowSecs) * time.Second
		}
		view := poolValueView{PoolValue: v, WindowSecs: int(window.Seconds()),
			Usage: observability.SlotConsumption{Slot: v.Slot}}
		// Best effort: a failing figure must not topple the view — the
		// administration of the pool has to stay usable even when the
		// evaluation does not answer.
		if usd, tokens, err := s.Obs.SlotUsage(r.Context(), p.OrgID, key, v.Slot, window); err == nil {
			view.Usage.USD, view.Usage.Tokens = usd, tokens
		}
		out.Values = append(out.Values, view)
	}
	if b, err := s.Secrets.Bindings(r.Context(), p.OrgID, key); err == nil {
		out.Bindings = b
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAddSecretValue(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	key := r.PathValue("key")
	var in struct {
		Value string `json:"value"`
		Label string `json:"label"`
	}
	if err := readJSON(r, &in); err != nil || key == "" || in.Value == "" {
		writeErr(w, http.StatusBadRequest, "value missing")
		return
	}
	slot, err := s.Secrets.AddValue(r.Context(), p.OrgID, key, in.Value, in.Label)
	if err != nil {
		mapErr(w, err)
		return
	}
	// The same live check as when depositing a secret: a dead token should show
	// up here and not first at the 401 inside the sandbox.
	check := checkCredential(r.Context(), key, in.Value)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "slot": slot, "check": check})
}

// handlePatchSecretValue changes what is changeable about a single value: its
// label, its limit, and whether it stays parked. The VALUE itself is not
// changeable here — an overwrite goes through Put/AddValue, so that the live
// check runs over it.
func (s *Server) handlePatchSecretValue(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	key := r.PathValue("key")
	slot, err := strconv.Atoi(r.PathValue("slot"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid slot")
		return
	}
	var in struct {
		Label *string        `json:"label"`
		Limit *secrets.Limit `json:"limit"`
		// Cooldown=false is the only accepted value: a park can be lifted by
		// hand ("the token works again, try it"), but setting one belongs to
		// the platform — a cooldown claims a measurement.
		Cooldown *bool `json:"cooldown"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if in.Label != nil {
		if err := s.Secrets.SetLabel(r.Context(), p.OrgID, key, slot, *in.Label); err != nil {
			mapErr(w, err)
			return
		}
	}
	if in.Limit != nil {
		if err := s.Secrets.SetLimit(r.Context(), p.OrgID, key, slot, *in.Limit); err != nil {
			mapErr(w, err)
			return
		}
	}
	if in.Cooldown != nil {
		if *in.Cooldown {
			writeErr(w, http.StatusBadRequest, "a cooldown is set by the platform, not by hand")
			return
		}
		if err := s.Secrets.Cooldown(r.Context(), p.OrgID, key, slot, time.Time{}, ""); err != nil {
			mapErr(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleDeleteSecretValue(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	slot, err := strconv.Atoi(r.PathValue("slot"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid slot")
		return
	}
	if err := s.Secrets.DeleteValue(r.Context(), p.OrgID, r.PathValue("key"), slot); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
