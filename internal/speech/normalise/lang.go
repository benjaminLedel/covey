package normalise

import (
	"strings"
	"unicode"
)

// language is everything a language says differently: its number words,
// its separators and the few words the normaliser puts in.
type language struct {
	code     string
	cardinal func(int64) string
	ordinal  func(int64, string) string
	year     func(int64) string
	digits   [10]string
	// thousands and decimal are the language's own separators; the other
	// language's are read too where they cannot mean anything else.
	thousands, decimal byte
	months             [13]string

	comma, dot, minus, plus, to, score, percent, version, ticket, mergeRequest, pullRequest string
	about, moreThan, lessThan, atLeast, atMost, and, equals, section, times, thousandWord   string
	linkPhrase, codePhrase                                                                  string
}

var german = &language{
	code: "de", cardinal: deCardinal, ordinal: deOrdinal, year: deYear,
	digits:    [10]string{"null", "eins", "zwei", "drei", "vier", "fünf", "sechs", "sieben", "acht", "neun"},
	thousands: '.', decimal: ',',
	months: [13]string{"", "Januar", "Februar", "März", "April", "Mai", "Juni", "Juli", "August", "September", "Oktober", "November", "Dezember"},
	comma:  "Komma", dot: "Punkt", minus: "minus", plus: "plus", to: "bis", score: "zu", percent: "Prozent",
	version: "Version", ticket: "Ticket", mergeRequest: "Merge Request", pullRequest: "Pull Request",
	about: "etwa", moreThan: "mehr als", lessThan: "weniger als", atLeast: "mindestens", atMost: "höchstens",
	and: "und", equals: "gleich", section: "Paragraf", times: "mal", thousandWord: "Tausend",
	linkPhrase: "(Link im Chat)", codePhrase: "(Code im Chat)",
}

var english = &language{
	code: "en", cardinal: enCardinal, ordinal: enOrdinal, year: enYear,
	digits:    [10]string{"zero", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine"},
	thousands: ',', decimal: '.',
	months: [13]string{"", "January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"},
	comma:  "point", dot: "point", minus: "minus", plus: "plus", to: "to", score: "to", percent: "percent",
	version: "version", ticket: "ticket", mergeRequest: "merge request", pullRequest: "pull request",
	about: "about", moreThan: "more than", lessThan: "less than", atLeast: "at least", atMost: "at most",
	and: "and", equals: "equals", section: "section", times: "times", thousandWord: "thousand",
	linkPhrase: "(link in the chat)", codePhrase: "(code in the chat)",
}

// pick takes the request's language (a BCP 47 tag) and otherwise guesses
// between German and English from the words. Another language gets the
// language-free steps only: nil.
func pick(tag, text string) *language {
	base, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(tag)), "-")
	base, _, _ = strings.Cut(base, "_")
	switch base {
	case "de":
		return german
	case "en":
		return english
	case "":
		return detect(text)
	}
	return nil
}

var (
	germanWords = set("der", "die", "das", "und", "ist", "nicht", "ich", "du", "wir", "sie", "es", "ein", "eine", "einen",
		"mit", "für", "auf", "den", "dem", "sind", "wird", "noch", "auch", "schon", "aber", "oder", "wenn", "dass",
		"bitte", "danke", "hallo", "heute", "habe", "hat", "um", "im", "am", "zum", "bei", "von", "nach", "doch", "ja", "nein")
	englishWords = set("the", "and", "is", "are", "not", "you", "we", "it", "a", "an", "with", "for", "on", "of", "to",
		"this", "that", "be", "will", "was", "have", "has", "please", "thanks", "hello", "today", "at", "in", "yes", "no", "done")
)

func set(words ...string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}

// detect counts common words; umlauts and ß count for German. A tie is
// English.
func detect(text string) *language {
	de, en := 0, 0
	for w := range strings.FieldsFuncSeq(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) }) {
		if germanWords[w] {
			de++
		}
		if englishWords[w] {
			en++
		}
		if strings.ContainsAny(w, "äöüß") {
			de += 2
		}
	}
	if de > en {
		return german
	}
	return english
}

// monthOf says which month a word names, in either language.
func monthOf(w string) int {
	for m := 1; m <= 12; m++ {
		if strings.EqualFold(w, german.months[m]) || strings.EqualFold(w, english.months[m]) {
			return m
		}
	}
	return 0
}

// unit is a unit symbol and how each language says it.
type unit struct {
	deOne, deSing, dePlural string // deOne is the article for one: "eine Sekunde"
	enSing, enPlural        string
}

