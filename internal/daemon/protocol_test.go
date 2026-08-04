package daemon

import (
	"encoding/json"
	"testing"
)

// responseTypes are the message types with which the control plane answers a
// REQUEST from the daemon. Each of them must find its waiting caller; if one is
// missing from the routing, the answer never arrives: the call runs into its
// timeout, and because some requests sit in the critical path before the run,
// the whole task stalls afterwards.
//
// That is exactly what happened when inject_skills was added — the route was
// missing, and every integration test ran into a 15-second timeout.
var responseTypes = []string{
	TypeInjectCredentials,
	TypeApprovalDecision,
	TypeInjectTarget,
	TypeInjectOrgChart,
	TypeInjectWiki,
	TypeInjectSkills,
	TypeInjectCreateTask,
}

// TestResponsesReachTheirCaller checks delivery by behavior instead of by
// comparing two lists: for every response type a real channel waits, and the
// message must arrive there. If the type is missing from the routing, the test
// fails — the same effect as in production, only in milliseconds.
func TestResponsesReachTheirCaller(t *testing.T) {
	c := &Client{pending: map[string]chan Message{}}

	for _, typ := range responseTypes {
		ch := make(chan Message, 1)
		c.pending["req-1"] = ch
		msg := Message{Type: typ, Payload: json.RawMessage(`{"request_id":"req-1"}`)}

		if !c.deliverIfResponse(msg) {
			t.Errorf("%s is not recognized as a response — the caller runs into its timeout", typ)
			continue
		}
		select {
		case got := <-ch:
			if got.Type != typ {
				t.Errorf("%s: wrong message delivered (%s)", typ, got.Type)
			}
		default:
			t.Errorf("%s: nothing arrived on the caller's channel", typ)
		}
	}
}

// A proactive push carries no request_id — it belongs in the read loop's switch
// and must not be swallowed as a response. inject_credentials occurs in both
// forms and is therefore the interesting case.
func TestPushIsNotTreatedAsResponse(t *testing.T) {
	c := &Client{pending: map[string]chan Message{}}
	push := Message{Type: TypeInjectCredentials, Payload: json.RawMessage(`{"system":"gitlab"}`)}
	if c.deliverIfResponse(push) {
		t.Fatal("a push without a request_id must not be routed as a response — otherwise it vanishes")
	}
}

// The list above is maintained by hand; this check at least catches the case
// that someone extends routedInjectTypes without pulling the behavior test
// along. A completely new type missing from BOTH stays undetected — the only
// remedy there is to add it here when it is built.
func TestRoutingTableMatchesTestedTypes(t *testing.T) {
	if len(routedInjectTypes) != len(responseTypes) {
		t.Fatalf("routedInjectTypes has %d entries, %d are checked — add the new response type to responseTypes",
			len(routedInjectTypes), len(responseTypes))
	}
	for _, typ := range responseTypes {
		if !routedInjectTypes[typ] {
			t.Errorf("%s is missing from routedInjectTypes", typ)
		}
	}
}
