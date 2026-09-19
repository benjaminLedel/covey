package httpapi

import "net/http"

/* Wer ist da.
 *
 * Eine eigene Route neben /org/running, und aus demselben Grund: Die Flächen,
 * die Anwesenheit brauchen, brauchen sie OFT — das Büro zeichnet sie neu,
 * solange man hinsieht, und ein Gespräch will wissen, ob der Angesprochene
 * gerade liest. Dafür jedes Mal die vollen Profile aller Menschen samt
 * Kennungen, Telefonnummern und Zuständigkeiten zu holen, wäre ein Vielfaches
 * an Daten für ein Feld.
 *
 * Zurück kommt der ZEITPUNKT, nicht ein Urteil. Ob fünf Minuten noch „da"
 * heißen, entscheidet die Fläche: Das Büro setzt jemanden an den Schreibtisch
 * oder lässt ihn leer, ein Gespräch schreibt „zuletzt gesehen um 14:02". Ein
 * Wahrheitswert von hier könnte beides nicht.
 */
func (s *Server) handlePresence(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	list, err := s.Org.Anwesende(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}
