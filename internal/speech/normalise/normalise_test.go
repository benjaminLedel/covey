package normalise

import (
	"strings"
	"testing"
	"time"
)

type tcase struct{ in, want string }

func run(t *testing.T, lang string, cases []tcase) {
	t.Helper()
	for _, c := range cases {
		got := Normalise(c.in, lang)
		if got != c.want {
			t.Errorf("%s %q\n got  %q\n want %q", lang, c.in, got, c.want)
			continue
		}
		// Idempotent: what it produced, it leaves alone.
		if again := Normalise(got, lang); again != got {
			t.Errorf("%s not idempotent on %q: %q", lang, got, again)
		}
	}
}

func TestGerman(t *testing.T) {
	run(t, "de-DE", []tcase{
		// The reply this issue was found with.
		{"Doch, ist drin – DLES-273, MR !475, alle Tests grün, wartet auf Gertruds Review.",
			"Doch, ist drin, D L E S zweihundertdreiundsiebzig, Merge Request vierhundertfünfundsiebzig, alle Tests grün, wartet auf Gertruds Review."},
		// Emojis.
		{"Fertig 🎉!", "Fertig!"},
		{"👍🏽 Passt, danke 🙏", "Passt, danke"},
		{"Familie 👨‍👩‍👧 ist da.", "Familie ist da."},
		{"Aus 🇩🇪 und 🏴󠁧󠁢󠁳󠁣󠁴󠁿 gemeldet.", "Aus und gemeldet."},
		{"Schritt 1️⃣ erledigt ✅", "Schritt erledigt"},
		{"❤️ Danke", "Danke"},
		{"Build → Test → Deploy", "Build, Test, Deploy"},
		// Numbers.
		{"Es sind 1.250 Tickets.", "Es sind eintausendzweihundertfünfzig Tickets."},
		{"21 offen", "einundzwanzig offen"},
		{"1 Ticket und 1 Datei", "ein Ticket und eine Datei"},
		{"Ergebnis: 1", "Ergebnis: eins"},
		{"1.200.000 Zeilen", "eine Million zweihunderttausend Zeilen"},
		{"101 Fälle", "einhunderteins Fälle"},
		{"Faktor 2,5", "Faktor zwei Komma fünf"},
		{"Temperatur -5 °C", "Temperatur minus fünf Grad Celsius"},
		{"Wert: 0,25", "Wert: null Komma zwei fünf"},
		// Ordinals.
		{"am 3. Oktober", "am dritten Oktober"},
		{"Der 1. Versuch", "Der erste Versuch"},
		{"zum 2. Mal", "zum zweiten Mal"},
		{"Es waren 3. Dann kam nichts.", "Es waren drei. Dann kam nichts."},
		// Percent, currency.
		{"12 % mehr", "zwölf Prozent mehr"},
		{"12% mehr", "zwölf Prozent mehr"},
		{"12,5 % weniger", "zwölf Komma fünf Prozent weniger"},
		{"Kostet 1.250 €.", "Kostet eintausendzweihundertfünfzig Euro."},
		{"Kostet €1.250", "Kostet eintausendzweihundertfünfzig Euro"},
		{"EUR 20 bitte", "zwanzig Euro bitte"},
		{"1 € pro Nutzer", "ein Euro pro Nutzer"},
		{"12,50 €", "zwölf Euro fünfzig"},
		{"0,99 €", "neunundneunzig Cent"},
		{"$5", "fünf Dollar"},
		{"USD 5", "fünf US-Dollar"},
		{"1,2 Mio. € Umsatz", "eins Komma zwei Millionen Euro Umsatz"},
		{"1 Mio. €", "eine Million Euro"},
		{"3 Mio. Aufrufe", "drei Millionen Aufrufe"},
		// Times, dates.
		{"um 10:30", "um zehn Uhr dreißig"},
		{"um 10:05 Uhr", "um zehn Uhr fünf"},
		{"ab 14:00", "ab vierzehn Uhr"},
		{"um 1:00", "um ein Uhr"},
		{"10:00–12:00 Uhr", "zehn Uhr bis zwölf Uhr"},
		{"30.09.2026", "dreißigster September zweitausendsechsundzwanzig"},
		{"am 30.09.2026", "am dreißigsten September zweitausendsechsundzwanzig"},
		{"Frist: 2026-09-30", "Frist: dreißigster September zweitausendsechsundzwanzig"},
		{"Stand: 2026-09-30T10:30:00Z", "Stand: dreißigster September zweitausendsechsundzwanzig, zehn Uhr dreißig"},
		{"bis 1.10. fertig", "bis ersten Oktober fertig"},
		{"seit 1999", "seit neunzehnhundertneunundneunzig"},
		{"3. Oktober 2026", "dritter Oktober zweitausendsechsundzwanzig"},
		// Ranges, units.
		{"10–20 Minuten", "zehn bis zwanzig Minuten"},
		{"10-20", "zehn bis zwanzig"},
		{"10 – 20 GB", "zehn bis zwanzig Gigabyte"},
		{"5 GB frei", "fünf Gigabyte frei"},
		{"1 GB", "ein Gigabyte"},
		{"250ms Latenz", "zweihundertfünfzig Millisekunden Latenz"},
		{"1 s", "eine Sekunde"},
		{"30 min", "dreißig Minuten"},
		{"2h", "zwei Stunden"},
		{"12 km", "zwölf Kilometer"},
		{"3 kg", "drei Kilogramm"},
		{"3x versucht", "dreimal versucht"},
		{"5k Nutzer", "fünftausend Nutzer"},
		// Codes.
		{"#31009 ist erledigt.", "Ticket einunddreißigtausendneun ist erledigt."},
		{"Ticket #31009 ist erledigt.", "Ticket einunddreißigtausendneun ist erledigt."},
		{"Issue #12", "Issue zwölf"},
		{"PR #480 ist offen", "Pull Request vierhundertachtzig ist offen"},
		{"!475 gemergt", "Merge Request vierhundertfünfundsiebzig gemergt"},
		{"SUP-481 offen", "S U P vierhunderteinundachtzig offen"},
		{"v0.12.0 ist raus", "Version null Punkt zwölf Punkt null ist raus"},
		{"Version 0.12.0", "Version null Punkt zwölf Punkt null"},
		{"Server 192.168.0.1", "Server einhundertzweiundneunzig Punkt einhundertachtundsechzig Punkt null Punkt eins"},
		{"Ruf 0171 an", "Ruf null eins sieben eins an"},
		{"Nummer 12345678", "Nummer eins zwei drei vier fünf sechs sieben acht"},
		{"+49 30 1234567", "plus vier neun drei null eins zwei drei vier fünf sechs sieben"},
		{"Commit a1b2c3d4 ist drin", "Commit ist drin"},
		{"3:1 gewonnen", "drei zu eins gewonnen"},
		// Abbreviations.
		{"CI ist grün, QA prüft die UI.", "C I ist grün, Q A prüft die U I."},
		{"Die API-URL und die ID", "Die A P I-U R L und die I D"},
		{"SSO über die DB", "S S O über die Datenbank"},
		{"z. B. Tests", "zum Beispiel Tests"},
		{"z.B. Tests", "zum Beispiel Tests"},
		{"Tests, Docs usw.", "Tests, Docs und so weiter."},
		{"ca. 5 Tickets bzw. mehr", "circa fünf Tickets beziehungsweise mehr"},
		{"Tom & Jerry", "Tom und Jerry"},
		// Links, code, markdown.
		{"Siehe https://example.org/x und https://example.org/y.", "Siehe (Link im Chat) und."},
		{"Siehe [die Doku](https://example.org/doc).", "Siehe die Doku."},
		{"Führe `make build` aus.", "Führe (Code im Chat) aus."},
		{"So:\n```go\nfmt.Println(1)\n```\nFertig", "So: (Code im Chat). Fertig."},
		{"## Stand\n- **Export** läuft\n- _Import_ wartet", "Stand. Export läuft. Import wartet."},
		{"| Name | Wert |\n|---|---|\n| A | 3 |", "Name, Wert. A, drei."},
		{"> zitiert\n1. erstens\n2. zweitens", "zitiert. erstens. zweitens."},
	})
}

