package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"covey/internal/config"
	"covey/internal/db"
	runnerstore "covey/internal/runner/store"
)

// runHomeStore is the cleanup from the operator's side. It is the same pass the
// button in Administration → Runners triggers — the shared CleanupOrg — with
// two differences that matter on a machine in trouble: it needs no browser, and
// it covers every organisation instead of the one whose admin happens to be
// logged in. An organisation nobody looks after is exactly the one whose store
// grows until a deploy fails on a full disk.
//
// Preview is the default. Deleting is what --apply is for, because a command
// that frees space as a side effect of being run wrongly is one nobody dares
// put in a script.
func runHomeStore(ctx context.Context, cfg config.Config, args []string, log *slog.Logger) error {
	if len(args) == 0 || args[0] != "cleanup" {
		return errors.New("usage: covey home-store cleanup [--apply]")
	}
	apply := false
	for _, a := range args[1:] {
		switch a {
		case "--apply":
			apply = true
		default:
			return fmt.Errorf("unknown option %q (--apply)", a)
		}
	}
	if !cfg.HomeStore {
		return errors.New("the home store is switched off (COVEY_HOME_STORE)")
	}

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	blobs, where, err := openBlobStore(ctx, cfg, log)
	if err != nil {
		return err
	}
	store := runnerstore.NewStore(pool)
	orgs, err := store.OrgIDs(ctx)
	if err != nil {
		return err
	}

	fmt.Printf("home store: %s\n", where)
	if !apply {
		fmt.Println("preview — nothing is deleted; run again with --apply")
	}
	var blocks int
	var freed int64
	for _, id := range orgs {
		res, err := store.CleanupOrg(ctx, blobs, id, !apply)
		if err != nil {
			return fmt.Errorf("organisation %s: %w", id, err)
		}
		blocks += res.BlocksRemoved
		freed += res.FreedBytes
		fmt.Printf("  %s  snapshots %-4d blocks %-6d %s\n",
			id, res.Snapshots, res.BlocksRemoved, storeBytes(res.FreedBytes))
	}
	verb := "would free"
	if apply {
		verb = "freed"
	}
	fmt.Printf("%d organisation(s), %d block(s), %s %s\n",
		len(orgs), blocks, verb, storeBytes(freed))
	return nil
}

func storeBytes(n int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	v := float64(n)
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d %s", n, units[i])
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}
