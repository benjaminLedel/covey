package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"covey/internal/style"
)

// covey style: the style measurement on the command line, without a database.
//
//	covey style stats PATH... [--profile] [--holdout NAME] [--lang de|en] [--json]
//	covey style check FILE --profile TONE.md [--json]
//
// stats measures documents (.md, .txt; .docx/.odt/.html through pandoc when it
// is installed) and prints a table, or with --profile the ```style-profile```
// block for a TONE.md. check measures one text against such a file and prints
// the findings the style gate would report; exit code 1 when there are any.
func runStyle(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: covey style stats PATH... [--profile] [--holdout NAME] [--lang de|en] [--json]\n" +
			"       covey style check FILE --profile TONE.md [--json]")
	}
	switch args[0] {
	case "stats":
		return runStyleStats(args[1:])
	case "check":
		return runStyleCheck(args[1:])
	}
	return fmt.Errorf("covey style: unknown subcommand %q", args[0])
}

func runStyleStats(args []string) error {
	var paths, holdout []string
	profile, asJSON := false, false
	lang := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--profile":
			profile = true
		case "--json":
			asJSON = true
		case "--holdout":
			if i+1 >= len(args) {
				return errors.New("--holdout needs a file name")
			}
			holdout = append(holdout, args[i+1])
			i++
		case "--lang":
			if i+1 >= len(args) {
				return errors.New("--lang needs de or en")
			}
			lang = args[i+1]
			i++
		default:
			paths = append(paths, args[i])
		}
	}
	docs, err := collectDocuments(paths, holdout)
	if err != nil {
		return err
	}
	if len(docs) == 0 {
		return errors.New("no documents found")
	}
	var perDoc []style.Measurement
	var usable []style.Measurement
	var texts []string
	for _, d := range docs {
		m := style.Measure(d.text, lang)
		perDoc = append(perDoc, m)
		texts = append(texts, d.text)
		if m.Words >= 150 {
			usable = append(usable, m)
		}
	}
	corpus := style.Aggregate(perDoc)
	language := lang
	if language == "" {
		language = majorityLanguage(perDoc)
	}
	if profile {
		if len(usable) < 4 {
			fmt.Fprintln(os.Stderr, "note: fewer than 4 documents; bands are the observed min..max padded by 15 %, not a percentile spread")
		}
		words := 0
		for _, m := range perDoc {
			words += m.Words
		}
		lex := style.BuildLexicon(texts, 40)
		lexJSON, _ := json.Marshal(lex)
		p := style.Profile{Schema: style.Schema, Language: language, Documents: len(usable), Words: words,
			Holdout: holdout, Bands: style.BandsFrom(usable, corpus), Corpus: pickBands(corpus), Lexicon: lexJSON}
		out, err := json.MarshalIndent(p, "", " ")
		if err != nil {
			return err
		}
		fmt.Println("```style-profile")
		fmt.Println(string(out))
		fmt.Println("```")
		return nil
	}
	if asJSON {
		out, _ := json.MarshalIndent(map[string]any{"documents": perDoc, "corpus": corpus}, "", " ")
		fmt.Println(string(out))
		return nil
	}
	printStatsTable(docs, perDoc, corpus)
	return nil
}

func runStyleCheck(args []string) error {
	var file, profilePath string
	asJSON := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--profile":
			if i+1 >= len(args) {
				return errors.New("--profile needs a file")
			}
			profilePath = args[i+1]
			i++
		case "--json":
			asJSON = true
		default:
			file = args[i]
		}
	}
	if file == "" {
		return errors.New("covey style check needs a FILE")
	}
	text, err := readDocument(file)
	if err != nil {
		return err
	}
	var profile *style.Profile
	if profilePath != "" {
		raw, err := os.ReadFile(profilePath)
		if err != nil {
			return err
		}
		p, _, err := style.ParseProfile(string(raw))
		if err != nil {
			return fmt.Errorf("%s: %w", profilePath, err)
		}
		profile = &p
	}
	r := style.Check(text, profile)
	if asJSON {
		out, _ := json.MarshalIndent(r, "", " ")
		fmt.Println(string(out))
	} else {
		fmt.Print(style.Render(r, profile))
	}
	if r.Score > 0 {
		os.Exit(1)
	}
	return nil
}

