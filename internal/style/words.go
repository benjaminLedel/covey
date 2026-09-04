package style

import "strings"

// Word lists. German first, English beside it; both are consulted regardless
// of the detected language because mixed texts are common. They mirror the
// covey-style skill's textmetrics.py and are kept identical to it.

func set(words string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(words) {
		m[w] = true
	}
	return m
}

var stopwordsDE = set(`der die das ein eine einer eines einem einen und oder aber doch sondern denn
weil dass daß ob wenn als wie nicht kein keine keinen keiner keinem ist sind war
waren wird werden wurde wurden hat haben hatte hatten sein seine seiner seinem
seinen ihr ihre ihrer ihrem ihren sich ich du er sie es wir mit von zu zum zur
auf in im an am aus bei nach über unter vor durch für gegen ohne um bis seit
dem den des auch nur noch schon so dann da hier dort was wer wo man mehr sehr
kann können könnte muss müssen soll sollen sollte will wollen darf dürfen
diese dieser dieses diesem diesen jede jeder jedes alle allen aller etwas
nichts sich mich dich uns euch ihnen ihm ihn wurde ganz also`)

var stopwordsEN = set(`the a an and or but nor so yet for of to in on at by from with without
about into over under before after between through during is are was were be
been being have has had do does did will would shall should can could may
might must this that these those it its he she they them his her their we us
our you your i me my not no yes as if then than too very just only also more
most some any all each every such there here what which who whom whose when
where why how`)

// Nominalisation suffixes, matched on lowercase words of at least 7 letters.
var nominalSuffixes = []string{
	"ung", "ungen", "heit", "heiten", "keit", "keiten", "ismus", "ierung", "ierungen",
	"tion", "tionen", "ität", "itäten", "schaft", "schaften",
	"tions", "sion", "sions", "ment", "ments", "ness", "ity", "ities", "ance", "ence",
	"ism", "isms", "ization", "isation",
}

// Copulas and light verbs: a sentence whose verb work is done by these carries
// its meaning in nouns instead.
var copulas = set(`ist sind war waren wird werden wurde wurden sei wäre wären bleibt bleiben
is are was were be been being remains remain`)

var stretchVerbPatterns = []string{
	`\berfolg(?:t|en|te|ten)\b`,
	`\bfind(?:et|en)\s+\w*\s*statt\b`,
	`\bstell(?:t|en|te|ten)\s+(?:\w+\s+){0,3}dar\b`,
	`\bzur\s+(?:anwendung|verfügung|durchführung|umsetzung|geltung|sprache|kenntnis)\b`,
	`\bin\s+(?:betracht|anspruch|erwägung|kraft|frage|angriff)\b`,
	`\bunter\s+beweis\b`,
	`\bzum\s+(?:einsatz|ausdruck|abschluss|tragen)\b`,
	`\bvornehm(?:en|t)\b`,
	`\bdurchführ(?:en|t)\b`,
	`\bgewährleist(?:en|et)\b`,
	`\bbewerkstellig(?:en|t)\b`,
	`\btake\s+(?:place|into\s+account|into\s+consideration)\b`,
	`\bmake\s+(?:a|an|the)\s+(?:decision|assessment|determination|contribution|use)\b`,
	`\bconduct(?:s|ed)?\s+(?:a|an)\s+\w+\b`,
	`\bperform(?:s|ed)?\s+(?:a|an)\s+\w+\b`,
	`\bprovide(?:s|d)?\s+(?:support|assistance|guidance)\b`,
	`\bis\s+(?:able|possible|necessary|important)\s+to\b`,
	`\bthere\s+(?:is|are|was|were)\b`,
}

// Subordinators: a proxy for clause nesting.
var subordinators = set(`dass daß weil obwohl wenn falls während nachdem bevor damit sodass ob indem
sofern soweit wobei welche welcher welches seitdem solange sobald als
that because although though if while whereas after before whether unless
until since which whose whom where when`)

// Hedges and fillers: words that soften without adding. Multi-word entries are
// matched as phrases.
var hedges = []string{
	"grundsätzlich", "im grunde", "letztlich", "letztendlich", "gewissermaßen", "sozusagen",
	"durchaus", "sicherlich", "in gewisser weise", "quasi", "eigentlich", "im prinzip",
	"prinzipiell", "generell", "in der regel", "tendenziell", "gegebenenfalls",
	"unter umständen", "möglicherweise", "vielfältig", "ganzheitlich", "nachhaltig",
	"zukunftsweisend", "spannend", "entscheidend", "essenziell", "essentiell",
	"basically", "essentially", "ultimately", "arguably", "generally", "in general",
	"in principle", "somewhat", "rather", "quite", "fairly", "in a sense", "to some extent",
	"potentially", "possibly", "holistic", "robust", "seamless", "leverage", "delve", "crucial",
	"pivotal", "vital", "landscape", "journey", "empower", "streamline",
}

// Example markers: the author is about to get concrete.
var examplePatternsDE = []string{
	`\bzum\s+beispiel\b`, `\bz\.\s?b\.`, `\bbeispielsweise\b`, `\betwa\b`, `\bkonkret\b`,
	`\bnehmen\s+wir\b`, `\bstell(?:en\s+sie|\s+dir)\s+(?:sich\s+)?vor\b`, `\bangenommen\b`,
	`\bein\s+beispiel\b`,
}

var examplePatternsEN = []string{
	`\bfor\s+example\b`, `\be\.\s?g\.`, `\bfor\s+instance\b`, `\bsay\b,`, `\bsuppose\b`,
	`\bimagine\b`, `\btake\b\s+(?:a|the)\b`, `\bconsider\b`, `\bconcretely\b`, `\bin\s+practice\b`,
}

var abbreviations = set(`z.b bzw usw etc vgl ca dr prof nr u.a d.h bspw evtl ggf inkl max min mr mrs ms
e.g i.e vs st jr no fig approx sog abs art bd hrsg jh mio mrd tel str geb gest`)

var monthsDE = []string{"Januar", "Februar", "März", "April", "Mai", "Juni", "Juli", "August",
	"September", "Oktober", "November", "Dezember", "Jahrhundert", "Klasse", "Platz", "Mal",
	"Auflage", "Kapitel", "Absatz", "Satz"}

// Words that open a capitalised run without making it a name: "Der Vorstand",
// "Diese Woche", "The Board".
var runStarters = set(`der die das des dem den ein eine einer eines einem einen dies diese dieser
dieses diesem diesen jede jeder jedes alle viele manche keine kein im am in an auf aus bei
mit nach vor für von zum zur über unter und oder aber auch wenn als wie was wer wo es er
sie wir ich ihr man the a an this these that those each every all many some no in on at by
for from with to of and or but if as when it he she we they you i there here what who how why`)

// Paragraph openers that link to the paragraph before: connectives and
// demonstratives. A text whose paragraphs never open with one starts over at
// every paragraph, however good the sentences are.
var linkers = set(`deshalb darum daher deswegen also aber doch denn dazu dabei damit davon dafür
dagegen danach dann das dies diese dieser dieses dort hier so sonst trotzdem dennoch
außerdem zudem ebenso genau und weil wenn statt stattdessen
so but because that this these those then therefore yet still also and instead which
here there hence thus otherwise meanwhile however`)

// Sentence openers that are articles or pronouns.
var pronounOpeners = set(`der die das ein eine es ich wir sie er dies diese dieser dieses the a an
it i we they he she this these there`)

func isNameRun(run string) bool {
	first := strings.ToLower(strings.Fields(run)[0])
	return !runStarters[first]
}
