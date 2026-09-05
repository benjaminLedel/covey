package homestore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"
)

// SyncResult is what a sync cost and what it produced.
type SyncResult struct {
	ManifestHash string
	TotalSize    int64
	// Blocks/BytesUp is what actually travelled — the figure that says whether
	// the deduplication is doing its job. A typical run changes megabytes in a
	// 7 GB home.
	Blocks   int
	BytesUp  int64
	Excluded int
}

// askBatch/askBatchBytes bound one bundled question. 512 blocks turn six
// figures of round trips into three, and the byte cap keeps the buffer honest
// when the blocks are large — 32 MiB is what a runner may hold for this, not
// what a home may contain.
const (
	askBatch      = 512
	askBatchBytes = 32 << 20
)

// transferWorkers is how many blocks are in flight at once, in either
// direction. Eight is enough to hide a remote store's round trip behind the
// disk and small enough that a runner host does not feel it (#175).
const transferWorkers = 8

// pendingBlock is a block waiting for its answer. data is nil for a block the
// stat cache vouched for: it is read from disk only if the store lacks it.
type pendingBlock struct {
	hash string
	data []byte
	// path, size and index locate the block on disk for the nil-data case.
	path  string
	size  int64
	index int
}

// Sync writes a home into the store as a snapshot and returns the manifest
// hash. Everything goes in; the question "what is valuable?" is never asked
// (spec/16).
func Sync(ctx context.Context, blobs BlobStore, orgID uuid.UUID, root string, excludes Excludes) (SyncResult, error) {
	return SyncWatched(ctx, blobs, orgID, root, excludes, nil)
}

// Watch is a sign of life while a long operation runs: how far it has come,
// measured in what the operation actually does.
//
// It exists because "started" and "finished" are not enough for something that
// takes minutes. A sync of a grown home reported nothing at all until it was
// done — and when a runner restarted in the middle of one, the only trace was a
// line in its debug log and an agent that said "securing" for half an hour.
type Watch func(seen int, bytesUp int64)

