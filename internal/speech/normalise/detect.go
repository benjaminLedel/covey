package normalise

import (
	"strings"
	"unicode"
)

/* Language says which language a short text is in, for the checks that keep
 * a call in one language (#511): a spoken form written in another language
 * than its written answer, a clean-up that translated what it should only
 * have tidied. Both are dropped rather than said, so the detector has to be
 * sure or say nothing: a wrong guess drops a good text, no guess keeps it.
 *
 * It counts function words that belong to one language only, and letters
 * only one of them writes; Japanese and Chinese are told by their script.
 * A text too short to tell ("Hi!", "Okay.") and a text where two languages
 * come close give "". detect, the normaliser's own guess, is different: it
 * has to choose between German and English whatever the text. */

// sichere are the words that count for one language only. A word that two of
// these languages share ("die", "was", "an", "in", "es", "no", "que") is in
// none of the lists.
var sichere = map[string]map[string]bool{
	"de": set("der", "das", "und", "ist", "nicht", "ich", "du", "wir", "ein", "eine", "einen", "mit", "für", "auf", "den",
		"dem", "sind", "wird", "noch", "auch", "schon", "aber", "oder", "wenn", "dass", "bitte", "danke", "hallo", "heute",
		"habe", "hab", "hat", "bei", "von", "nach", "doch", "nein", "kann", "bin", "gerade", "warum", "mir", "mich", "dir",
		"dich", "mal", "gern", "gerne", "jetzt", "wie", "sehr", "gut", "warte", "schau", "plötzlich", "sprichst", "englisch",
		"deutsch", "ja", "zu", "zum", "im", "vom", "sich", "uns", "euch", "läuft", "wieder", "kein", "keine", "was", "wer"),
	"en": set("the", "and", "is", "are", "not", "you", "we", "it", "with", "for", "on", "of", "to", "this", "that", "be",
		"will", "have", "has", "please", "thanks", "hello", "today", "at", "yes", "done", "i", "my", "your", "what",
		"why", "how", "now", "right", "just", "do", "don", "can", "looking", "waiting", "speak", "speaking", "english",
		"suddenly", "they", "there", "been", "would", "could", "should", "from", "about", "it's", "i'm", "ask", "sure"),
	"fr": set("le", "les", "et", "est", "pas", "tu", "nous", "vous", "il", "elle", "une", "avec", "pour", "sur",
		"dans", "qui", "mais", "oui", "merci", "bonjour", "cette", "suis", "très", "c'est", "au", "aux", "ou", "ça", "être",
		"presque", "encore", "déjà", "rien", "tout", "fait", "peux"),
	"es": set("el", "los", "las", "y", "está", "pero", "con", "para", "por", "gracias", "hola", "muy", "también", "estoy",
		"soy", "del", "lo", "sí", "hay", "puedo", "ahora", "cuando", "donde", "porque", "usted", "hoy", "bien", "mi"),
	"it": set("il", "gli", "e", "è", "non", "sono", "che", "della", "per", "con", "una", "grazie", "ciao", "anche", "ma",
		"questo", "questa", "sei", "siamo", "molto", "adesso", "oggi", "perché", "bene", "io", "lei", "del", "nel"),
	"nl": set("de", "het", "een", "en", "is", "niet", "ik", "jij", "wij", "we", "met", "voor", "op", "van", "dat",
		"maar", "ook", "nog", "dank", "hallo", "vandaag", "heb", "heeft", "bij", "naar", "nee", "kan", "ben", "waarom"),
	"pt": set("o", "os", "as", "e", "não", "eu", "você", "nós", "com", "para", "uma", "obrigado", "obrigada", "olá",
		"também", "estou", "sou", "muito", "agora", "hoje", "bem", "isso", "isto", "do", "da", "dos", "das", "em", "no"),
	"pl": set("i", "nie", "jest", "się", "ja", "ty", "my", "wy", "że", "to", "na", "w", "z", "do", "dla", "jak", "tak",
		"ale", "czy", "dziękuję", "cześć", "dzisiaj", "teraz", "bardzo", "jestem", "mam", "już"),
}

// eigene are letters one of these languages writes and the others do not.
var eigene = map[string]string{
	"de": "äöüß", "fr": "çèêëîïôœ", "es": "ñ¿¡", "pt": "ãõ", "pl": "ąęłśżźćń", "it": "ì",
}

// Language returns the base code of the language text is in ("de", "en",
// "fr", "es", "it", "nl", "pt", "pl", "ja", "zh"), or "" when it cannot tell
// for sure.
func Language(text string) string {
	if len(text) > 4*MaxRunes {
		text = text[:4*MaxRunes]
	}
	kana, han, latin := 0, 0, 0
	for _, r := range text {
		switch {
		case unicode.Is(unicode.Hiragana, r), unicode.Is(unicode.Katakana, r):
			kana++
		case unicode.Is(unicode.Han, r):
			han++
		case unicode.Is(unicode.Latin, r):
			latin++
		}
	}
	switch {
	case kana >= 2 && kana+han >= latin:
		return "ja"
	case han >= 2 && han >= latin:
		return "zh"
	}
	zaehl := map[string]int{}
	for w := range strings.FieldsFuncSeq(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && r != '\''
	}) {
		w = strings.Trim(w, "'")
		for lang, liste := range sichere {
			if liste[w] {
				zaehl[lang]++
			}
		}
		for lang, zeichen := range eigene {
			if strings.ContainsAny(w, zeichen) {
				zaehl[lang] += 2
			}
		}
	}
	best, erste, zweite := "", 0, 0
	for lang, n := range zaehl {
		switch {
		case n > erste:
			best, erste, zweite = lang, n, erste
		case n > zweite:
			zweite = n
		}
	}
	// Sure means: at least three signs, and twice as many as any other
	// language has.
	if erste < 3 || erste < 2*zweite {
		return ""
	}
	return best
}

// SameLanguage says whether b is not known to be in another language than
// a: false only when both can be told and they differ.
func SameLanguage(a, b string) bool {
	la, lb := Language(a), Language(b)
	return la == "" || lb == "" || la == lb
}
