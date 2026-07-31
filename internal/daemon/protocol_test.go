package daemon

import (
	"encoding/json"
	"testing"
)

// responseTypes sind die Nachrichtentypen, mit denen die Control Plane eine
// ANFRAGE des Daemons beantwortet. Jede davon muss ihren wartenden Aufrufer
// finden; fehlt eine im Routing, kommt die Antwort nie an: Der Aufruf läuft in
// seinen Timeout, und weil manche Anfragen vor dem Lauf im kritischen Pfad
// sitzen, steht danach die ganze Aufgabe.
//
// Genau das ist beim Einbau von inject_skills passiert — die Route fehlte, und
// jeder Integrationstest lief in einen 15-Sekunden-Timeout.
var responseTypes = []string{
	TypeInjectCredentials,
	TypeApprovalDecision,
	TypeInjectTarget,
	TypeInjectOrgChart,
	TypeInjectWiki,
	TypeInjectSkills,
	TypeInjectCreateTask,
}

// TestResponsesReachTheirCaller prüft die Zustellung am Verhalten statt am
// Abgleich zweier Listen: Für jeden Antworttyp wartet ein echter Kanal, und die
// Nachricht muss dort ankommen. Fehlt der Typ im Routing, schlägt der Test fehl
// — dieselbe Wirkung wie im Betrieb, nur in Millisekunden.
func TestResponsesReachTheirCaller(t *testing.T) {
	c := &Client{pending: map[string]chan Message{}}

	for _, typ := range responseTypes {
		ch := make(chan Message, 1)
		c.pending["req-1"] = ch
		msg := Message{Type: typ, Payload: json.RawMessage(`{"request_id":"req-1"}`)}

		if !c.deliverIfResponse(msg) {
			t.Errorf("%s wird nicht als Antwort erkannt — der Aufrufer läuft in seinen Timeout", typ)
			continue
		}
		select {
		case got := <-ch:
			if got.Type != typ {
				t.Errorf("%s: falsche Nachricht zugestellt (%s)", typ, got.Type)
			}
		default:
			t.Errorf("%s: nichts am Kanal des Aufrufers angekommen", typ)
		}
	}
}

// Ein proaktiver Push trägt keine request_id — er gehört in den switch der
// Lese-Schleife und darf nicht als Antwort verschluckt werden.
// inject_credentials kommt in beiden Formen vor und ist deshalb der
// interessante Fall.
func TestPushIsNotTreatedAsResponse(t *testing.T) {
	c := &Client{pending: map[string]chan Message{}}
	push := Message{Type: TypeInjectCredentials, Payload: json.RawMessage(`{"system":"gitlab"}`)}
	if c.deliverIfResponse(push) {
		t.Fatal("ein Push ohne request_id darf nicht als Antwort geroutet werden — sonst verschwindet er")
	}
}

// Die Liste oben ist handgepflegt; diese Prüfung fängt wenigstens den Fall ab,
// dass jemand routedInjectTypes erweitert, ohne den Verhaltenstest mitzuziehen.
// Ein völlig neuer Typ, der in BEIDEN fehlt, bleibt unentdeckt — dagegen hilft
// nur, ihn beim Einbau hier einzutragen.
func TestRoutingTableMatchesTestedTypes(t *testing.T) {
	if len(routedInjectTypes) != len(responseTypes) {
		t.Fatalf("routedInjectTypes hat %d Einträge, geprüft werden %d — neuen Antworttyp in responseTypes ergänzen",
			len(routedInjectTypes), len(responseTypes))
	}
	for _, typ := range responseTypes {
		if !routedInjectTypes[typ] {
			t.Errorf("%s fehlt in routedInjectTypes", typ)
		}
	}
}