// SyncWatched is Sync with that sign of life. watch may be nil; it is called as
// the scan walks, not on a timer — the caller decides how often that is worth
// passing on, because only the caller knows what it costs to report.
func SyncWatched(ctx context.Context, blobs BlobStore, orgID uuid.UUID, root string, excludes Excludes, watch Watch) (SyncResult, error) {
	var res SyncResult
	seen := map[string]bool{}
	// The cache beside the copy: a file it vouches for is not read (#174).
	// Loaded here and saved at the end, so a sync that fails midway leaves the
	// previous cache — which is at worst stale, never wrong about a file it
	// names, because the stat has to match too.
	cache := LoadStatCache(root)
	// Where each cached hash would be read from, should the store lack it.
	var cachedPath string
	var cachedSize int64
	cachedIndex := 0

	// Gefragt wird gebündelt, hochgeladen einzeln. Der Grund ist der Weg: bei
	// einem Store hinter dem Netz war "kennst du diesen Block?" bisher eine
	// Anfrage pro Block, nacheinander — bei einem gewachsenen Home sechsstellig
	// oft, bevor auch nur ein neues Byte hochging. Ein 16,9-GB-Home mit 150.000
	// Dateien kam damit nicht mehr durch.
	//
	// Der Puffer hält die Blöcke, bis genug beisammen ist, um EINE Frage zu
	// stellen. Begrenzt wird er nach Bytes und nicht nach Anzahl, weil beides
	// vorkommt: hunderttausend winzige Dateien und ein paar große. Was schon im
	// Store liegt, wird verworfen, ohne je die Leitung gesehen zu haben.
	buf := make([]pendingBlock, 0, askBatch)
	bufBytes := 0
	// The scan runs ahead of the upload: a full batch is handed to the
	// uploader and the scan carries on reading while it goes up, at most one
	// batch waiting. Within a batch the blocks the store lacks are put
	// concurrently (#175). res is the uploader's to write until it is joined.
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	batches := make(chan []pendingBlock, 1)
	uploaded := make(chan error, 1)
	var blocksUp, bytesUp atomic.Int64
	go func() {
		err := uploadBatches(ctx, blobs, orgID, batches, &blocksUp, &bytesUp)
		if err != nil {
			// Ends the scan too: a flush waiting to hand over a batch would
			// otherwise wait for an uploader that has gone.
			stop()
		}
		uploaded <- err
	}()
	flush := func() error {
		if len(buf) == 0 {
			return nil
		}
		batch := buf
		buf = make([]pendingBlock, 0, askBatch)
		bufBytes = 0
		select {
		case batches <- batch:
			return nil
		case <-ctx.Done():
			// The uploader gave up; its error is read at the join.
			return ctx.Err()
		}
	}

	manifest, err := scanCached(root, excludes, cache, func(hash string, data []byte) error {
		// Je gelesenem Block, nicht je hochgeladenem: bei einem Home, das sich
		// kaum geändert hat, ist das Durchsehen die Arbeit — jeder Block wird
		// gelesen und gehasht, und in den Store geht am Ende nichts. Ein
		// Lebenszeichen, das am Hochladen hinge, schwiege dann durchgehend.
		if watch != nil {
			watch(len(seen), bytesUp.Load())
		}
		if data == nil {
			// A hash the cache vouched for. Its place in the file is counted
			// whether or not it is asked about again — a block seen before
			// still occupies its index.
			index := cachedIndex
			cachedIndex++
			if seen[hash] {
				return nil
			}
			seen[hash] = true
			// Asked about like any other; read only if the store says no.
			buf = append(buf, pendingBlock{hash: hash, path: cachedPath, size: cachedSize, index: index})
			if len(buf) >= askBatch {
				return flush()
			}
			return nil
		}
		if seen[hash] {
			return nil
		}
		seen[hash] = true
		// Only what is missing travels. This is where the 4 GB of toolchain
		// caches that are byte-for-byte identical on every developer home stop
		// costing anything after the first agent.
		//
		// Die Kopie ist nötig: Scan gibt seinen Lesepuffer weiter und
		// überschreibt ihn beim nächsten Block.
		cp := make([]byte, len(data))
		copy(cp, data)
		buf = append(buf, pendingBlock{hash: hash, data: cp})
		bufBytes += len(cp)
		if len(buf) >= askBatch || bufBytes >= askBatchBytes {
			return flush()
		}
		return nil
	}, func(path string, size int64) {
		cachedPath, cachedSize, cachedIndex = path, size, 0
	})
	flushErr := flush()
	close(batches)
	if upErr := <-uploaded; upErr != nil {
		return SyncResult{}, upErr
	}
	res.Blocks = int(blocksUp.Load())
	res.BytesUp = bytesUp.Load()
	if err != nil {
		return SyncResult{}, err
	}
	if flushErr != nil {
		return SyncResult{}, flushErr
	}
	if err := cache.Save(root); err != nil {
		// Not fatal: the snapshot is what matters, the cache is a saving.
		// Without it the next sync reads everything, as before.
		_ = err
	}

	raw, err := manifest.Encode()
	if err != nil {
		return SyncResult{}, err
	}
	// The manifest itself obeys the block size, and it took a production
	// instance to notice that it did not: every file in a home is chunked at
	// chunkSize, but the manifest went as ONE object of whatever size it had.
	// A home of 16.9 GB carries hundreds of thousands of entries, each with a
	// path and a 64-character hash — tens of megabytes in one PUT. Over a
	// remote runner that is an HTTP request, and it died against the request
	// limit of whatever sits in front of the control plane. The home was
	// therefore unsyncable, permanently, while every small home worked and made
	// the installation look healthy.
	hash, stored, err := putManifest(ctx, blobs, orgID, raw)
	if err != nil {
		return SyncResult{}, err
	}
	// The manifest is a block like any other and counts as one: the figure the
	// interface shows is "what travelled", and a sync that stores a snapshot
	// has moved at least this.
	res.Blocks += stored
	if stored > 0 {
		res.BytesUp += int64(len(raw))
	}
	res.ManifestHash = hash
	res.TotalSize = manifest.TotalSize()
	return res, nil
}

// uploadBatches takes batches off the channel until it is closed: asks the
// store which of the batch it holds, and puts the rest, transferWorkers at a
// time. The first error ends it, and the context with it, so the scan stops
// feeding.
func uploadBatches(ctx context.Context, blobs BlobStore, orgID uuid.UUID, batches <-chan []pendingBlock, blocksUp, bytesUp *atomic.Int64) error {
	for batch := range batches {
		hashes := make([]string, len(batch))
		for i, b := range batch {
			hashes[i] = b.hash
		}
		have, err := AskAll(ctx, blobs, orgID, hashes)
		if err != nil {
			return err
		}
		var (
			wg      sync.WaitGroup
			slots   = make(chan struct{}, transferWorkers)
			firstMu sync.Mutex
			first   error
		)
		for _, b := range batch {
			if have[b.hash] {
				continue
			}
			firstMu.Lock()
			failed := first != nil
			firstMu.Unlock()
			if failed {
				break
			}
			slots <- struct{}{}
			wg.Add(1)
			go func(b pendingBlock) {
				defer wg.Done()
				defer func() { <-slots }()
				data := b.data
				if data == nil {
					// The cache vouched for a block the store does not hold —
					// it was swept, or never arrived. Read now, from where the
					// cache said it lies.
					read, err := readBlock(b.path, b.size, b.index, b.hash)
					if err != nil {
						firstMu.Lock()
						if first == nil {
							first = err
						}
						firstMu.Unlock()
						return
					}
					data = read
				}
				if err := blobs.Put(ctx, orgID, b.hash, bytes.NewReader(data)); err != nil {
					firstMu.Lock()
					if first == nil {
						first = err
					}
					firstMu.Unlock()
					return
				}
				blocksUp.Add(1)
				bytesUp.Add(int64(len(data)))
			}(b)
		}
		wg.Wait()
		if first != nil {
			return first
		}
	}
	return nil
}

