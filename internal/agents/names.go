package agents

// The name generator: funny but human-sounding agent names, language-dependent
// (de/en). Two worlds, mixed — down-to-earth (first name + a diligent or
// bureaucratic surname: "Renate Büroklammer" / "Reg of Clipboard") and made-up
// names assembled from syllables ("Wuselbert Wibbelzahn" / "Bumblewick
// Snickerpip"). Several patterns (double-barrelled, title, middle initial,
// nobility gag) add variance on top.
//
// It used to live in the frontend only. Setup and the People department need it
// server-side now (spec/20), and a second generator would drift from the first
// — so it lives here and the interface asks for a roll.
//
// Names are rolled rather than derived: an agent called `support-agent-2` is an
// artefact of a process, an agent called Renate Büroklammer is a colleague.

// math/rand and not crypto/rand, deliberately, and the four #nosec G404 below
// say so at each site: nothing here guards anything. The dice decide whether an
// agent is called Renate Büroklammer or Wuselbert Wibbelzahn. Two identical
// rolls are a coincidence somebody smiles at and renames, not a collision that
// costs something — what has to be unique is the slug, and that is the
// database's job, not the dice's.
import (
	"math/rand"
	"regexp"
	"strings"
)

type namePool struct {
	firstNames     []string
	virtueSurnames []string // the diligent tone
	officeSurnames []string // the silly note
	titles         []string
	noble          string // nobility particle: "von" / "of"
	// Made-up syllables: prefix/suffix for invented first and last names.
	fFirstPre, fFirstSuf []string
	fSurPre, fSurSuf     []string
}

var poolDE = namePool{
	firstNames: []string{
		"Bernd", "Uschi", "Detlef", "Heike", "Klaus-Dieter", "Ingrid",
		"Waltraud", "Horst", "Renate", "Günther", "Sieglinde", "Hubert",
		"Edeltraud", "Manfred", "Roswitha", "Egon", "Hannelore", "Norbert",
		"Gisela", "Jürgen", "Lieselotte", "Rüdiger", "Erika", "Karl-Heinz",
		"Frieda", "Achim", "Brunhilde", "Ottfried", "Hildegard", "Volker",
		"Gerlinde", "Reinhold", "Traudl", "Dieter", "Helga", "Wolfgang",
		"Marlies", "Bruno", "Elfriede", "Hartmut", "Käthe", "Lothar",
		"Ursel", "Friedhelm", "Gudrun", "Werner", "Irmgard", "Siegfried",
		"Annegret", "Eberhard", "Meinhard", "Waldtraut", "Kunibert", "Adelheid",
		"Gottfried", "Hertha", "Bodo", "Liesel", "Willibald", "Ottilie",
		"Reinhard", "Gertrud", "Ewald", "Cordula", "Alfons", "Doris",
		"Gunter", "Rosemarie", "Hans-Peter", "Bärbel", "Fiete", "Elke",
		"Knut", "Roswita", "Heinz-Rüdiger", "Mechthild", "Olaf", "Ute",
	},
	virtueSurnames: []string{
		"Fleißig", "Emsig", "Hurtig", "Wacker", "Redlich", "Munter",
		"Flink", "Tüchtig", "Unverzagt", "Pünktlich", "Gründlich", "Beflissen",
		"Sonnig", "Freundlich", "Rastlos", "Schaffig", "Eifrig", "Tatkräftig",
		"Zuverlässig", "Bienenfleißig", "Sorgfältig", "Rührig", "Betriebsam",
		"Umtriebig", "Hilfsbereit", "Gewissenhaft", "Aufgeweckt", "Strebsam",
		"Findig", "Geflissentlich", "Behände", "Wohlgemut", "Unermüdlich",
		"Dienstbeflissen", "Vorbildlich", "Beherzt", "Wieselflink",
	},
	officeSurnames: []string{
		"Klemmbrett", "Büroklammer", "Tackernagel", "Aktendeckel", "Stempelkissen",
		"Umlaufmappe", "Ablagekorb", "Frankiermeister", "Lochermann", "Heftstreifen",
		"Registerreiter", "Trennblatt", "Posteingang", "Terminkalender", "Klebezettel",
		"Textmarker", "Radiergummi", "Bleistiftspitzer", "Aktenordner", "Sichthülle",
		"Karteikasten", "Pendelmappe", "Kaffeeküche", "Umlaufbeschluss", "Sammelmappe",
		"Notizblock", "Faxgerät", "Wählscheibe", "Vorzimmer", "Konferenzkeks",
		"Sitzungsprotokoll", "Dienstsiegel", "Poststelle", "Hängeregister", "Bürostuhl",
		"Kabelbinder", "Gummiband", "Eddingstift", "Locherstärke", "Rollcontainer",
		"Tischkalender", "Prospekthülle", "Aktenschrank", "Büroklammerich", "Ringbuch",
		"Serienbrief", "Wartemarke", "Kopiervorlage", "Stehordner", "Dienstweg",
	},
	titles:    []string{"Dr.", "Prof.", "Dipl.-Ing.", "Mag.", "Dr. Dr."},
	noble:     "von",
	fFirstPre: []string{"Wusel", "Knuff", "Schnuffel", "Plörr", "Grummel", "Flausch", "Knuddel", "Wibbel", "Zwuckel", "Möppel", "Brummel", "Schlonz", "Muckel", "Fussel", "Tröt", "Quassel", "Schnatter", "Kringel", "Pömpel", "Nöhl", "Wubbel", "Purzel", "Bibber", "Schrubbel"},
	fFirstSuf: []string{"bert", "hilde", "fried", "traud", "mund", "gunde", "olf", "rich", "inchen", "bald", "hard", "linde", "fix", "wupp", "dolf", "gard"},
	fSurPre:   []string{"Wibbel", "Schnatter", "Tapp", "Schlonz", "Kringel", "Pömpel", "Nöhl", "Rumpel", "Klöter", "Schrubb", "Bibber", "Fizzel", "Knatter", "Wupp", "Möpp", "Schlick", "Trödel", "Purzel", "Zwirbel", "Bommel", "Nuckel", "Quietsch"},
	fSurSuf:   []string{"zahn", "schnut", "bommel", "wupp", "strunz", "knopf", "docht", "pömpel", "zwick", "fuß", "näschen", "brömmel", "datsch", "pütz", "kötter", "schwupp"},
}