func (u unit) say(l *language, one bool) string {
	switch {
	case l == german && one:
		return u.deSing
	case l == german:
		return u.dePlural
	case one:
		return u.enSing
	}
	return u.enPlural
}

func bytesUnit(de, en string) unit { return unit{"ein", de, de, en, en + "s"} }

// units after a number, case-sensitive: "m" is a metre, "M" is not.
var units = map[string]unit{
	"B": bytesUnit("Byte", "byte"), "KB": bytesUnit("Kilobyte", "kilobyte"), "kB": bytesUnit("Kilobyte", "kilobyte"),
	"MB": bytesUnit("Megabyte", "megabyte"), "GB": bytesUnit("Gigabyte", "gigabyte"), "TB": bytesUnit("Terabyte", "terabyte"),
	"KiB": bytesUnit("Kibibyte", "kibibyte"), "MiB": bytesUnit("Mebibyte", "mebibyte"), "GiB": bytesUnit("Gibibyte", "gibibyte"),
	"ms":   {"eine", "Millisekunde", "Millisekunden", "millisecond", "milliseconds"},
	"s":    {"eine", "Sekunde", "Sekunden", "second", "seconds"},
	"sec":  {"eine", "Sekunde", "Sekunden", "second", "seconds"},
	"Sek":  {"eine", "Sekunde", "Sekunden", "second", "seconds"},
	"min":  {"eine", "Minute", "Minuten", "minute", "minutes"},
	"Min":  {"eine", "Minute", "Minuten", "minute", "minutes"},
	"h":    {"eine", "Stunde", "Stunden", "hour", "hours"},
	"Std":  {"eine", "Stunde", "Stunden", "hour", "hours"},
	"km":   {"ein", "Kilometer", "Kilometer", "kilometre", "kilometres"},
	"m":    {"ein", "Meter", "Meter", "metre", "metres"},
	"cm":   {"ein", "Zentimeter", "Zentimeter", "centimetre", "centimetres"},
	"mm":   {"ein", "Millimeter", "Millimeter", "millimetre", "millimetres"},
	"kg":   {"ein", "Kilogramm", "Kilogramm", "kilogram", "kilograms"},
	"g":    {"ein", "Gramm", "Gramm", "gram", "grams"},
	"km/h": {"ein", "Kilometer pro Stunde", "Kilometer pro Stunde", "kilometre per hour", "kilometres per hour"},
	"°C":   {"ein", "Grad Celsius", "Grad Celsius", "degree Celsius", "degrees Celsius"},
	"°F":   {"ein", "Grad Fahrenheit", "Grad Fahrenheit", "degree Fahrenheit", "degrees Fahrenheit"},
	"°":    {"ein", "Grad", "Grad", "degree", "degrees"},
	"px":   {"ein", "Pixel", "Pixel", "pixel", "pixels"},
}

// currency is a currency and how each language says it and its hundredth.
type currency struct {
	de, deOne          string // "fünf US-Dollar", "ein US-Dollar"
	enSing, enPlural   string
	deCent, deCentOne  string
	enCent, enCentPlur string
}

var (
	euro     = currency{"Euro", "ein Euro", "euro", "euros", "Cent", "ein Cent", "cent", "cents"}
	dollar   = currency{"Dollar", "ein Dollar", "dollar", "dollars", "Cent", "ein Cent", "cent", "cents"}
	usDollar = currency{"US-Dollar", "ein US-Dollar", "US dollar", "US dollars", "Cent", "ein Cent", "cent", "cents"}
	pound    = currency{"Pfund", "ein Pfund", "pound", "pounds", "Pence", "ein Penny", "penny", "pence"}
	gbp      = currency{"britische Pfund", "ein britisches Pfund", "British pound", "British pounds", "Pence", "ein Penny", "penny", "pence"}
	franc    = currency{"Franken", "ein Franken", "Swiss franc", "Swiss francs", "Rappen", "ein Rappen", "centime", "centimes"}
)

// currencies are the symbols and codes an amount can carry, before or
// after it.
var currencies = map[string]currency{
	"€": euro, "EUR": euro, "$": dollar, "US$": usDollar, "USD": usDollar, "£": pound, "GBP": gbp, "CHF": franc,
}

func (c currency) say(l *language, one bool) string {
	switch {
	case l == german && one:
		return c.deOne
	case l == german:
		return c.de
	case one:
		return "one " + c.enSing
	}
	return c.enPlural
}

