package sandboxfs

import "errors"

// The file errors have to survive the journey over the runner link. Without
// this an agent's home on another host would answer every mistyped path with a
// 500 — the HTTP layer maps the sentinels below to their statuses, and an
// error that arrives as bare text carries none of that.

// ErrorKind names an error so it can be rebuilt on the other side.
func ErrorKind(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrNotFound):
		return "not_found"
	case errors.Is(err, ErrInvalidPath):
		return "invalid_path"
	case errors.Is(err, ErrTooLarge):
		return "too_large"
	case errors.Is(err, ErrNotDir):
		return "not_dir"
	case errors.Is(err, ErrIsDir):
		return "is_dir"
	case errors.Is(err, ErrExists):
		return "exists"
	case errors.Is(err, ErrTooMany):
		return "too_many"
	default:
		return ""
	}
}

// ErrorFromKind rebuilds it. An unknown kind keeps its message: better a plain
// error with the runner's own wording than a sentinel that fits it badly.
func ErrorFromKind(kind, message string) error {
	switch kind {
	case "not_found":
		return ErrNotFound
	case "invalid_path":
		return ErrInvalidPath
	case "too_large":
		return ErrTooLarge
	case "not_dir":
		return ErrNotDir
	case "is_dir":
		return ErrIsDir
	case "exists":
		return ErrExists
	case "too_many":
		return ErrTooMany
	default:
		return errors.New(message)
	}
}