var poolEN = namePool{
	firstNames: []string{
		"Barbara", "Nigel", "Doris", "Keith", "Brenda", "Colin",
		"Gladys", "Trevor", "Mildred", "Derek", "Norma", "Reg",
		"Ethel", "Clive", "Beryl", "Gordon", "Deirdre", "Malcolm",
		"Sheila", "Roy", "Maureen", "Cyril", "Edna", "Bernard",
		"Pam", "Stan", "Glenda", "Harold", "Vera", "Alan",
		"Sandra", "Ronald", "Pauline", "Terry", "Marjorie", "Dennis",
		"Susan", "Neville", "Carol", "Kenneth", "Enid", "Graham",
		"Sylvia", "Leonard", "Hilda", "Wendy", "Bert", "Agnes",
		"Frank", "Doreen", "Herbert", "Joyce", "Percy", "Iris", "Mavis",
	},
	virtueSurnames: []string{
		"Diligent", "Chipper", "Nimble", "Tireless", "Prompt", "Thorough",
		"Steadfast", "Trusty", "Earnest", "Zealous", "Sprightly", "Dutiful",
		"Chirpy", "Sunny", "Keen", "Sturdy", "Plucky", "Brisk",
		"Cheerful", "Hearty", "Willing", "Spry", "Bustling", "Industrious",
		"Eager", "Reliable", "Meticulous", "Nifty", "Perky", "Dapper",
		"Wholesome", "Bright-Eyed", "Can-Do", "Unflagging",
	},
	officeSurnames: []string{
		"Paperclip", "Stapler", "Clipboard", "Binder", "Highlighter", "Hole-Punch",
		"Lanyard", "Spreadsheet", "Filofax", "Foolscap", "Letterhead", "Envelope",
		"Postbag", "Rubber-Stamp", "Treasury-Tag", "Bulldog-Clip", "Sticky-Note",
		"Whiteboard", "Pigeonhole", "Inbox", "Memo", "Franking-Machine", "Guillotine",
		"Ledger", "Ring-Binder", "Photocopier", "Watercooler", "Teabag", "Biscuit-Tin",
		"Swivel-Chair", "Fax-Machine", "Rolodex", "Index-Card", "Drawing-Pin",
		"Manila-Folder", "Desk-Tidy", "Correction-Fluid", "Ballpoint", "Minutes",
		"Agenda", "Cubicle", "Kettle", "Lever-Arch", "Flip-Chart", "Name-Badge",
		"Jiffy-Bag", "Comb-Binding", "Hot-Desk", "Tea-Trolley", "Suggestion-Box",
		// More neutral / US office terms, for less of a UK slant:
		"Post-It", "Sharpie", "Legal-Pad", "Scotch-Tape", "Push-Pin", "Thumbtack",
		"Cork-Board", "Dry-Erase", "Mouse-Pad", "File-Cabinet", "Paper-Shredder",
		"Toner-Cartridge", "Notepad", "Cubicle-Wall", "Break-Room", "Standing-Desk",
		"Coffee-Mug", "Desk-Calendar", "Time-Card", "Vending-Machine", "Wite-Out",
		"Dictaphone", "Intercom", "Expense-Report", "Org-Chart", "Water-Cooler",
		"Cubicle-Farm", "Trapper-Keeper", "Paper-Tray", "Rubber-Band", "Copy-Machine",
	},
	titles:    []string{"Dr.", "Prof.", "Sir", "Dame", "Rev."},
	noble:     "of",
	fFirstPre: []string{"Bumble", "Snuggle", "Wobble", "Fizz", "Snicker", "Bramble", "Pomple", "Wiggle", "Grumble", "Fuddle", "Noodle", "Doodle", "Boffle", "Muddle", "Toddle", "Higgle", "Wuzzle", "Crumple", "Snoodle", "Pockle", "Jangle", "Wimple"},
	fFirstSuf: []string{"wick", "ory", "bert", "ora", "pip", "kins", "dorf", "boodle", "snout", "wump", "dora", "fizzle", "bottom", "willow", "monk", "ington"},
	fSurPre:   []string{"Snicker", "Fluffer", "Wiggle", "Bumble", "Pickle", "Noodle", "Wobble", "Snuffle", "Cringle", "Higgle", "Squabble", "Doodle", "Puddle", "Grumble", "Twiddle", "Fiddle", "Muddle", "Bimble", "Wonker", "Plimp", "Gobble", "Whiffle"},
	fSurSuf:   []string{"pip", "nutter", "snort", "bottom", "doodle", "wump", "sniff", "puff", "knocker", "bee", "whistle", "mop", "pop", "dinkle", "wobble", "britches"},
}

