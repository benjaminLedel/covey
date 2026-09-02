package mail

// The HTML half of a message (#180).
//
// A mail from the installation used to be plain text only, on the argument
// that it carries one link and one sentence about why. The notification mail
// broke that: ten lines, each with an address of its own, and a footer — and
// as a monospace block nothing in it tells the reader which line needs them
// now. So every message now goes out as multipart/alternative: the text as
// before, and beside it an HTML part in the interface's own design language.
//
// What the HTML deliberately does NOT do: load a font, load an image, load
// anything. Every style is inline, every colour is a literal from the mockup
// (mockup/covey-ui-mockup.html), and the one font named is Inter with the
// system stack behind it — a client that blocks remote content shows exactly
// the same mail as one that does not, and a phishing filter finds nothing to
// chew on beyond the links the text part carries as well.

import (
	"html"
	"html/template"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Page is what the layout wraps: the installation's name over the top, a
// heading, the body, and a footer in the muted colour.
type Page struct {
	Site   string
	Title  string
	Body   template.HTML
	Footer template.HTML
}

// The palette, as in the mockup's light theme. A mail has no dark mode of its
// own worth the trouble: clients that invert do so on their own terms.
const (
	colourSurface0  = "#EFEDE4"
	colourSurface2  = "#FDFCF9"
	colourText      = "#1E1C17"
	colourSecondary = "#6B675E"
	colourMuted     = "#9C978B"
	colourBorder    = "#E1DED4"
	colourAccent    = "#185FA5"
	fontSans        = "Inter, ui-sans-serif, -apple-system, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif"
	fontVoice       = "Lora, Georgia, 'Times New Roman', serif"
)

var layout = template.Must(template.New("mail").Parse(`<!DOCTYPE html>
<html lang="{{.Lang}}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light">
<title>{{.Title}}</title>
</head>
<body style="margin:0;padding:0;background:` + colourSurface0 + `;color:` + colourText + `;font-family:` + fontSans + `;font-size:15px;line-height:1.55;-webkit-font-smoothing:antialiased;">
<table role="presentation" width="100%" cellspacing="0" cellpadding="0" border="0" style="background:` + colourSurface0 + `;">
<tr><td align="center" style="padding:32px 16px;">
<table role="presentation" width="100%" cellspacing="0" cellpadding="0" border="0" style="max-width:560px;">
<tr><td style="padding:0 6px 14px;font-family:` + fontVoice + `;font-style:italic;font-size:15px;color:` + colourSecondary + `;">{{.Site}}</td></tr>
<tr><td style="background:` + colourSurface2 + `;border:1px solid ` + colourBorder + `;border-radius:8px;padding:28px 30px;">
<h1 style="margin:0 0 16px;font-size:19px;line-height:1.3;font-weight:500;color:` + colourText + `;">{{.Title}}</h1>
{{.Body}}
</td></tr>
<tr><td style="padding:16px 6px 0;font-size:12px;line-height:1.5;color:` + colourMuted + `;">{{.Footer}}</td></tr>
</table>
</td></tr>
</table>
</body>
</html>
`))

// Render wraps a page in the layout. The result is complete HTML, ready for
// Message.HTML.
func Render(lang string, p Page) string {
	var b strings.Builder
	if err := layout.Execute(&b, struct {
		Lang string
		Page
	}{lang, p}); err != nil {
		// The template is a constant and the data is four strings; an error
		// here is a programming mistake, and an empty HTML part degrades to
		// the text part rather than to no mail.
		return ""
	}
	return b.String()
}

// Heading makes the HTML part's title out of a subject of the shape
// "<site>: <what>": the site's name is already over the card, so it comes
// off, and the rest starts with a capital — a subject continues the name, a
// heading stands on its own.
func Heading(subject, site string) string {
	t := strings.TrimPrefix(subject, site+": ")
	r, size := utf8.DecodeRuneInString(t)
	if r == utf8.RuneError {
		return t
	}
	return string(unicode.ToUpper(r)) + t[size:]
}

// The building blocks, each returning safe HTML.

// Paragraph wraps escaped text.
func Paragraph(text string) template.HTML {
	return template.HTML(`<p style="margin:0 0 14px;">` + html.EscapeString(text) + `</p>`)
}

// Link is an inline anchor in the accent colour.
func Link(href, text string) template.HTML {
	return template.HTML(`<a href="` + html.EscapeString(href) + `" style="color:` + colourAccent + `;text-decoration:underline;">` + html.EscapeString(text) + `</a>`)
}

// Button is the one action of a mail — the confirmation, the reset — as a
// block the thumb can hit. It is followed by the bare address in small print,
// for the reader whose client turns buttons into nothing.
func Button(href, text string) template.HTML {
	h := html.EscapeString(href)
	return template.HTML(`<p style="margin:20px 0;"><a href="` + h + `" style="display:inline-block;background:` + colourAccent + `;color:#ffffff;text-decoration:none;font-size:14px;font-weight:500;padding:10px 18px;border-radius:8px;">` + html.EscapeString(text) + `</a></p>` +
		`<p style="margin:0 0 14px;font-size:12px;color:` + colourMuted + `;word-break:break-all;">` + h + `</p>`)
}

// List renders lines, each optionally a link. An item without an address
// stands as text; one with an address is the link itself, so the whole line
// is the target and not a stray "here" at its end.
type Item struct {
	Text string
	Href string
}

func List(items []Item) template.HTML {
	var b strings.Builder
	b.WriteString(`<ul style="margin:0 0 14px;padding:0;list-style:none;">`)
	for _, it := range items {
		b.WriteString(`<li style="margin:0;padding:9px 0;border-top:1px solid ` + colourBorder + `;">`)
		if it.Href != "" {
			b.WriteString(string(Link(it.Href, it.Text)))
		} else {
			b.WriteString(html.EscapeString(it.Text))
		}
		b.WriteString(`</li>`)
	}
	b.WriteString(`</ul>`)
	return template.HTML(b.String())
}

var urlPattern = regexp.MustCompile(`https?://[^\s<>"]+`)

// FromText turns a plain-text body — the catalogue strings the confirmation
// and reset mails are made of — into HTML: paragraphs at blank lines, the
// rest escaped, addresses turned into links. A paragraph that is nothing but
// an address becomes the button, because in those mails that is the one thing
// to do. `action` is the button's label.
func FromText(body, action string) template.HTML {
	var b strings.Builder
	for _, para := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n\n") {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		if urlPattern.FindString(para) == para {
			b.WriteString(string(Button(para, action)))
			continue
		}
		b.WriteString(`<p style="margin:0 0 14px;">`)
		last := 0
		for _, m := range urlPattern.FindAllStringIndex(para, -1) {
			b.WriteString(strings.ReplaceAll(html.EscapeString(para[last:m[0]]), "\n", "<br>"))
			b.WriteString(string(Link(para[m[0]:m[1]], para[m[0]:m[1]])))
			last = m[1]
		}
		b.WriteString(strings.ReplaceAll(html.EscapeString(para[last:]), "\n", "<br>"))
		b.WriteString(`</p>`)
	}
	return template.HTML(b.String())
}
