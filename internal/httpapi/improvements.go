package httpapi

// Die Liste der offenen Punkte — und damit der Kanal (spec/21).
//
// Ein Review endet in einer von drei Diagnosen, und alle drei brauchen einen
// Menschen: der Vorschlag mit seinem Diff, der Befund ohne einen, das schon
// eingereichte Issue. Die Versuchung wäre, einen Weg zu bauen, jemandem etwas
// zu SAGEN — die Plattform kennt keine Nachricht an einen Menschen, und eine
// hier zu erfinden hieße, einen zweiten, schlechteren Posteingang neben den
// zu stellen, den es für das Annehmen ohnehin geben muss. Also ist die
// Annahme-Oberfläche der Kanal: ein Befund, den nur ein Mensch bearbeiten
// kann, ist dann keine Nachricht, die untergehen kann, sondern ein offener
// Punkt, der offen bleibt.
//
// Angelegt werden die Punkte hier NICHT. Ein Mensch, der eine Config ändern
// will, ändert sie — er braucht keinen Vorschlag an sich selbst. Der
// Schreibweg gehört dem Agenten (covey/propose_agent_config, spätere
// Scheibe); diese Datei ist das andere Ende.

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/identity"
)

// fileDiff ist eine geänderte Datei, wie die Oberfläche sie zeigt: vorher
// der laufende Stand, nachher der vorgeschlagene. Bewusst gegen den LAUFENDEN
// Stand und nicht gegen die Basis des Vorschlags — was ein Mensch beurteilt,
// ist die Änderung, die durch sein Klicken entsteht.
type fileDiff struct {
	File   string `json:"file"`
	Before string `json:"before"`
	After  string `json:"after"`
}

// improvementView ist der Punkt plus alles, was die Oberfläche sonst
// nachfragen müsste: um wen es geht, wer ihn geschrieben hat, ob die Basis
// weggewandert ist und wer ihn annehmen darf.
type improvementView struct {
	agents.ImprovementItem
	AgentSlug    string     `json:"agent_slug"`
	AgentName    string     `json:"agent_name"`
	AgentOwnerID *uuid.UUID `json:"agent_owner_id,omitempty"`
	AuthorSlug   string     `json:"author_slug,omitempty"`
	AuthorName   string     `json:"author_name,omitempty"`
	// CurrentVersion ist die Version, die gerade läuft. Stale sagt, dass der
	// Vorschlag gegen eine ältere geschrieben wurde — das allein macht ihn
	// nicht falsch, es ist eine Warnung.
	CurrentVersion int  `json:"current_version"`
	Stale          bool `json:"stale"`
	// Conflicts sind die Dateien, die seit der Basis von jemand anderem
	// geändert wurden. Solange die Liste nicht leer ist, wird der Vorschlag
	// nicht angenommen — sonst überschriebe die Annahme still eine fremde
	// Änderung.
	Conflicts []string `json:"conflicts,omitempty"`
	// NeedsSecurity: der Vorschlag fasst ACCESS.md oder EGRESS.md an. Dann
	// entscheidet nicht der Teamleiter, dem der Agent gehört, sondern
	// org_admin/security (spec/02, spec/21).
	NeedsSecurity bool       `json:"needs_security"`
	Diff          []fileDiff `json:"diff,omitempty"`
}

// improvementRoles dürfen die Liste LESEN. Controlling fehlt bewusst: ein
// Kostenblatt sagt, was ausgegeben wurde, ein Vorschlag sagt, wie jemand
// gearbeitet hat — dieselbe Grenze, die spec/21 für die Arbeitsakte zieht.
func improvementReadRoles() []string {
	return []string{identity.RoleOrgAdmin, identity.RoleAgentOwner,
		identity.RoleSecurity, identity.RoleAuditor}
}