// manifestIndexMarker is what tells a stored object apart from a manifest: an
// index names the chunks the manifest was split into. It is a field name and
// not a length rule, because a reader must be able to decide from the CONTENT —
// a snapshot written by an older version is a plain manifest of any size, and
// it has to keep loading.
type manifestIndex struct {
	Chunks []string `json:"covey_manifest_chunks"`
}

// putManifest stores the manifest and returns the hash a snapshot refers to.
// Small enough, and it lies there as before — a reader of any version
// understands it. Above chunkSize it travels in pieces with a small index in
// front, which is the only object the snapshot hash then points at.
//
// Returns how many objects were actually written (0 = everything was already
// there, which is the normal case for an unchanged home).
func putManifest(ctx context.Context, blobs BlobStore, orgID uuid.UUID, raw []byte) (string, int, error) {
	if len(raw) <= chunkSize {
		hash := Hash(raw)
		has, err := blobs.Has(ctx, orgID, hash)
		if err != nil {
			return "", 0, err
		}
		if has {
			return hash, 0, nil
		}
		if err := blobs.Put(ctx, orgID, hash, bytes.NewReader(raw)); err != nil {
			return "", 0, err
		}
		return hash, 1, nil
	}

	var idx manifestIndex
	written := 0
	for off := 0; off < len(raw); off += chunkSize {
		end := off + chunkSize
		if end > len(raw) {
			end = len(raw)
		}
		part := raw[off:end]
		h := Hash(part)
		idx.Chunks = append(idx.Chunks, h)
		has, err := blobs.Has(ctx, orgID, h)
		if err != nil {
			return "", 0, err
		}
		if has {
			continue
		}
		if err := blobs.Put(ctx, orgID, h, bytes.NewReader(part)); err != nil {
			return "", 0, err
		}
		written++
	}
	enc, err := json.Marshal(idx)
	if err != nil {
		return "", 0, err
	}
	hash := Hash(enc)
	has, err := blobs.Has(ctx, orgID, hash)
	if err != nil {
		return "", 0, err
	}
	if !has {
		if err := blobs.Put(ctx, orgID, hash, bytes.NewReader(enc)); err != nil {
			return "", 0, err
		}
		written++
	}
	return hash, written, nil
}

// Load reads a snapshot's manifest back — whether it lies there whole or as an
// index over its chunks.
func Load(ctx context.Context, blobs BlobStore, orgID uuid.UUID, manifestHash string) (Manifest, error) {
	m, _, err := LoadWithObjects(ctx, blobs, orgID, manifestHash)
	return m, err
}

// LoadWithObjects reads the manifest and additionally names the objects the
// manifest ITSELF occupies: the hash a snapshot points at, plus the chunks it
// was split into when it did not fit into one.
//
// It exists for the garbage collection. A manifest's chunks are blocks like any
// other and are therefore swept like any other — but they appear in no entry,
// so a keep-set built from BlockSet() plus the snapshot hash leaves them out.
// The first sweep after a large home was synced then deleted exactly the pieces
// the index points at, the index survived pointing at nothing, and the agent
// could never be woken again ("manifest chunk …: block not found"). Below the
// chunk limit nothing of this was visible, which is why it only ever hit the
// biggest home on an instance.
func LoadWithObjects(ctx context.Context, blobs BlobStore, orgID uuid.UUID, manifestHash string) (Manifest, []string, error) {
	objects := []string{manifestHash}
	raw, err := fetch(ctx, blobs, orgID, manifestHash)
	if err != nil {
		return Manifest{}, nil, err
	}
	var idx manifestIndex
	if err := json.Unmarshal(raw, &idx); err == nil && len(idx.Chunks) > 0 {
		var buf bytes.Buffer
		for _, h := range idx.Chunks {
			objects = append(objects, h)
			part, err := fetch(ctx, blobs, orgID, h)
			if err != nil {
				return Manifest{}, nil, fmt.Errorf("manifest chunk %s: %w", h, err)
			}
			buf.Write(part)
		}
		raw = buf.Bytes()
	}
	m, err := DecodeManifest(raw)
	if err != nil {
		return Manifest{}, nil, err
	}
	return m, objects, nil
}

