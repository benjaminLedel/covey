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

func evalTon(t evalTone) string {
	return voice.EffectiveChatTone(voice.ChatTone{Address: t.Address, Tone: t.Tone, Emoji: t.Emoji, Note: t.Note}, voice.ChatTone{}).Prompt()
}

func evalTriagieren(ctx context.Context, p llm.Provider, r evalRahmen, organisation string, offen []Offen, fertig []Fertig, verlauf []Beitrag, nachricht string, suche *Suche) (Entscheidung, error) {
	return Triagieren(ctx, p, Rahmen(r), organisation, offen, fertig, verlauf, nachricht, suche)
}

func evalErzaehlen(ctx context.Context, p llm.Provider, r evalRahmen, verlauf []Beitrag, auftrag, ausgang, ergebnis string) (string, error) {
	return Erzaehlen(ctx, p, Rahmen(r), verlauf, auftrag, ausgang, ergebnis)
}

func evalAnthropic(cred string, oauth bool) llm.Provider { return llm.Anthropic(cred, oauth) }
