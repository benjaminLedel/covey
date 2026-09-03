package style

import (
	"regexp"
	"sort"
	"strings"
)

// ClaimDiff lists what a revision dropped and what it added: numbers with their
// unit, URLs and mail addresses, quoted passages, inline code, tokens with inner
// capitals or digits or in all caps, and runs of two or more capitalised words
// not opened by an article. Multisets: a number that appeared twice and survives
// once is reported.
type ClaimDiff struct {
	Before  int           `json:"before"`
	After   int           `json:"after"`
	Missing []ClaimAnchor `json:"missing"`
	Added   []ClaimAnchor `json:"added"`
}

type ClaimAnchor struct {
	Kind  string `json:"kind"`
	Text  string `json:"text"`
	Count int    `json:"count"`
}

var (
	reCodeSpan     = regexp.MustCompile("`([^`\n]+)`")
	reQuotedGroups = regexp.MustCompile(`„([^“”"]{3,})[“”]|"([^"\n]{3,})"|«([^»\n]{3,})»|“([^”\n]{3,})”`)
)

// ClaimAnchors extracts the anchors of a text as a multiset.
func ClaimAnchors(text string) map[Anchor]int {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = reFrontMatter.ReplaceAllString(text, "")
	c := map[Anchor]int{}
	for _, m := range reCodeSpan.FindAllStringSubmatch(text, -1) {
		c[Anchor{"code", strings.TrimSpace(m[1])}]++
	}
	stripped := reCodeSpan.ReplaceAllString(text, " ")
	for _, m := range reURL.FindAllString(stripped, -1) {
		c[Anchor{"url", strings.TrimRight(m, ".,;:)")}]++
	}
	stripped = reURL.ReplaceAllString(stripped, " ")
	for _, m := range reQuotedGroups.FindAllStringSubmatch(stripped, -1) {
		for _, g := range m[1:] {
			if g != "" {
				c[Anchor{"quote", strings.TrimSpace(reSpaces.ReplaceAllString(g, " "))}]++
				break
			}
		}
	}
	for _, n := range findNumbers(stripped) {
		v := strings.TrimRight(strings.TrimSpace(reSpaces.ReplaceAllString(n, " ")), ".,:")
		if v != "" {
			c[Anchor{"number", v}]++
		}
	}
	for _, t := range findTokens(stripped) {
		c[Anchor{"token", t}]++
	}
	for _, n := range findNameRuns(stripped, reNameRunDE, reNameWordDE, false) {
		c[Anchor{"name", reSpaces.ReplaceAllString(n, " ")}]++
	}
	return c
}

// Diff compares the anchors of two versions.
func Diff(before, after string) ClaimDiff {
	a, b := ClaimAnchors(before), ClaimAnchors(after)
	d := ClaimDiff{Missing: []ClaimAnchor{}, Added: []ClaimAnchor{}}
	for k, n := range a {
		d.Before += n
		if n > b[k] {
			d.Missing = append(d.Missing, ClaimAnchor{k.Kind, k.Text, n - b[k]})
		}
	}
	for k, n := range b {
		d.After += n
		if n > a[k] {
			d.Added = append(d.Added, ClaimAnchor{k.Kind, k.Text, n - a[k]})
		}
	}
	sortAnchors(d.Missing)
	sortAnchors(d.Added)
	return d
}

func sortAnchors(xs []ClaimAnchor) {
	sort.Slice(xs, func(i, j int) bool {
		if xs[i].Kind != xs[j].Kind {
			return xs[i].Kind < xs[j].Kind
		}
		return xs[i].Text < xs[j].Text
	})
}