func fetch(ctx context.Context, blobs BlobStore, orgID uuid.UUID, hash string) ([]byte, error) {
	r, err := blobs.Get(ctx, orgID, hash)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

// MaterializeResult is what restoring cost.
type MaterializeResult struct {
	Written int   // files actually written
	Kept    int   // files already correct on disk
	Removed int   // paths the snapshot does not know
	BytesIn int64 // what had to be fetched
	// Missing are the paths whose blocks the store no longer holds — the
	// snapshot names them, nothing can produce them.
	//
	// They are a result and not an error on purpose. Refusing a whole start
	// over one unreadable file is the harsher of two bad outcomes: the agent
	// could work, it just has to be told what is not there (#138). Whoever
	// receives this has to SAY so — a silent gap in a home is the one thing
	// worse than a refused start.
	Missing []string
}

// Owner is who the materialised files belong to.
//
// It exists because a home restored on a runner used to belong to root: the
// runner runs as root (Docker wants that), and every file it wrote carried its
// ownership. The agent inside the sandbox is uid 1001 and cannot touch any of
// it — 13 of 16 GB of one home, including the caches the platform then asked it
// to tidy up (#120). It could not repair its own workspace either: a
// half-unpacked archive, a broken checkout, a wrong SDK — all restored as root,
// all read-only in practice.
//
// A chown per created entry costs nothing next to writing the content, and it
// is the difference between a home an agent lives in and one it only looks at.
type Owner struct{ UID, GID int }

// gehoert sets the owner of a path, if one was asked for.
//
// Errors are swallowed on purpose. Only root may hand a file to another user,
// and the same code runs on a developer's machine where the runner is not root
// and the files are already theirs. Refusing to materialise a home there
// because a chown was not permitted would trade a real start for a cosmetic
// one.
func (o *Owner) gehoert(pfad string) {
	if o == nil {
		return
	}
	_ = os.Lchown(pfad, o.UID, o.GID)
}

// Materialize brings a working copy to the state of a snapshot. It writes only
// what differs and leaves the rest alone: on the runner an agent last ran on
// that is the normal case, and then materialising costs nothing at all.
//
// It also removes what the snapshot does not know. That is not tidiness but
// correctness — a working copy that keeps files from a previous state is not
// the home the snapshot describes, and the difference would show up as a
// mystery in the agent's next run.
func Materialize(ctx context.Context, blobs BlobStore, orgID uuid.UUID, root string, m Manifest) (MaterializeResult, error) {
	return MaterializeInto(ctx, blobs, orgID, root, m, true)
}

// MaterializeInto is Materialize with the one decision made explicit: may it
// REMOVE what the snapshot does not describe?
//
// Removing is right when the working copy is supposed to be that snapshot and
// nothing else — an explicit restore, or a copy that was last synced to exactly
// this state. It is ruinous when the snapshot is older than the copy: on a
// production instance a home whose sync had been failing for hours was
// materialised from a stale snapshot at every wake, and every wake deleted the
// session transcripts of the runs since. The continuations that wanted to
// resume those sessions then failed with "no conversation found" — the platform
// had erased the memory of its own unfinished work and blamed the runtime.
//
// So the caller decides, and the caller that cannot be sure says no: keeping a
// file too many costs disk, deleting one costs work nobody can get back.
func MaterializeInto(ctx context.Context, blobs BlobStore, orgID uuid.UUID, root string, m Manifest, prune bool) (MaterializeResult, error) {
	return MaterializeWatched(ctx, blobs, orgID, root, m, prune, nil)
}

// MaterializeWatched is MaterializeInto with a sign of life: watch is called as
// the entries are walked, with how many have been dealt with and how many bytes
// came out of the store. May be nil.
//
// The figures are the ones that make the wait explicable. Eleven minutes of
// silence and eleven minutes with "3.1 GB of 8.3 GB" are the same wait, and
// only one of them is a fault report.
func MaterializeWatched(ctx context.Context, blobs BlobStore, orgID uuid.UUID, root string, m Manifest, prune bool, watch Watch) (MaterializeResult, error) {
	return MaterializeOwned(ctx, blobs, orgID, root, m, prune, watch, nil)
}

// MaterializeOwned is MaterializeWatched with the answer to "and whose is it?".
//
// owner nil = whatever the writing process is, which is right for a restore
// into a directory somebody is looking at. On a runner it is the agent, and
// that is the whole point (#120).
func MaterializeOwned(ctx context.Context, blobs BlobStore, orgID uuid.UUID, root string, m Manifest, prune bool, watch Watch, owner *Owner) (MaterializeResult, error) {
	var res MaterializeResult
	if err := os.MkdirAll(root, 0o755); err != nil {
		return res, err
	}
	owner.gehoert(root)
	// The same cache the sync keeps (#174): a file whose stat the cache knows
	// and whose cached hashes are the manifest's is correct without being
	// read. Saved at the end, with what was written noted.
	cache := LoadStatCache(root)

	wanted := map[string]bool{}
	// Directories first, and shallow before deep — a file cannot be written
	// into a directory that does not exist yet.
	entries := append([]Entry(nil), m.Entries...)
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Dir != entries[j].Dir {
			return entries[i].Dir
		}
		return entries[i].Path < entries[j].Path
	})

	// Directories and links first, in order and on this goroutine; the files
	// afterwards, several at a time. On a fresh host every file is a fetch
	// from the store — over a remote runner one HTTP round trip each — and a
	// home has six figures of them: done one after the other that was minutes
	// of latency with the line idle (#175).
	var files []Entry
	for _, e := range entries {
		target, err := safeJoin(root, e.Path)
		if err != nil {
			return res, err
		}
		wanted[e.Path] = true
		switch {
		case e.Dir:
			if err := mkdirAllClearing(root, target, e.Mode|0o700); err != nil {
				return res, err
			}
			owner.gehoert(target)
		case e.Link != "":
			if old, err := os.Readlink(target); err == nil && old == e.Link {
				res.Kept++
				continue
			}
			_ = os.Remove(target)
			if err := mkdirAllClearing(root, filepath.Dir(target), 0o755); err != nil {
				return res, err
			}
			if err := os.Symlink(e.Link, target); err != nil {
				return res, err
			}
			owner.gehoert(target)
			res.Written++
		default:
			files = append(files, e)
		}
	}

	var (
		mu      sync.Mutex // guards res, cache and the watch below
		wg      sync.WaitGroup
		slots   = make(chan struct{}, transferWorkers)
		firstMu sync.Mutex
		first   error
		done    int
	)
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	fail := func(err error) {
		firstMu.Lock()
		if first == nil {
			first = err
			stop()
		}
		firstMu.Unlock()
	}
	for _, e := range files {
		if ctx.Err() != nil {
			break
		}
		slots <- struct{}{}
		wg.Add(1)
		go func(e Entry) {
			defer wg.Done()
			defer func() { <-slots }()
			target, err := safeJoin(root, e.Path)
			if err != nil {
				fail(err)
				return
			}
			mu.Lock()
			hit := cachedMatch(cache, target, e)
			mu.Unlock()
			if hit || unchanged(target, e) {
				mu.Lock()
				res.Kept++
				if !hit {
					// Read once, and remembered: the next materialise or
					// sync of this file costs a stat.
					if info, err := os.Lstat(target); err == nil {
						cache.Note(e.Path, info, e.Blocks)
					}
				}
				done++
				if watch != nil {
					watch(done, res.BytesIn)
				}
				mu.Unlock()
				return
			}
			n, err := writeFile(ctx, blobs, orgID, root, target, e)
			if err != nil {
				// A block the store does not have any more cannot be produced
				// by trying again — for this file the answer is final, for the
				// rest of the home it is not (#138). Anything else (a full
				// disk, a broken connection) still ends the whole run: that is
				// a condition of the machine, not of one file.
				if errors.Is(err, ErrNotFound) {
					// Half a file is worse than none: the next unchanged()
					// would find the right size with the wrong content.
					_ = os.Remove(target)
					mu.Lock()
					res.Missing = append(res.Missing, e.Path)
					done++
					mu.Unlock()
					return
				}
				fail(err)
				return
			}
			owner.gehoert(target)
			mu.Lock()
			res.Written++
			res.BytesIn += n
			if info, err := os.Lstat(target); err == nil {
				cache.Note(e.Path, info, e.Blocks)
			}
			done++
			if watch != nil {
				watch(done, res.BytesIn)
			}
			mu.Unlock()
		}(e)
	}
	wg.Wait()
	if first != nil {
		return res, first
	}
	// A stable order for the report, whatever order the fetches finished in.
	sort.Strings(res.Missing)

	if prune {
		removed, err := removeUnknown(root, wanted)
		if err != nil {
			return res, err
		}
		res.Removed = removed
		cache.Forget(wanted)
	}
	_ = cache.Save(root) // a saving, not the result — see SyncWatched
	return res, nil
}

