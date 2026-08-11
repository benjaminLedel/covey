package main

import (
	"context"
	"fmt"

	"covey/internal/config"
	"covey/internal/doctor"
)

// runDoctor prints what a restart or an upgrade would run into on this
// installation. The checks themselves live in internal/doctor, because the
// platform administration asks the same questions through the browser — and a
// check that exists only as a subcommand is one that effectively does not
// exist, which is the lesson the agent config lint learned first.
func runDoctor(ctx context.Context, cfg config.Config, args []string) error {
	quiet := false
	for _, a := range args {
		switch a {
		case "--quiet", "-q":
			quiet = true
		default:
			return fmt.Errorf("unknown option %q (--quiet)", a)
		}
	}

	report := doctor.Run(ctx, cfg)
	if !quiet || report.Blocking > 0 {
		printReport(report)
	}
	if report.Blocking > 0 {
		// A non-zero exit so this can stand in a deploy script before the
		// restart: whoever wires it in wants the deploy to stop, not a line in
		// a log nobody reads.
		return fmt.Errorf("%d of %d checks would keep agents from working",
			report.Blocking, len(report.Findings))
	}
	return nil
}

func printReport(report doctor.Report) {
	width := 0
	for _, f := range report.Findings {
		if len(f.What) > width {
			width = len(f.What)
		}
	}
	for _, f := range report.Findings {
		mark := "!"
		switch {
		case f.OK:
			mark = "·"
		case f.Blocking:
			mark = "×"
		}
		fmt.Printf("%s %-*s  %s\n", mark, width, f.What, f.Detail)
		if f.Remedy != "" {
			fmt.Printf("  %-*s  → %s\n", width, "", f.Remedy)
		}
	}
}