func (c currency) sayCents(l *language, n int64) string {
	switch {
	case l == german && n == 1:
		return c.deCentOne
	case l == german:
		return deCardinal(n) + " " + c.deCent
	case n == 1:
		return "one " + c.enCent
	}
	return enCardinal(n) + " " + c.enCentPlur
}

// Abbreviations spoken whole. Spelled acronyms are read letter by letter
// in both languages; the rest are words of one language.
var acronyms = map[string]string{
	"CI": "C I", "CD": "C D", "CI/CD": "C I C D", "QA": "Q A", "UI": "U I", "UX": "U X", "API": "A P I", "APIs": "A P Is",
	"URL": "U R L", "URLs": "U R Ls", "ID": "I D", "IDs": "I Ds", "SSO": "S S O", "CLI": "C L I", "SDK": "S D K",
}

var acronymsBy = map[string]map[string]string{
	"de": {"MR": "Merge Request", "MRs": "Merge Requests", "PR": "Pull Request", "PRs": "Pull Requests", "DB": "Datenbank", "DBs": "Datenbanken"},
	"en": {"MR": "merge request", "MRs": "merge requests", "PR": "pull request", "PRs": "pull requests", "DB": "database", "DBs": "databases"},
}

// dotted abbreviations, looked up in lower case without their last dot;
// ends says the dot may also end the sentence.
type abbrev struct {
	words string
	ends  bool
}

var abbrevsBy = map[string]map[string]abbrev{
	"de": {
		"z.b": {"zum Beispiel", false}, "usw": {"und so weiter", true}, "etc": {"et cetera", true}, "ca": {"circa", false},
		"bzw": {"beziehungsweise", false}, "d.h": {"das heißt", false}, "u.a": {"unter anderem", true}, "nr": {"Nummer", false},
		"inkl": {"inklusive", false}, "ggf": {"gegebenenfalls", false}, "evtl": {"eventuell", false}, "bspw": {"beispielsweise", false},
		"z.t": {"zum Teil", false}, "vs": {"versus", false}, "mio": {"Millionen", true}, "mrd": {"Milliarden", true},
		"e.g": {"zum Beispiel", false}, "min": {"Minuten", true}, "std": {"Stunden", true}, "sek": {"Sekunden", true},
	},
	"en": {
		"e.g": {"for example", false}, "i.e": {"that is", false}, "etc": {"et cetera", true}, "ca": {"circa", false},
		"vs": {"versus", false}, "approx": {"approximately", false}, "no": {"number", false},
		"z.b": {"for example", false}, "usw": {"and so on", true}, "bzw": {"respectively", false},
	},
}

// Two-part abbreviations: "z. B.".
var pairAbbrevsBy = map[string]map[string]string{
	"de": {"z b": "zum Beispiel", "d h": "das heißt", "u a": "unter anderem", "z t": "zum Teil", "o ä": "oder Ähnliches"},
	"en": {"e g": "for example", "i e": "that is"},
}

// Words before "#123" that already say what the number is.
var numberNouns = set("ticket", "tickets", "issue", "issues", "pr", "prs", "mr", "request", "requests", "nr", "nummer",
	"number", "no", "anfrage", "fall", "case", "bug", "story", "task", "aufgabe", "item", "vorgang", "incident", "#")

// The words before a German ordinal that fix its ending.
var (
	deDativeWords = set("am", "im", "vom", "zum", "zur", "beim", "den", "dem", "des", "seit", "ab", "bis", "ihrem", "seinem", "unserem", "jedem")
	deNomWords    = set("der", "die", "das", "jeder", "jede", "jedes")
	yearWords     = set("im", "jahr", "jahre", "jahres", "jahren", "seit", "anno", "sommer", "winter", "herbst", "frühjahr",
		"in", "since", "until", "year", "of")
)

// deEnding is the ending of an ordinal after prev, and whether prev fixes it.
func deEnding(prev string) (string, bool) {
	p := strings.ToLower(prev)
	switch {
	case deDativeWords[p]:
		return "en", true
	case deNomWords[p]:
		return "e", true
	}
	return "er", false
}

// deFeminine guesses from a noun's ending whether "1" before it is "eine".
func deFeminine(noun string) bool {
	n := strings.ToLower(noun)
	for _, suf := range []string{"ung", "heit", "keit", "ion", "tät", "ei", "schaft", "ik", "e", "mail", "nachricht"} {
		if strings.HasSuffix(n, suf) {
			return true
		}
	}
	return false
}
