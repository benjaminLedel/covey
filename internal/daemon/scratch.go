package daemon

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// A run used to start in the root of the home, and nothing told the agent where
// the files a run makes along the way belong. Every parameter file, screenshot,
// log and comparison copy of every ticket therefore landed in `~` — 806 entries
// on one developer home after three weeks, `shot-*`, `suite-*`, `harness-*` by
// the dozen (#271). The home root is where a person opens the file browser and
// where the agent itself looks first; a heap there is read by nobody.
//
// So every task gets a directory of its own under ~/scratch, the run starts in
// it, and the prompt says what it is for. A directory no run has entered for
// scratchKeep is removed. That is a sweep, and tidy.go explains why the platform
// otherwise asks rather than sweeps: only the agent knows what a file in its home
// was for. Here the agent was told before it wrote the first file — whatever
// lies under ~/scratch/<task-id> was put there as scratch.

// scratchRoot is the directory under the home that holds one directory per task.
const scratchRoot = "scratch"

// scratchKeep is how long a task's directory outlives the last run that entered
// it. Long enough for a follow-up that comes the next day or after a weekend,
// short enough that the heap does not come back under another name.
const scratchKeep = 7 * 24 * time.Hour

// scratchDir creates ~/scratch/<task-id> and stamps it as entered now. It
// returns "" when there is no home or the task id is not one, or when the
// directory cannot be created — the run then starts in the home as before,
// which is untidy but not broken.
func scratchDir(home, taskID string) string {
	if home == "" || !isTaskID(taskID) {
		return ""
	}
	dir := filepath.Join(home, scratchRoot, taskID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	// The directory's mtime is what the sweep measures. Adding a file changes it
	// only for entries directly inside, so the run stamps it itself: "entered"
	// is the event that counts, not "written to at the top level".
	now := time.Now()
	_ = os.Chtimes(dir, now, now)
	return dir
}

// isTaskID accepts the canonical form of a UUID and nothing else. The name
// becomes a path element, and the sweep uses the same test to recognise what it
// may remove — a directory the agent named itself is never one of them.
func isTaskID(s string) bool {
	if len(s) != 36 {
		return false
	}
	_, err := uuid.Parse(s)
	return err == nil
}

// sweepScratch removes the task directories under ~/scratch that no run has
// entered for scratchKeep, except keep (the run about to start). Anything else
// in ~/scratch — a file, a symlink, a directory with another name — stays.
//
// A home materialised from the store on another runner gets fresh directory
// mtimes, since the manifest carries none. The sweep then waits a week longer
// than it would have; it never removes earlier.
func sweepScratch(home string, now time.Time, keep string) (int, error) {
	if home == "" {
		return 0, nil
	}
	root := filepath.Join(home, scratchRoot)
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var removed int
	var failed error
	for _, e := range entries {
		// DirEntry.IsDir is false for a symlink, so a link named like a task id
		// is not followed into wherever it points.
		if !e.IsDir() || !isTaskID(e.Name()) {
			continue
		}
		path := filepath.Join(root, e.Name())
		if path == keep {
			continue
		}
		info, err := e.Info()
		if err != nil || now.Sub(info.ModTime()) < scratchKeep {
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			failed = err
			continue
		}
		removed++
	}
	return removed, failed
}

// withScratch appends the paragraph that names the run's directory. Like the
// workplace it is added by the daemon and not compiled into the configuration:
// only the daemon that starts the run in that directory can truthfully say so.
func withScratch(prompt, dir string) string {
	if dir == "" {
		return prompt
	}
	doc := scratchDoc(filepath.Base(dir))
	if strings.TrimSpace(prompt) == "" {
		return doc
	}
	return prompt + "\n\n" + doc
}

func scratchDoc(taskID string) string {
	where := "~/" + scratchRoot + "/" + taskID + "/"
	return "## Where your files go\n\n" +
		"This run works in `" + where + "` — it is your working directory. It belongs to this task: " +
		"parameter files, screenshots, logs, a copy you compare against, anything that serves only this " +
		"task goes there. The same directory comes back when this task resumes, and it is removed a week " +
		"after the last run that entered it.\n\n" +
		"Your home (`~`) is your memory, not your desk. Put something there only when a later task will " +
		"need it, and then under a name that says what it is: a checkout under `~/repos/`, knowledge in your " +
		"wiki. Do not create files or directories directly in `~`."
}