// cachedMatch: does the cache vouch for this file being exactly the entry?
// Same stat as at the last scan, and the same blocks then as now.
func cachedMatch(cache *StatCache, path string, e Entry) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != e.Size {
		return false
	}
	blocks, ok := cache.Lookup(e.Path, info)
	if !ok || len(blocks) != len(e.Blocks) {
		return false
	}
	for i := range blocks {
		if blocks[i] != e.Blocks[i] {
			return false
		}
	}
	return true
}

// unchanged: is the file on disk already the one the snapshot describes?
//
// It reads the file to answer that, LARGE FILES INCLUDED, and the earlier
// version deliberately did not — "hashing a 3 GB SDK tarball to save writing it
// is a bad trade". The trade it actually made was worse: everything above
// wholeFileLimit counted as changed, always, and was therefore fetched and
// written again on every single wake. Measured on a production instance: an
// agent whose home holds an SDK, a JDK and package caches pulled 8.3 GB across
// the network at every wake — eleven minutes, before its first turn, every
// time. Reading those same bytes off the local disk costs seconds.
//
// The comparison mirrors how the blocks were made (blocksOf): one block means
// the whole file was hashed at once, several mean fixed chunkSize pieces.
func unchanged(path string, e Entry) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != e.Size {
		return false
	}
	if len(e.Blocks) == 0 {
		return e.Size == 0
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	if len(e.Blocks) == 1 {
		data, err := io.ReadAll(f)
		if err != nil {
			return false
		}
		return Hash(data) == e.Blocks[0]
	}
	buf := make([]byte, chunkSize)
	for _, want := range e.Blocks {
		n, err := io.ReadFull(f, buf)
		if n == 0 || (err != nil && err != io.ErrUnexpectedEOF) {
			return false
		}
		if Hash(buf[:n]) != want {
			return false
		}
	}
	// Nothing may follow: a longer file with the same prefix is not this one.
	// The size check above already says so — this is the belt to its braces.
	if n, _ := f.Read(buf[:1]); n != 0 {
		return false
	}
	return true
}