type document struct {
	path string
	text string
}

var textSuffixes = map[string]bool{".md": true, ".markdown": true, ".txt": true}
var pandocSuffixes = map[string]bool{".docx": true, ".odt": true, ".html": true, ".rtf": true, ".epub": true}

func collectDocuments(paths, holdout []string) ([]document, error) {
	skip := map[string]bool{}
	for _, h := range holdout {
		skip[h] = true
	}
	var docs []document
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		var files []string
		if info.IsDir() {
			err := filepath.WalkDir(p, func(path string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				ext := strings.ToLower(filepath.Ext(path))
				if !d.IsDir() && (textSuffixes[ext] || pandocSuffixes[ext]) && d.Name() != "README.md" {
					files = append(files, path)
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
			sort.Strings(files)
		} else {
			files = []string{p}
		}
		for _, f := range files {
			if skip[filepath.Base(f)] || skip[f] {
				continue
			}
			text, err := readDocument(f)
			if err != nil {
				return nil, err
			}
			docs = append(docs, document{f, text})
		}
	}
	return docs, nil
}

// readDocument reads a text file, or converts an office document through pandoc.
func readDocument(path string) (string, error) {
	if pandocSuffixes[strings.ToLower(filepath.Ext(path))] {
		if _, err := exec.LookPath("pandoc"); err != nil {
			return "", fmt.Errorf("%s: converting %s needs pandoc on the PATH", filepath.Base(path), filepath.Ext(path))
		}
		out, err := exec.Command("pandoc", path, "-t", "gfm", "--wrap=none").Output()
		if err != nil {
			return "", fmt.Errorf("pandoc %s: %w", filepath.Base(path), err)
		}
		return string(out), nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func majorityLanguage(ms []style.Measurement) string {
	counts := map[string]int{}
	for _, m := range ms {
		counts[m.Language]++
	}
	if counts["en"] > counts["de"] {
		return "en"
	}
	return "de"
}

// pickBands keeps the corpus values of the metrics that have bands.
func pickBands(corpus map[string]float64) map[string]float64 {
	out := map[string]float64{}
	for k := range style.Label {
		if v, ok := corpus[k]; ok {
			out[k] = v
		}
	}
	return out
}

var statsKeys = []string{"sent_len_mean", "sent_len_cv", "long_sent_share", "para_len_mean",
	"nominalisation_rate", "long_word_rate", "copula_per_sentence", "stretch_verb_rate", "hedge_rate",
	"anchor_per_100_words", "para_without_anchor_share", "subordinators_per_sentence",
	"commas_per_sentence", "deep_sent_share", "du_per_1000", "sie_per_1000", "wir_per_1000",
	"ich_per_1000", "man_per_1000"}

func printStatsTable(docs []document, perDoc []style.Measurement, corpus map[string]float64) {
	names := make([]string, 0, len(docs)+1)
	for _, d := range docs {
		n := filepath.Base(d.path)
		if len(n) > 18 {
			n = n[:18]
		}
		names = append(names, n)
	}
	cols := make([]map[string]float64, 0, len(perDoc)+1)
	for _, m := range perDoc {
		v := map[string]float64{"words": float64(m.Words)}
		for k, x := range m.Values {
			v[k] = x
		}
		cols = append(cols, v)
	}
	if len(perDoc) > 1 {
		names = append(names, "CORPUS")
		c := map[string]float64{}
		for k, x := range corpus {
			c[k] = x
		}
		words := 0
		for _, m := range perDoc {
			words += m.Words
		}
		c["words"] = float64(words)
		cols = append(cols, c)
	}
	fmt.Printf("%-28s", "")
	for _, n := range names {
		fmt.Printf("  %18s", n)
	}
	fmt.Println()
	for _, k := range append([]string{"words"}, statsKeys...) {
		fmt.Printf("%-28s", k)
		for _, c := range cols {
			fmt.Printf("  %18s", trimNum(c[k]))
		}
		fmt.Println()
	}
}

func trimNum(v float64) string {
	s := fmt.Sprintf("%.3f", v)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "" {
		return "0"
	}
	return s
}
