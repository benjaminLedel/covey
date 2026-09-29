#!/bin/sh
# Appends a paragraph to a GitHub release's notes, once (#436).
#
#   tool/release_note.sh <tag> <paragraph>
#
# The Mac and the Windows job both append to the same release at about the
# same time, and `gh release edit` replaces the whole text: of two
# read-and-write rounds that overlap, the later one drops the earlier one's
# note. So the note goes in only when it is absent, is looked for again after
# a pause, and goes in again when the other job's write has taken it out.
# Running it twice leaves one note.
set -eu

tag="$1"
note="$2"

for pause in 5 10 20; do
  body="$(gh release view "$tag" --json body -q .body)"
  case "$body" in
    *"$note"*) ;;
    *) printf '%s\n\n%s\n' "$body" "$note" | gh release edit "$tag" --notes-file - >/dev/null ;;
  esac
  sleep "$pause"
  case "$(gh release view "$tag" --json body -q .body)" in
    *"$note"*) exit 0 ;;
  esac
done
echo "The note did not stay in the notes of ${tag}." >&2
exit 1