// updateInPlace repairs a chunked file that is already there, writing ONLY the
// chunks that differ. Without it a changed file is rewritten whole: a
// transcript that grew by a line costs its full size in local writes, and on a
// home of gigabytes that is the second half of the same waste the reuse of
// local chunks removes from the wire.
//
// Not writing through a temporary file is the deliberate part. The temp+rename
// dance protects against a half-written file that looks finished — and the
// protection is now elsewhere and stronger: unchanged() compares by content,
// so a file left mixed by a crash is detected at the next materialisation and
// repaired from the store. The working copy is replaceable; that is the whole
// premise of the home store.
//
// Reports what had to be FETCHED, like writeFile — the bytes taken from the
// file itself never travelled.
func updateInPlace(ctx context.Context, blobs BlobStore, orgID uuid.UUID, f *os.File, e Entry) (int64, error) {
	buf := make([]byte, chunkSize)
	var n int64
	for i, hash := range e.Blocks {
		off := int64(i) * int64(chunkSize)
		read, err := f.ReadAt(buf, off)
		if read > 0 && (err == nil || err == io.EOF) && Hash(buf[:read]) == hash {
			continue
		}
		r, err := blobs.Get(ctx, orgID, hash)
		if err != nil {
			return n, fmt.Errorf("block %s of %s: %w", hash[:min(8, len(hash))], e.Path, err)
		}
		data, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			return n, err
		}
		if _, err := f.WriteAt(data, off); err != nil {
			return n, err
		}
		n += int64(len(data))
	}
	// A file that used to be longer keeps its tail without this.
	if err := f.Truncate(e.Size); err != nil {
		return n, err
	}
	return n, f.Sync()
}

// mkdirAllClearing is os.MkdirAll with one repair: where a path component is on
// disk as something a directory cannot be made of — a leftover file, or a
// symlink whose target is gone — mkdir answers "file exists", and since a wake
// materialises the same manifest onto the same disk every time, it answers it
// again on the next one. One stale node_modules symlink in a 23 GB home kept an
// agent from waking for eighteen hours (#197): the backlog filled up, and the
// prune that would have cleared the leftover sits at the end of the
// materialisation, past the loop that returns the error.
//
// The link branch of the materialisation has always removed what stood in its
// way. This gives the directory branch the same right, and no more than that:
// only a non-directory goes, only below root, and only after MkdirAll has
// actually failed. A symlink pointing at a real directory is left alone —
// MkdirAll walks through it, so it was never the obstacle.
func mkdirAllClearing(root, path string, mode os.FileMode) error {
	err := os.MkdirAll(path, mode)
	if err == nil {
		return nil
	}
	if !clearBlockers(root, path) {
		return err
	}
	return os.MkdirAll(path, mode)
}

// clearBlockers removes the components of path, from root downwards, that exist
// but are not directories. Reports whether it removed anything — if it did not,
// the caller's original error is the honest one to return.
func clearBlockers(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	cleared := false
	prefix := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		prefix = filepath.Join(prefix, part)
		if _, err := os.Lstat(prefix); err != nil {
			continue // not here yet; MkdirAll will make it
		}
		if info, err := os.Stat(prefix); err == nil && info.IsDir() {
			continue // a directory, or a link to one
		}
		if os.Remove(prefix) == nil {
			cleared = true
		}
	}
	return cleared
}