func TestEnglish(t *testing.T) {
	run(t, "en", []tcase{
		{"Done 🎉!", "Done!"},
		{"👍🏻 Thanks 🙏", "Thanks"},
		{"We have 1,250 tickets.", "We have one thousand two hundred fifty tickets."},
		{"31009", "thirty-one thousand nine"},
		{"1,200,000 rows", "one million two hundred thousand rows"},
		{"Factor 2.5", "Factor two point five"},
		{"-3 degrees", "minus three degrees"},
		{"the 3rd try and the 21st", "the third try and the twenty-first"},
		{"12 % more", "twelve percent more"},
		{"12% more", "twelve percent more"},
		{"It costs €1,250.", "It costs one thousand two hundred fifty euros."},
		{"1,250 €", "one thousand two hundred fifty euros"},
		{"EUR 20", "twenty euros"},
		{"$5", "five dollars"},
		{"USD 5", "five US dollars"},
		{"$1", "one dollar"},
		{"$5.99", "five dollars and ninety-nine cents"},
		{"$0.50", "fifty cents"},
		{"at 10:30", "at ten thirty"},
		{"at 10:05", "at ten oh five"},
		{"at 14:00", "at two PM"},
		{"at 10:00", "at ten o'clock"},
		{"at 9:15 am", "at nine fifteen am"},
		{"on 2026-09-30", "on September thirtieth twenty twenty-six"},
		{"on 30.09.2026", "on September thirtieth twenty twenty-six"},
		{"on 9/30/2026", "on September thirtieth twenty twenty-six"},
		{"September 30, 2026", "September thirtieth, twenty twenty-six"},
		{"in 2026", "in twenty twenty-six"},
		{"10–20 minutes", "ten to twenty minutes"},
		{"10-20", "ten to twenty"},
		{"5 GB free", "five gigabytes free"},
		{"1 GB", "one gigabyte"},
		{"250ms", "two hundred fifty milliseconds"},
		{"1 s", "one second"},
		{"2h", "two hours"},
		{"12 km", "twelve kilometres"},
		{"3x", "three times"},
		{"#31009 is done.", "ticket thirty-one thousand nine is done."},
		{"Issue #12", "Issue twelve"},
		{"PR #480 is open", "pull request four hundred eighty is open"},
		{"MR !475", "merge request four hundred seventy-five"},
		{"SUP-481", "S U P four hundred eighty-one"},
		{"v0.12.0 is out", "version zero point twelve point zero is out"},
		{"call 5551234567", "call five five five one two three four five six seven"},
		{"hash 3f9a2b7c1d", "hash"},
		{"e.g. tests, docs etc.", "for example tests, docs et cetera."},
		{"The DB and the API", "The database and the A P I"},
		{"See https://example.org.", "See (link in the chat)."},
		{"Run `make build` now", "Run (code in the chat) now"},
		{"# Status\n* **Export** done\n* Import waits", "Status. Export done. Import waits."},
		{"Done – finally", "Done, finally"},
	})
}