func (s *Server) handleListImprovements(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	f := agents.ImprovementFilter{
		Status: strings.TrimSpace(r.URL.Query().Get("status")),
		Kind:   strings.TrimSpace(r.URL.Query().Get("kind")),
	}
	// „Liste pro Agent-Owner": mine=1 zeigt nur die Punkte zu den Kollegen,
	// die dem Anfragenden gehören. Serverseitig gefiltert und nicht in der
	// Oberfläche — eine leere Liste soll leer ankommen und nicht ausgeblendet
	// werden.
	if r.URL.Query().Get("mine") == "1" {
		owned, err := s.Registry.List(r.Context(), p.OrgID)
		if err != nil {
			mapErr(w, err)
			return
		}
		ids := []uuid.UUID{}
		for _, a := range owned {
			if a.OwnerID != nil && *a.OwnerID == p.ID {
				ids = append(ids, a.ID)
			}
		}
		f.AgentIDs = ids
	}
	items, err := s.Registry.ListImprovements(r.Context(), p.OrgID, f)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.improvementViews(r, items))
}

func (s *Server) handleGetImprovement(w http.ResponseWriter, r *http.Request) {
	item, ok := s.improvementFromPath(w, r)
	if !ok {
		return
	}
	views := s.improvementViews(r, []agents.ImprovementItem{item})
	writeJSON(w, http.StatusOK, views[0])
}

// improvementFromPath liest den Punkt aus der URL und prüft die Organisation.
// Fremd und nicht vorhanden sind dieselbe Antwort.
func (s *Server) improvementFromPath(w http.ResponseWriter, r *http.Request) (agents.ImprovementItem, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return agents.ImprovementItem{}, false
	}
	item, err := s.Registry.GetImprovement(r.Context(), id)
	if err != nil || item.OrgID != principalFrom(r).OrgID {
		writeErr(w, http.StatusNotFound, "not found")
		return agents.ImprovementItem{}, false
	}
	return item, true
}

// improvementViews reichert die Punkte an. Die Configs werden pro Agent EINMAL
// gelesen, nicht pro Punkt: eine offene Liste enthält typischerweise mehrere
// Punkte zu wenigen Kollegen.
func (s *Server) improvementViews(r *http.Request, items []agents.ImprovementItem) []improvementView {
	ctx := r.Context()
	agentCache := map[uuid.UUID]agents.Agent{}
	lookupAgent := func(id uuid.UUID) (agents.Agent, bool) {
		if a, ok := agentCache[id]; ok {
			return a, true
		}
		a, err := s.Registry.Get(ctx, id)
		if err != nil {
			return agents.Agent{}, false
		}
		agentCache[id] = a
		return a, true
	}
	configCache := map[uuid.UUID]agents.ConfigVersion{}
	lookupConfig := func(id uuid.UUID) agents.ConfigVersion {
		if c, ok := configCache[id]; ok {
			return c
		}
		c, err := s.Registry.CurrentConfig(ctx, id)
		if err != nil {
			c = agents.ConfigVersion{Files: map[string]string{}}
		}
		configCache[id] = c
		return c
	}

	out := make([]improvementView, 0, len(items))
	for _, item := range items {
		v := improvementView{ImprovementItem: item}
		if a, ok := lookupAgent(item.AgentID); ok {
			v.AgentSlug, v.AgentName, v.AgentOwnerID = a.Slug, a.DisplayName, a.OwnerID
		}
		if item.AuthorAgentID != nil {
			if a, ok := lookupAgent(*item.AuthorAgentID); ok {
				v.AuthorSlug, v.AuthorName = a.Slug, a.DisplayName
			}
		}
		if item.Kind != agents.KindProposal {
			out = append(out, v)
			continue
		}
		cur := lookupConfig(item.AgentID)
		v.CurrentVersion = cur.Version
		v.NeedsSecurity = len(agents.RestrictedChanges(cur.Files, item.Files)) > 0
		for _, name := range agents.ChangedFiles(cur.Files, item.Files) {
			v.Diff = append(v.Diff, fileDiff{File: name, Before: cur.Files[name], After: item.Files[name]})
		}
		// Veraltet und in Konflikt sind Fragen an einen OFFENEN Vorschlag. Für
		// einen angenommenen sind sie nicht nur überflüssig, sondern verkehrt:
		// die laufende Version enthält danach genau seine Dateien, also meldet
		// der Vergleich zuverlässig „zwischenzeitlich geändert" — und zwar für
		// die Dateien, die die Annahme selbst geschrieben hat. Im Archiv stand
		// so hinter jeder erfolgreichen Annahme ein roter Konflikt.
		if item.Status == agents.ImprovementPending {
			v.Stale = item.BaseVersion != cur.Version
			if v.Stale {
				v.Conflicts = s.proposalConflicts(r, item, cur)
			}
		}
		out = append(out, v)
	}
	return out
}