func writeFile(ctx context.Context, blobs BlobStore, orgID uuid.UUID, root, target string, e Entry) (int64, error) {
	if err := mkdirAllClearing(root, filepath.Dir(target), 0o755); err != nil {
		return 0, err
	}
	// A chunked file that is already here is repaired where it lies — only the
	// chunks that differ are written, and nothing is copied that has not
	// changed. Everything else takes the route below.
	if len(e.Blocks) > 1 {
		if f, err := os.OpenFile(target, os.O_RDWR, e.Mode); err == nil {
			info, statErr := f.Stat()
			if statErr == nil && info.Mode().IsRegular() {
				n, err := updateInPlace(ctx, blobs, orgID, f, e)
				closeErr := f.Close()
				if err == nil && closeErr == nil {
					return n, os.Chmod(target, e.Mode)
				}
				// Anything unexpected falls through to the whole rewrite —
				// which is correct in every case, only more expensive.
			} else {
				f.Close()
			}
		}
	}
	// Into a temporary file and rename, so a failed restore does not leave a
	// half file behind that looks like the real one.
	tmp, err := os.CreateTemp(filepath.Dir(target), ".covey-tmp-*")
	if err != nil {
		return 0, err
	}
	defer os.Remove(tmp.Name())

	// No reuse of local bytes here on purpose: this is the route for a file
	// that is NOT there yet (or could not be repaired), and what is not there
	// has nothing to reuse. The saving lives in one place — updateInPlace above
	// — and a second copy of it here would be a mechanism nothing exercises:
	// removing it made no test go red, which is how it was found.
	var n int64
	for _, hash := range e.Blocks {
		r, err := blobs.Get(ctx, orgID, hash)
		if err != nil {
			tmp.Close()
			return n, fmt.Errorf("block %s of %s: %w", hash[:min(8, len(hash))], e.Path, err)
		}
		written, err := io.Copy(tmp, r)
		r.Close()
		if err != nil {
			tmp.Close()
			return n, err
		}
		n += written
	}
	if err := tmp.Close(); err != nil {
		return n, err
	}
	if err := os.Chmod(tmp.Name(), e.Mode); err != nil {
		return n, err
	}
	return n, os.Rename(tmp.Name(), target)
}

// removeUnknown clears away what the snapshot does not describe.
func removeUnknown(root string, wanted map[string]bool) (int, error) {
	var toRemove []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || path == root {
			return nil //nolint:nilerr // a path that vanished needs no removing
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if wanted[rel] {
			return nil
		}
		toRemove = append(toRemove, path)
		if d.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	for _, path := range toRemove {
		if err := os.RemoveAll(path); err != nil {
			return 0, err
		}
	}
	return len(toRemove), nil
}

// safeJoin keeps a manifest path inside the home. The manifest comes back over
// the protocol from a runner, and a path of "../.." would otherwise write into
// the control plane's own directories.
func safeJoin(root, rel string) (string, error) {
	slashed := strings.ReplaceAll(rel, "\\", "/")
	// Refused, not quietly clamped: a manifest with ".." in it is a protocol
	// violation, and bending it into a path inside the home would restore an
	// agent's work under names nobody wrote.
	for _, part := range strings.Split(slashed, "/") {
		if part == ".." {
			return "", fmt.Errorf("path %q leaves the home", rel)
		}
	}
	target := filepath.Join(root, filepath.FromSlash(filepath.Clean("/"+slashed)))
	if target != root && !strings.HasPrefix(target, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("path %q leaves the home", rel)
	}
	return target, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Woher eine Arbeitskopie weiß, ob sie noch der Schnappschuss ist, für den sie
// sich hält: neben ihr liegt eine Marke mit dem Manifest-Hash des letzten
// erfolgreichen Syncs. NEBEN ihr, nicht darin — im Home wäre sie Teil jedes
// Schnappschusses und änderte ihn bei jedem Sync.
//
// Entscheidend ist, WANN sie verschwindet: sobald eine Sandbox startet. Ab dann
// kann in der Kopie alles Mögliche entstehen, und niemand weiß mehr, ob sie
// noch dem Schnappschuss entspricht. Erst ein gelungener Sync setzt sie wieder.
//
// Genau daran hing der Fall, der eine Produktivinstanz Tagesarbeit gekostet
// hat: dort stand die Kopie formal auf demselben Schnappschuss wie der Store —
// der letzte erfolgreiche Sync lag Stunden zurück —, und trotzdem trug sie die
// Sitzungstranskripte dreier Läufe, deren Syncs nicht durchkamen. Die Frage
// „steht sie auf diesem Stand?" hätte mit Ja geantwortet und die Transkripte
// gelöscht. Die richtige Frage ist „hat seither jemand darin gearbeitet?".
func stateFile(root string) string { return strings.TrimRight(root, "/\\") + ".snapshot" }

// ownerFile marks that this home has been handed to its agent once.
func ownerFile(root string) string { return strings.TrimRight(root, "/\\") + ".owned" }

// Adopt hands an existing home to its agent — once, and then never again.
//
// Homes materialised before #120 belong to root all the way down: the runner
// wrote them, and the runner is root. The agent inside cannot delete a cache it
// is asked to clean up, cannot repair a broken checkout, cannot do anything but
// read. Walking the tree costs seconds for half a million files and is paid
// once per home, which is why the marker matters more than the speed.
//
// Errors are counted, not returned. On a machine where the runner is not root
// every chown fails and the home is already the right person's — refusing to
// start there would be a fix that breaks the case it does not apply to.
func Adopt(root string, owner Owner) (int, bool) {
	if _, err := os.Stat(ownerFile(root)); err == nil {
		return 0, false
	}
	var geaendert int
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if st, ok := info.Sys().(*syscall.Stat_t); ok &&
			int(st.Uid) == owner.UID && int(st.Gid) == owner.GID {
			return nil
		}
		if os.Lchown(p, owner.UID, owner.GID) == nil {
			geaendert++
		}
		return nil
	})
	// The marker only goes down when there was nothing left to hand over.
	// Otherwise a run that could not chown (not root) would mark the home as
	// done and the real repair would never happen.
	if geaendert == 0 {
		_ = os.WriteFile(ownerFile(root), []byte("1"), 0o600)
	}
	return geaendert, geaendert > 0
}