// A language without number words keeps its numbers; the rest is done.
func TestOtherLanguage(t *testing.T) {
	run(t, "fr-FR", []tcase{
		{"C'est fait 🎉 – 12 tickets, voir https://example.org", "C'est fait, 12 tickets, voir"},
	})
}

func TestDetect(t *testing.T) {
	if got := Normalise("Das kostet 1.250 €.", ""); got != "Das kostet eintausendzweihundertfünfzig Euro." {
		t.Errorf("German guessed: %q", got)
	}
	if got := Normalise("This costs 1,250 €.", ""); got != "This costs one thousand two hundred fifty euros." {
		t.Errorf("English guessed: %q", got)
	}
}

func TestCleanTextIsUnchanged(t *testing.T) {
	for _, s := range []string{"Der Export läuft.", "Alles klar, bis morgen!", "The export is running.", "Hallo."} {
		if got := Normalise(s, ""); got != s {
			t.Errorf("%q became %q", s, got)
		}
	}
}

// Bounded and linear: a long hostile input returns quickly and cut.
func TestBounded(t *testing.T) {
	long := strings.Repeat("1.250 € 🎉 #31009 **x** `y` https://example.org ", 2000)
	start := time.Now()
	out := Normalise(long, "de")
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("took %v", d)
	}
	if out == "" {
		t.Fatal("nothing left")
	}
	if in := Normalise(strings.Repeat("a", MaxRunes+100), "de"); len(in) != MaxRunes {
		t.Fatalf("not cut: %d", len(in))
	}
}

func TestNumberWords(t *testing.T) {
	de := map[int64]string{0: "null", 1: "eins", 16: "sechzehn", 17: "siebzehn", 21: "einundzwanzig", 100: "einhundert",
		1001: "eintausendeins", 1250: "eintausendzweihundertfünfzig", 31009: "einunddreißigtausendneun",
		1_200_000: "eine Million zweihunderttausend", 2_000_001: "zwei Millionen eins", 3_000_000_000: "drei Milliarden"}
	for n, w := range de {
		if got := deCardinal(n); got != w {
			t.Errorf("de %d = %q, want %q", n, got, w)
		}
	}
	en := map[int64]string{0: "zero", 15: "fifteen", 42: "forty-two", 100: "one hundred", 1250: "one thousand two hundred fifty",
		31009: "thirty-one thousand nine", 1_000_000: "one million"}
	for n, w := range en {
		if got := enCardinal(n); got != w {
			t.Errorf("en %d = %q, want %q", n, got, w)
		}
	}
	for n, w := range map[int64]string{1: "erster", 3: "dritter", 7: "siebter", 20: "zwanzigster", 30: "dreißigster", 101: "einhunderterster"} {
		if got := deOrdinal(n, "er"); got != w {
			t.Errorf("de ordinal %d = %q, want %q", n, got, w)
		}
	}
	for n, w := range map[int64]string{1: "first", 2: "second", 12: "twelfth", 20: "twentieth", 23: "twenty-third", 100: "one hundredth"} {
		if got := enOrdinal(n, ""); got != w {
			t.Errorf("en ordinal %d = %q, want %q", n, got, w)
		}
	}
	for n, w := range map[int64]string{1999: "nineteen ninety-nine", 2005: "two thousand five", 1905: "nineteen oh five", 2026: "twenty twenty-six"} {
		if got := enYear(n); got != w {
			t.Errorf("en year %d = %q, want %q", n, got, w)
		}
	}
}
