package httpapi

// Die Arbeitsakte auf dem Mitarbeiter-Profil (spec/21).
//
// Sie wird hier zuerst für MENSCHEN gebaut und erst danach für den
// Covey Doctor, der sie über covey/work_record liest. Das ist keine
// Reihenfolge aus Bequemlichkeit: was ein Mensch auf einer Seite nicht lesen
// kann, kann er in einer Agenten-Antwort auch nicht überprüfen — und die
// Antwort auf „warum liefert dieser Agent nicht" ist die erste, die jemand
// nachrechnen können muss.
//
// WER SIE LESEN DARF, ist hier entschieden und nicht geerbt. spec/17 sagt,
// Leistungsdaten je Agent sind empfindlicher als eine Kostensumme; die Akte
// folgt deshalb den RECORDINGS, nicht den Kostenzahlen: platform_admin,
// security, der Verwalter des Agenten und der Auditor lesend. Controlling
// fehlt. „Wer die Rechnung sehen darf, darf auch das sehen" wäre die Antwort,
// die die Funktion in jedem Betrieb mit Betriebsrat unbenutzbar macht.

import (
	"net/http"

	"covey/internal/identity"
	"covey/internal/workrecord"
)

// workRecordRoles: dieselbe Grenze für die Akte und für die Kennzahlen eines
// einzelnen Agenten — sie aus der Akte zu lesen ist dieselbe Handlung wie die
// Akte zu lesen (spec/21, und damit die offene Frage aus spec/17).
func workRecordRoles() []string {
	return []string{identity.RolePlatformAdmin, identity.RoleAgentOwner,
		identity.RoleSecurity, identity.RoleAuditor}
}

func (s *Server) handleWorkRecord(w http.ResponseWriter, r *http.Request) {
	agent := agentFrom(r)
	_, since := costWindow(r)
	rec, err := s.workRecords().Build(r.Context(), agent.ID, since)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// workRecords baut den Sammler bei jedem Aufruf neu — er hält keinen Zustand,
// nur Zeiger auf die Stores, die der Server ohnehin trägt.
func (s *Server) workRecords() *workrecord.Builder {
	return &workrecord.Builder{
		Pool: s.Pool, Registry: s.Registry, Obs: s.Obs, Skills: s.lintSkills(),
	}
}

// handleAgentReviews ist die Historie auf dem Mitarbeiter-Profil: was der
// Betrieb ueber diesen Kollegen geschrieben hat, datiert, neueste zuerst.
//
// Dieselbe Rollengrenze wie die Arbeitsakte — ein Review ist die Akte in
// Worten. Und bewusst nur ein LESE-Pfad fuer Menschen: es gibt keine Aktion,
// mit der ein Agent Reviews abruft. Ein offener Vorschlag und eine Beurteilung
// erreichen den beurteilten Agenten auf keinem Weg, und das bleibt so, weil
// der Weg fehlt und nicht, weil eine Regel ihn verbietet.
func (s *Server) handleAgentReviews(w http.ResponseWriter, r *http.Request) {
	agent := agentFrom(r)
	list, err := s.Registry.Reviews(r.Context(), agent.ID, 20)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}