// MarkSynced records that this working copy IS exactly this snapshot, and
// when that became true. Only after a successful sync (or an explicit restore).
//
// The moment travels with the hash because the hash alone cannot say whether
// the copy is newer or older than the state the control plane holds. Two
// states meet on a wake — the copy on this host and the snapshot in the
// database — and when they differ, which of the two is the later one decides
// whether materialising is a restore or a reversal (#153). The times are the
// host's own clock, compared against the control plane's; a skew of seconds is
// irrelevant against the minutes a run takes, and a skew of hours is a broken
// host that has worse problems than this.
func MarkSynced(root, manifestHash string) {
	if root == "" || manifestHash == "" {
		return
	}
	_ = os.WriteFile(stateFile(root), []byte(manifestHash+" "+time.Now().UTC().Format(time.RFC3339Nano)), 0o600)
}

// inUseMark is what the state file holds while a sandbox is (or was) working
// in the copy: not a hash, but the moment the work began. Until #153 the file
// was simply removed here, and with it the one fact the next wake needs — that
// the copy carries a run the snapshot in the database does not know, and since
// when.
const inUseMark = "in-use"

// MarkInUse takes the synced mark back: a sandbox starts, and from now on the
// copy counts as changed until a sync proves otherwise. The moment is kept.
func MarkInUse(root string) {
	if root == "" {
		return
	}
	_ = os.WriteFile(stateFile(root), []byte(inUseMark+" "+time.Now().UTC().Format(time.RFC3339Nano)), 0o600)
}

// SyncedHash reads the mark. Empty = the copy has been used since its last
// sync, or there never was one — both mean: delete nothing.
func SyncedHash(root string) string {
	hash, _, inUse := readState(root)
	if inUse {
		return ""
	}
	return hash
}

// SyncedAt is when the copy was last brought to the state SyncedHash names.
// Zero when it is in use, never synced, or the mark predates the timestamp.
func SyncedAt(root string) time.Time {
	_, at, inUse := readState(root)
	if inUse {
		return time.Time{}
	}
	return at
}

// InUseSince is when a sandbox last started working in this copy without a
// sync having closed the run since. Zero when the copy is synced, or has never
// been used, or the mark predates #153 (then MarkInUse removed the file, and
// nothing can be said about when).
func InUseSince(root string) time.Time {
	_, at, inUse := readState(root)
	if !inUse {
		return time.Time{}
	}
	return at
}

// readState parses the mark: "<hash>", "<hash> <time>" or "in-use <time>".
// The bare hash is what versions before #153 wrote; it still reads as synced,
// only without a time.
func readState(root string) (hash string, at time.Time, inUse bool) {
	// The mark lies BESIDE the copy, so it survives `rm -rf` of the copy
	// itself — and then claims a state of a directory that is not there. A
	// copy that does not exist has no state, whatever the mark says (#173).
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return "", time.Time{}, false
	}
	b, err := os.ReadFile(stateFile(root))
	if err != nil {
		return "", time.Time{}, false
	}
	first, rest, _ := strings.Cut(strings.TrimSpace(string(b)), " ")
	if rest != "" {
		at, _ = time.Parse(time.RFC3339Nano, strings.TrimSpace(rest))
	}
	if first == inUseMark {
		return "", at, true
	}
	return first, at, false
}
