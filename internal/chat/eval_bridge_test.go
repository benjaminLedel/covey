package chat

import (
	"context"

	"github.com/google/uuid"

	"covey/internal/llm"
	"covey/internal/voice"
)

/* The seam between the evaluation and the code it evaluates (eval_test.go).
 *
 * Everything the set calls of the turns goes through these few functions, so
 * that the same scenarios can be run against an older state of the prompts —
 * a copy of the set with this one file adapted — and the two reports put side
 * by side. Nothing here decides anything. */

func evalRaum(conv Conversation, agentID uuid.UUID, von string, angesprochen bool) string {
	return Raum(conv, agentID, von, angesprochen)
}

func chatTon(t evalTone) voice.ChatTone {
	return voice.ChatTone{Address: t.Address, Tone: t.Tone, Emoji: t.Emoji, Note: t.Note}
}

func evalVoiceID(name string) uuid.UUID {
	if name == "" {
		return uuid.Nil
	}
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("eval-voice:"+name))
}

func evalSlots(named map[string]string) voice.Slots {
	out := voice.Slots{}
	for occ, name := range named {
		if id := evalVoiceID(name); id != uuid.Nil {
			out[voice.Occasion(occ)] = id
		}
	}
	return out
}

func evalAudienceDept(d evalDepartment) voice.AudienceDepartment {
	return voice.AudienceDepartment{ID: uuid.NewSHA1(uuid.NameSpaceOID, []byte("eval-dept:"+d.Name)),
		Name: d.Name, Note: d.Note, Slots: evalSlots(d.Voices)}
}

// evalStimme is the chat's voice for a scenario the way the server chooses
// it (httpapi chatStimme, #471): the person's department, the others in a
// group, voice.Choose over the agent's and the organisation's slots, the
// chosen voice's chat tone over the organisation's — and, while no chat
// voice is named, the chat tone of the agent's customers voice.
func evalStimme(sc evalScenario) evalWahl {
	var aud voice.Audience
	if d, ok := sc.abteilung(sc.Person.Department); ok {
		a := evalAudienceDept(d)
		aud.Addressed = &a
	}
	if sc.gruppe() {
		for _, m := range sc.Conversation.Members {
			if d, ok := sc.abteilung(m.Department); ok && m.Kind != MemberAgent {
				aud.Others = append(aud.Others, evalAudienceDept(d))
			}
		}
	}
	c := voice.Choose(voice.OccasionChat, aud, evalSlots(sc.Voices.Agent), evalSlots(sc.Voices.Org))
	w := evalWahl{Grund: c.Reason(), Publikum: voice.AudiencePrompt(c.Notes)}
	for _, n := range c.Notes {
		w.Abteilungen = append(w.Abteilungen, n.Department)
	}
	byID := map[uuid.UUID]string{}
	for name := range sc.Voices.Library {
		byID[evalVoiceID(name)] = name
	}
	var eigen evalTone
	if name, ok := byID[c.VoiceID]; ok && c.Found() {
		lib := sc.Voices.Library[name]
		v := voice.Voice{Name: name, ReleasedCard: lib.Card}
		for _, p := range lib.Passages {
			v.Exemplars = append(v.Exemplars, voice.Exemplar{Role: "passage", Text: p})
		}
		w.Name, w.Stimme, eigen = name, voice.ChatPrompt(v), lib.ChatTone
	} else if !c.Found() {
		eigen = sc.Voices.Library[sc.Voices.Agent[string(voice.OccasionCustomers)]].ChatTone
	}
	t := voice.EffectiveChatTone(chatTon(eigen), chatTon(sc.Tone))
	w.Ton = t.Prompt()
	w.Effektiv = evalTone{Address: t.Address, Tone: t.Tone, Emoji: t.Emoji, Note: t.Note}
	return w
}

func evalTriagieren(ctx context.Context, p llm.Provider, r evalRahmen, organisation string, offen []Offen, fertig []Fertig, verlauf []Beitrag, nachricht string, suche *Suche) (Entscheidung, error) {
	return Triagieren(ctx, p, Rahmen(r), organisation, offen, fertig, verlauf, nachricht, suche)
}

func evalErzaehlen(ctx context.Context, p llm.Provider, r evalRahmen, verlauf []Beitrag, auftrag, ausgang, ergebnis string) (string, error) {
	return Erzaehlen(ctx, p, Rahmen(r), verlauf, auftrag, ausgang, ergebnis)
}

func evalAnthropic(cred string, oauth bool) llm.Provider { return llm.Anthropic(cred, oauth) }
