package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"covey/internal/skills"
)

// covey skills validate: the checks of the skill import, run against a local
// directory or zip — the counterpart of `skills-ref validate` from the Agent
// Skills project, with covey's own limits on top. It needs neither database nor
// key, so it runs in a repository's CI just as well as on a laptop.

func runSkills(args []string) error {
	if len(args) == 0 || args[0] != "validate" {
		return errors.New("usage: covey skills validate [DIR|FILE.zip]...\n" +
			"  checks every skill (a directory holding a SKILL.md) below each argument; default: the current directory")
	}
	return runSkillsValidate(os.Stdout, args[1:])
}

func runSkillsValidate(out io.Writer, paths []string) error {
	if len(paths) == 0 {
		paths = []string{"."}
	}
	failed, total := 0, 0
	for _, p := range paths {
		cands, err := readSkillsAt(p)
		if err != nil {
			fmt.Fprintf(out, "FAIL %s: %v\n", p, err)
			failed++
			continue
		}
		for _, c := range cands {
			total++
			where := filepath.Join(p, filepath.FromSlash(c.Dir))
			if c.Dir == "" {
				// A skill at the top of the argument: the directory the
				// argument names is its directory, and the specification wants
				// the name to match it.
				if abs, err := filepath.Abs(p); err == nil && !strings.HasSuffix(strings.ToLower(p), ".zip") {
					if base := filepath.Base(abs); c.Spec.Name != "" && c.Spec.Name != base {
						c.Problems = append(c.Problems,
							fmt.Sprintf("frontmatter name %q does not match directory %q", c.Spec.Name, base))
					}
				}
			}
			if len(c.Problems) == 0 {
				fmt.Fprintf(out, "ok   %s (%s): %d files\n", c.Spec.Name, where, len(c.Spec.Files))
			} else {
				failed++
				fmt.Fprintf(out, "FAIL %s (%s)\n", c.Spec.Name, where)
				for _, prob := range c.Problems {
					fmt.Fprintf(out, "     - %s\n", prob)
				}
			}
			for _, s := range c.Skipped {
				fmt.Fprintf(out, "     skipped: %s\n", s)
			}
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d skills failed", failed, total)
	}
	return nil
}

func readSkillsAt(p string) ([]skills.Candidate, error) {
	info, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		return skills.FromZip(data, "")
	}
	return skills.FromFS(os.DirFS(p))
}
