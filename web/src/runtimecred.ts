// Spiegel von internal/claudeapi/runtimecred.go: welche Secret-Namen
// Laufzeit-Credentials sind und welcher Sorte.
//
// Bewusst doppelt gehalten statt über einen Endpunkt geholt: die Oberfläche
// braucht die Antwort beim Tippen im Namensfeld, also vor dem Speichern, und
// eine Runde zum Server pro Tastendruck wäre für zwei Zeichenkettenvergleiche
// verschwendet. Der Server bleibt die Instanz, die entscheidet — hier hängt
// nur das Etikett dran.

export type RuntimeKind = "api_key" | "oauth" | null;

const STEM_API = "anthropic_api_key";
const STEM_OAUTH = "claude_code_oauth_token";

const matchesStem = (key: string, stem: string) => key === stem || key.startsWith(stem + "_");

// classifyRuntimeCredential trifft den Stamm selbst oder Stamm + "_<Suffix>":
// anthropic_api_key_team_a zählt, anthropic_api_keyring nicht.
export function classifyRuntimeCredential(key: string): RuntimeKind {
  if (matchesStem(key, STEM_API)) return "api_key";
  if (matchesStem(key, STEM_OAUTH)) return "oauth";
  return null;
}

export const isRuntimeCredential = (key: string) => classifyRuntimeCredential(key) !== null;