func poolFor(lang string) namePool {
	if strings.HasPrefix(strings.ToLower(lang), "en") {
		return poolEN
	}
	return poolDE
}

var nameInitials = strings.Split("ABCDEFGHIJKLMNOPRSTUVW", "")

// #nosec G404 — a name, not a secret (see the note at the import).
func pick(list []string) string { return list[rand.Intn(len(list))] }

// surname: the office objects (the joke) a little more often.
// #nosec G404 — a name, not a secret (see the note at the import).
func (p namePool) surname() string {
	if rand.Float64() < 0.55 {
		return pick(p.officeSurnames)
	}
	return pick(p.virtueSurnames)
}

func twoSurnames(sur func() string) (string, string) {
	a := sur()
	b := sur()
	for guard := 0; b == a && guard < 5; guard++ {
		b = sur()
	}
	return a, b
}

// collapse shortens three identical letters at the seam ("Knuff"+"fix") to a
// double. By hand over the runes rather than with a regexp: Go's has no
// backreference, so `(.)\1{2,}` — the frontend's expression — has no equivalent.
func collapse(s string) string {
	rs := []rune(s)
	out := make([]rune, 0, len(rs))
	run := 0
	for i, r := range rs {
		if i > 0 && r == rs[i-1] {
			run++
		} else {
			run = 0
		}
		if run < 2 {
			out = append(out, r)
		}
	}
	return string(out)
}

func (p namePool) fantasyFirst() string   { return collapse(pick(p.fFirstPre) + pick(p.fFirstSuf)) }
func (p namePool) fantasySurname() string { return collapse(pick(p.fSurPre) + pick(p.fSurSuf)) }

// baseName applies the shared patterns (double-barrelled, title, initial,
// nobility gag) to a given first name and a surname generator.
// #nosec G404 — a name, not a secret (see the note at the import).
func (p namePool) baseName(first string, sur func() string) string {
	switch r := rand.Float64(); {
	case r < 0.6:
		return first + " " + sur()
	case r < 0.74:
		a, b := twoSurnames(sur)
		return first + " " + a + "-" + b
	case r < 0.86:
		return pick(p.titles) + " " + first + " " + sur()
	case r < 0.94:
		return first + " " + pick(nameInitials) + ". " + sur()
	default:
		return first + " " + p.noble + " " + sur()
	}
}

// RolledName is one roll: the display name and the slug derived from it.
type RolledName struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// RollName generates an agent name for the given language ("de"/"en", anything
// else falls back to German). ~40% made-up names, otherwise down-to-earth ones.
// #nosec G404 — a name, not a secret (see the note at the import).
func RollName(lang string) RolledName {
	p := poolFor(lang)
	fantasy := rand.Float64() < 0.4
	first := pick(p.firstNames)
	sur := p.surname
	if fantasy {
		first = p.fantasyFirst()
		sur = p.fantasySurname
	}
	name := p.baseName(first, sur)
	return RolledName{Name: name, Slug: Slugify(name)}
}

var slugKill = regexp.MustCompile(`[^a-z0-9]+`)

// Slugify turns a display name into the slug that ends up in URLs and in the
// webhook address. Kept identical to the frontend's version — the field is
// filled in live while typing there.
func Slugify(name string) string {
	s := strings.ToLower(name)
	for from, to := range map[string]string{"ä": "ae", "ö": "oe", "ü": "ue", "ß": "ss"} {
		s = strings.ReplaceAll(s, from, to)
	}
	return strings.Trim(slugKill.ReplaceAllString(s, "-"), "-")
}