// proposalConflicts vergleicht die Basis des Vorschlags mit dem laufenden
// Stand. Ist die Basisversion nicht mehr da (gelöscht, oder es gab nie eine),
// gilt der leere Satz als Basis — dann sind genau die Dateien in Konflikt, die
// heute schon Inhalt haben.
func (s *Server) proposalConflicts(r *http.Request, item agents.ImprovementItem, cur agents.ConfigVersion) []string {
	base := map[string]string{}
	if item.BaseVersion > 0 {
		if bv, err := s.Registry.ConfigAtVersion(r.Context(), item.AgentID, item.BaseVersion); err == nil {
			base = bv.Files
		}
	}
	return agents.ProposalConflicts(base, cur.Files, item.Files)
}

// handleDecideImprovement ist die eine Handlung dieser Oberfläche: annehmen
// oder ablehnen, beides von einem Menschen.
//
// Annehmen heißt beim Vorschlag: mergen und über den NORMALEN Schreibweg als
// neue Version speichern, mit dem Menschen als Urheber. Es gibt keinen zweiten
// Weg in eine laufende Config, und deshalb auch keine Stelle, an der ein
// Vorschlag etwas könnte, was ein Mensch nicht könnte.
func (s *Server) handleDecideImprovement(w http.ResponseWriter, r *http.Request) {
	item, ok := s.improvementFromPath(w, r)
	if !ok {
		return
	}
	var in struct {
		Accept bool   `json:"accept"`
		Note   string `json:"note"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if item.Status != agents.ImprovementPending {
		writeErr(w, http.StatusConflict, "this item has already been decided")
		return
	}
	p := principalFrom(r)

	// Ablehnen kostet nichts und nimmt nichts weg: das darf jeder, der die
	// Liste bedienen darf. Der Grund bleibt stehen — ein abgelehnter Vorschlag
	// ist das Nützlichste, was jemand lesen kann, der covey Doctor
	// selbst überprüfen will.
	if !in.Accept {
		s.finishImprovement(w, r, item.ID, agents.ImprovementRejected, in.Note, 0)
		return
	}
	// Befund und Issue haben keinen Diff — sie werden abgehakt, nicht anwendet.
	if item.Kind != agents.KindProposal {
		s.finishImprovement(w, r, item.ID, agents.ImprovementAccepted, in.Note, 0)
		return
	}

	cur, err := s.Registry.CurrentConfig(r.Context(), item.AgentID)
	if err != nil && !errors.Is(err, agents.ErrNotFound) {
		mapErr(w, err)
		return
	}
	if cur.Files == nil {
		cur.Files = map[string]string{}
	}
	// Konflikt vor Rolle: ein Vorschlag, dessen Basis weggewandert ist, wird
	// gar nicht erst zur Rollenfrage. Er wird neu geschrieben oder verworfen —
	// dieselbe Antwort, die ein Pull Request darauf gibt.
	if conflicts := s.proposalConflicts(r, item, cur); len(conflicts) > 0 {
		writeErr(w, http.StatusConflict,
			"the configuration has changed since this proposal was written ("+
				strings.Join(conflicts, ", ")+") — it has to be rewritten or discarded")
		return
	}
	// Die Rollengrenze aus spec/02, geerbt statt umgangen: wer ACCESS.md oder
	// EGRESS.md ändert, weitet den Zugang eines Kollegen. Das entscheidet
	// nicht, wer zuerst geklickt hat.
	if restricted := agents.RestrictedChanges(cur.Files, item.Files); len(restricted) > 0 {
		if p.Role != identity.RoleOrgAdmin && p.Role != identity.RoleSecurity {
			writeErr(w, http.StatusForbidden,
				"this proposal changes "+strings.Join(restricted, " and ")+
					" — only org_admin or security may accept it")
			return
		}
	}

	merged := agents.MergeConfig(cur.Files, item.Files)
	// Der Schreib-Durchgriff darf NUR anfassen, was der Vorschlag wirklich
	// ändert.
	//
	// ACCESS.md und EGRESS.md stehen zwar im Schnappschuss, sind dort aber
	// nicht maßgeblich: der Tools-Reiter und die Egress-Routen ändern die
	// Zuweisung, ohne eine Config-Version zu schreiben. Ginge der Schnappschuss
	// so in prepareConfigWrite, riefe dessen apply() SetAgentTools mit der
	// veralteten Liste — und die Annahme eines Vorschlags zu PLAYBOOKS.md hübe
	// die Werkzeug-Einschränkung auf, die jemand über die Oberfläche gesetzt
	// hat. Für einen agent_owner wäre dieselbe Ursache ein 403 auf einen
	// Vorschlag, der den Zugang gar nicht berührt.
	//
	// prepareConfigApply lässt einen Bereich in Ruhe, wenn seine Datei fehlt
	// ("omitted EGRESS.md means no change"). Also fehlt sie dort — gespeichert
	// wird die Version trotzdem vollständig, damit der Schnappschuss keine
	// Lücke bekommt.
	writeThrough := make(map[string]string, len(merged))
	for k, v := range merged {
		writeThrough[k] = v
	}
	for _, name := range []string{"ACCESS.md", "EGRESS.md"} {
		if _, touched := item.Files[name]; !touched {
			delete(writeThrough, name)
		}
	}
	apply, ok := s.prepareConfigWrite(w, r, item.AgentID, writeThrough)
	if !ok {
		return
	}
	cv, err := s.Registry.SaveConfig(r.Context(), item.AgentID, merged, &p.ID)
	if err != nil {
		mapErr(w, err)
		return
	}
	if err := apply(r.Context()); err != nil {
		s.Log.Error("improvement: config write-through", "agent", item.AgentID, "err", err)
		writeErr(w, http.StatusInternalServerError,
			"version saved, but applying it to tools/egress failed: "+err.Error())
		return
	}
	s.finishImprovement(w, r, item.ID, agents.ImprovementAccepted, in.Note, cv.Version)
}

// finishImprovement hält die Entscheidung fest. Das UPDATE ist gegen
// „pending" geführt; klicken zwei Menschen gleichzeitig, gewinnt einer und
// der andere bekommt 409 statt einer zweiten Entscheidung.
func (s *Server) finishImprovement(w http.ResponseWriter, r *http.Request, id uuid.UUID,
	status, note string, appliedVersion int) {

	p := principalFrom(r)
	decided, err := s.Registry.DecideImprovement(r.Context(), id, status, p.ID, strings.TrimSpace(note), appliedVersion)
	if errors.Is(err, agents.ErrNotPending) {
		writeErr(w, http.StatusConflict, "this item has already been decided")
		return
	}
	if err != nil {
		mapErr(w, err)
		return
	}
	views := s.improvementViews(r, []agents.ImprovementItem{decided})
	writeJSON(w, http.StatusOK, views[0])
}
