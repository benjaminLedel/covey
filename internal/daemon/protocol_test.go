package daemon

import "testing"

// Jede Anfrage des Daemons wartet auf eine Antwort, die die Lese-Schleife über
// eine EXPLIZITE Liste von Nachrichtentypen zustellt (Client.Run). Fehlt ein
// inject_*-Typ dort, kommt die Antwort nie an: Der Aufruf läuft in seinen
// Timeout, und weil manche Anfragen vor dem Lauf im kritischen Pfad sitzen,
// steht danach die ganze Aufgabe.
//
// Genau das ist beim Einbau von inject_skills passiert — die Route fehlte, und
// jeder Integrationstest lief in einen Timeout. Dieser Test hält die Liste
// gegen die Antworttypen, damit der nächste Einbau daran scheitert statt an
// einem 600-Sekunden-Testlauf.
func TestEveryInjectTypeIsRoutable(t *testing.T) {
	// Antworttypen, die eine Anfrage des Daemons beantworten und deshalb über
	// route() ihren Wartekanal finden müssen.
	responses := []string{
		TypeInjectCredentials,
		TypeApprovalDecision,
		TypeInjectTarget,
		TypeInjectOrgChart,
		TypeInjectWiki,
		TypeInjectSkills,
		TypeInjectCreateTask,
	}
	for _, typ := range responses {
		if !routedInjectTypes[typ] {
			t.Errorf("%s beantwortet eine Anfrage, wird aber in Client.Run nicht geroutet — "+
				"der Aufrufer läuft in seinen Timeout", typ)
		}
	}
}
