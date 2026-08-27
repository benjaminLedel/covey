package homestore

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeS3 is as much of an S3-compatible store as a block store touches: PUT,
// GET, HEAD, DELETE and ListObjectsV2, path style. It also insists on a
// signature being present — not to check the cryptography (the AWS vector does
// that) but so that an unsigned request cannot pass unnoticed.
type fakeS3 struct {
	// bucket is what the path-style prefix is stripped by — the fake has to
	// know its own name, the way a real store does.
	bucket  string
	mu      sync.Mutex
	objects map[string][]byte
	// pageSize forces paging, so the continuation token is exercised rather
	// than assumed.
	pageSize int
	// parallel/maxParallel zählen überlappende Anfragen — womit ein Test
	// belegen kann, dass nebenläufig gefragt wird und wie weit.
	parallel    int
	maxParallel int
	// headFails lässt jede HEAD-Anfrage scheitern.
	headFails bool
}

func newFakeS3(t *testing.T, pageSize int) (*fakeS3, *S3) {
	t.Helper()
	f := &fakeS3{bucket: "covey-blocks", objects: map[string][]byte{}, pageSize: pageSize}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)

	store, err := NewS3(srv.URL, "covey-blocks", "", Credentials{
		AccessKey: "key", SecretKey: "secret", Region: "eu-central-1",
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	return f, store
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodHead {
		f.mu.Lock()
		f.parallel++
		if f.parallel > f.maxParallel {
			f.maxParallel = f.parallel
		}
		scheitern := f.headFails
		f.mu.Unlock()
		// Kurz halten, damit sich die Anfragen überhaupt überschneiden können.
		time.Sleep(5 * time.Millisecond)
		defer func() {
			f.mu.Lock()
			f.parallel--
			f.mu.Unlock()
		}()
		if scheitern {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}
	if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<Error><Code>AccessDenied</Code><Message>unsigned</Message></Error>`))
		return
	}
	key := strings.TrimPrefix(r.URL.Path, "/"+f.bucket)
	key = strings.TrimPrefix(key, "/")

	f.mu.Lock()
	defer f.mu.Unlock()

	switch {
	case r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2":
		f.list(w, r)
	case r.Method == http.MethodPut:
		body, _ := io.ReadAll(r.Body)
		// What a real store checks too: the announced content hash has to be
		// the content's. A block stored under a foreign hash would be a lie
		// that survives every later check.
		if got := Hash(body); got != r.Header.Get("X-Amz-Content-Sha256") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`<Error><Code>XAmzContentSHA256Mismatch</Code></Error>`))
			return
		}
		f.objects[key] = body
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodHead:
		body, ok := f.objects[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodGet:
		body, ok := f.objects[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`<Error><Code>NoSuchKey</Code></Error>`))
			return
		}
		_, _ = w.Write(body)
	case r.Method == http.MethodDelete:
		delete(f.objects, key)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (f *fakeS3) list(w http.ResponseWriter, r *http.Request) {
	prefix := r.URL.Query().Get("prefix")
	var keys []string
	for k := range f.objects {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	start := 0
	if token := r.URL.Query().Get("continuation-token"); token != "" {
		for i, k := range keys {
			if k == token {
				start = i
				break
			}
		}
	}
	end := len(keys)
	truncated := false
	if f.pageSize > 0 && start+f.pageSize < end {
		end = start + f.pageSize
		truncated = true
	}

	type item struct {
		Key  string `xml:"Key"`
		Size int64  `xml:"Size"`
	}
	out := struct {
		XMLName               xml.Name `xml:"ListBucketResult"`
		IsTruncated           bool     `xml:"IsTruncated"`
		NextContinuationToken string   `xml:"NextContinuationToken,omitempty"`
		Contents              []item   `xml:"Contents"`
	}{IsTruncated: truncated}
	for _, k := range keys[start:end] {
		out.Contents = append(out.Contents, item{Key: k, Size: int64(len(f.objects[k]))})
	}
	if truncated {
		out.NextContinuationToken = keys[end]
	}
	w.Header().Set("Content-Type", "application/xml")
	_ = xml.NewEncoder(w).Encode(out)
}

// blobStoreContract is what every backend has to do, run against both. The
// port exists so that the store can be swapped without the rest noticing — and
// that only holds as long as both implementations answer the same, including
// where they answer "no".
func blobStoreContract(t *testing.T, blobs BlobStore) {
	t.Helper()
	ctx := context.Background()
	org, other := uuid.New(), uuid.New()
	content := []byte("der Inhalt eines Blocks")
	hash := Hash(content)

	if has, err := blobs.Has(ctx, org, hash); err != nil || has {
		t.Fatalf("a block that was never stored must not be there: %v, %v", has, err)
	}
	if _, err := blobs.Get(ctx, org, hash); err == nil {
		t.Error("reading a missing block has to fail")
	}

	if err := blobs.Put(ctx, org, hash, strings.NewReader(string(content))); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if has, err := blobs.Has(ctx, org, hash); err != nil || !has {
		t.Fatalf("the stored block has to be there: %v, %v", has, err)
	}
	rc, err := blobs.Get(ctx, org, hash)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if string(got) != string(content) {
		t.Errorf("the block came back changed: %q", got)
	}

	// Writing the same hash again is allowed and changes nothing — blocks are
	// immutable, and a sync that raced with another must not fail on it.
	if err := blobs.Put(ctx, org, hash, strings.NewReader(string(content))); err != nil {
		t.Errorf("storing the same block twice has to be allowed: %v", err)
	}

	// The organisation is a boundary, not a filter: a shared namespace would be
	// an existence oracle over hashes.
	if has, err := blobs.Has(ctx, other, hash); err != nil || has {
		t.Error("a foreign organisation must not see the block")
	}
	if _, err := blobs.Get(ctx, other, hash); err == nil {
		t.Error("a foreign organisation must not read the block")
	}

	// A hash becomes a path; anything that is not one would write elsewhere.
	for _, bad := range []string{"../../etc/passwd", "..", "ZZZZ", ""} {
		if err := blobs.Put(ctx, org, bad, strings.NewReader("x")); err == nil {
			t.Errorf("hash %q had to be refused", bad)
		}
	}

	list, err := blobs.List(ctx, org)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0] != hash {
		t.Errorf("the listing is wrong: %v", list)
	}
	if list, err := blobs.List(ctx, other); err != nil || len(list) != 0 {
		t.Errorf("a foreign organisation's listing has to be empty: %v, %v", list, err)
	}

	if err := blobs.Delete(ctx, org, hash); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if has, _ := blobs.Has(ctx, org, hash); has {
		t.Error("the deleted block is still there")
	}
	// Deleting something that is not there has the outcome the caller wanted.
	if err := blobs.Delete(ctx, org, hash); err != nil {
		t.Errorf("deleting twice must not fail: %v", err)
	}
}

func TestDirSatisfiesTheContract(t *testing.T) { blobStoreContract(t, newDir(t)) }

func TestS3SatisfiesTheContract(t *testing.T) {
	_, store := newFakeS3(t, 0)
	blobStoreContract(t, store)
}

// A grown store holds hundreds of thousands of blocks and the answer comes a
// thousand at a time. Without paging the retention would sweep against a
// listing that stops after the first page — and remove everything it did not
// see.
func TestS3ListingIsPaged(t *testing.T) {
	ctx := context.Background()
	_, store := newFakeS3(t, 3)
	org := uuid.New()

	want := map[string]bool{}
	for i := 0; i < 10; i++ {
		content := fmt.Sprintf("block %d", i)
		hash := Hash([]byte(content))
		want[hash] = true
		if err := store.Put(ctx, org, hash, strings.NewReader(content)); err != nil {
			t.Fatal(err)
		}
	}

	list, err := store.List(ctx, org)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != len(want) {
		t.Fatalf("the listing stopped after %d of %d blocks", len(list), len(want))
	}
	for _, hash := range list {
		if !want[hash] {
			t.Errorf("unknown block in the listing: %s", hash)
		}
	}

	// The fill level falls out of the same listing, so it costs no second pass.
	size, err := store.Size(ctx, org)
	if err != nil {
		t.Fatal(err)
	}
	if size == 0 {
		t.Error("the fill level has to be measured across all pages")
	}
}

// A whole home through the S3 backend: sync, materialise, and the sweep over
// what survives. It is the same code as with the directory — which is what the
// port is for, and this is where that claim is checked instead of asserted.
func TestHomeRoundTripThroughS3(t *testing.T) {
	ctx := context.Background()
	_, store := newFakeS3(t, 4)
	org := uuid.New()
	home := t.TempDir()

	write(t, home, "arbeit/ergebnis.md", "was nur hier existiert")
	write(t, home, ".pub-cache/paket.tar", strings.Repeat("SDK", 5_000))

	res, err := Sync(ctx, store, org, home, nil)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if res.ManifestHash == "" || res.Blocks == 0 {
		t.Fatalf("the sync produced nothing: %+v", res)
	}

	m, err := Load(ctx, store, org, res.ManifestHash)
	if err != nil {
		t.Fatal(err)
	}
	restore := t.TempDir()
	if _, err := Materialize(ctx, store, org, restore, m); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if got, err := readFile(restore, "arbeit/ergebnis.md"); err != nil || got != "was nur hier existiert" {
		t.Errorf("the home did not come back: %q, %v", got, err)
	}

	// And the cleanup: with this snapshot alive, nothing may go.
	plan, err := Plan(ctx, store, org, []string{res.ManifestHash})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Blocks != 0 {
		t.Errorf("a cleanup must not touch a living snapshot: %+v", plan)
	}
	// Without it, everything goes — and the measured figure is the space
	// actually freed, which needs a size per block.
	plan, err = Plan(ctx, store, org, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Blocks == 0 || plan.Bytes == 0 {
		t.Errorf("the preview has to measure what would be freed: %+v", plan)
	}
}

// The error body carries the reason (SignatureDoesNotMatch, NoSuchBucket,
// AccessDenied). Without it a 403 is a wall, and the search starts in the wrong
// place.
func TestS3ErrorsCarryTheirReason(t *testing.T) {
	ctx := context.Background()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<Error><Code>SignatureDoesNotMatch</Code><Message>keys differ</Message></Error>`))
	}))
	defer srv.Close()

	store, err := NewS3(srv.URL, "b", "", Credentials{AccessKey: "a", SecretKey: "s"}, true)
	if err != nil {
		t.Fatal(err)
	}
	err = store.Put(ctx, uuid.New(), Hash([]byte("x")), strings.NewReader("x"))
	if err == nil {
		t.Fatal("a 403 has to come back as an error")
	}
	if !strings.Contains(err.Error(), "SignatureDoesNotMatch") || !strings.Contains(err.Error(), "keys differ") {
		t.Errorf("the reason is missing from the message: %v", err)
	}
}

// NewS3 refuses a configuration that cannot work, at the moment it is made
// rather than at the first agent's falling asleep.
func TestNewS3RefusesAnIncompleteConfiguration(t *testing.T) {
	for _, f := range []func() (*S3, error){
		func() (*S3, error) { return NewS3("", "b", "", Credentials{AccessKey: "a", SecretKey: "s"}, true) },
		func() (*S3, error) {
			return NewS3("https://x", "", "", Credentials{AccessKey: "a", SecretKey: "s"}, true)
		},
		func() (*S3, error) { return NewS3("https://x", "b", "", Credentials{SecretKey: "s"}, true) },
		func() (*S3, error) { return NewS3("https://x", "b", "", Credentials{AccessKey: "a"}, true) },
	} {
		if _, err := f(); err == nil {
			t.Error("an incomplete configuration had to be refused")
		}
	}
	// Without a region: servers that have no notion of one still want it in
	// the signature.
	store, err := NewS3("https://x", "b", "", Credentials{AccessKey: "a", SecretKey: "s"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if store.Creds.Region == "" {
		t.Error("a region has to be filled in")
	}
}

func readFile(root, rel string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(root, rel))
	return string(raw), err
}

// Check is asked at startup: a wrong key or a missing bucket should say so
// there and not at the first agent's falling asleep, where the message would
// arrive inside the recording of a task that has long since run.
func TestS3CheckProbesWritingAndReading(t *testing.T) {
	ctx := context.Background()
	f, store := newFakeS3(t, 0)
	if err := store.Check(ctx); err != nil {
		t.Fatalf("a working store has to pass the check: %v", err)
	}
	// And it leaves nothing behind — a probe object that accumulated on every
	// restart would be a small, permanent puzzle.
	f.mu.Lock()
	left := len(f.objects)
	f.mu.Unlock()
	if left != 0 {
		t.Errorf("the check left %d objects behind", left)
	}

	// A store that refuses says so, with the reason it gave.
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`<Error><Code>NoSuchBucket</Code><Message>bucket missing</Message></Error>`))
	}))
	defer broken.Close()
	bad, err := NewS3(broken.URL, "weg", "", Credentials{AccessKey: "a", SecretKey: "s"}, true)
	if err != nil {
		t.Fatal(err)
	}
	err = bad.Check(ctx)
	if err == nil || !strings.Contains(err.Error(), "NoSuchBucket") {
		t.Errorf("a missing bucket has to be named: %v", err)
	}
}

// A prefix lets the blocks share a bucket with other things — and must not
// blur the organisation boundary while doing it.
func TestS3PrefixKeepsTheOrganisationBoundary(t *testing.T) {
	ctx := context.Background()
	f := &fakeS3{bucket: "bucket", objects: map[string][]byte{}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	store, err := NewS3(srv.URL, "bucket", "covey/blocks", Credentials{
		AccessKey: "k", SecretKey: "s", Region: "eu",
	}, true)
	if err != nil {
		t.Fatal(err)
	}

	orgA, orgB := uuid.New(), uuid.New()
	hash := Hash([]byte("gemeinsamer Inhalt"))
	if err := store.Put(ctx, orgA, hash, strings.NewReader("gemeinsamer Inhalt")); err != nil {
		t.Fatal(err)
	}
	// Same content, other organisation: a second object, not a shared one.
	if has, err := store.Has(ctx, orgB, hash); err != nil || has {
		t.Error("the block must not be visible to the other organisation")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for key := range f.objects {
		if !strings.HasPrefix(key, "covey/blocks/"+orgA.String()+"/") {
			t.Errorf("the key does not respect prefix and organisation: %q", key)
		}
	}
}

// A runner reaches its blocks through the control plane, and two operations it
// deliberately cannot perform: deleting and enumerating. The garbage collection
// runs on the control plane, and removing a block is the one operation where a
// mistake is not recoverable. Refused rather than merely unused — an unused
// path is one somebody wires up later.
func TestRunnerStoreRefusesDeletingAndListing(t *testing.T) {
	ctx := context.Background()
	store := NewHTTPStore("https://covey.example", "runner-token")
	if err := store.Delete(ctx, uuid.New(), Hash([]byte("x"))); err == nil {
		t.Error("a runner must not delete blocks")
	}
	if _, err := store.List(ctx, uuid.New()); err == nil {
		t.Error("a runner must not enumerate an organisation's blocks")
	}
}

// Bei S3 ist jede Frage eine signierte HEAD-Anfrage über das Netz. Ein Home mit
// hunderttausend Dateien fragt so oft, bevor sein erstes neues Byte reist —
// hintereinander sind das Stunden, und ein Sync hat dreißig Minuten. Eine
// Bündelfrage gibt es bei S3 nicht, also ist Nebenläufigkeit der einzige Hebel.
func TestS3FragtNebenlaeufigUndAntwortetVollstaendig(t *testing.T) {
	ctx := context.Background()
	f, store := newFakeS3(t, 0)
	org := uuid.New()

	// Blöcke sind inhaltsadressiert: der Schlüssel IST der Hash des Inhalts,
	// und der Store prüft das — wie ein echter Bucket.
	vorhanden := map[string]bool{}
	var hashes []string
	for i := 0; i < 50; i++ {
		inhalt := fmt.Sprintf("block %d", i)
		h := Hash([]byte(inhalt))
		hashes = append(hashes, h)
		if i%2 == 0 {
			if err := store.Put(ctx, org, h, strings.NewReader(inhalt)); err != nil {
				t.Fatal(err)
			}
			vorhanden[h] = true
		}
	}

	// Gleichzeitigkeit messen: der Fake zählt mit, wie viele Anfragen sich
	// überschneiden.
	f.mu.Lock()
	f.parallel, f.maxParallel = 0, 0
	f.mu.Unlock()

	have, err := store.HasMany(ctx, org, hashes)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hashes {
		if have[h] != vorhanden[h] {
			t.Fatalf("falsche Auskunft für %s: %v statt %v", h[:8], have[h], vorhanden[h])
		}
	}
	f.mu.Lock()
	hoechstens := f.maxParallel
	f.mu.Unlock()
	if hoechstens < 2 {
		t.Errorf("die Fragen liefen nacheinander (höchstens %d gleichzeitig)", hoechstens)
	}
	if hoechstens > s3AskWorkers {
		t.Errorf("%d Anfragen gleichzeitig — mehr als die Grenze von %d", hoechstens, s3AskWorkers)
	}
}

// Ein Fehler beendet die Frage, und zwar mit Fehler: eine halb beantwortete
// Frage ließe einen Sync glauben, Blöcke seien vorhanden, die niemand bestätigt
// hat — und ein Schnappschuss, der auf einen fehlenden Block zeigt, ist
// schlimmer als ein Sync, der laut scheitert.
func TestS3EinFehlerBeendetDieBuendelfrage(t *testing.T) {
	ctx := context.Background()
	f, store := newFakeS3(t, 0)
	org := uuid.New()
	f.mu.Lock()
	f.headFails = true
	f.mu.Unlock()

	var hashes []string
	for i := 0; i < 20; i++ {
		hashes = append(hashes, Hash([]byte(fmt.Sprintf("block %d", i))))
	}
	if _, err := store.HasMany(ctx, org, hashes); err == nil {
		t.Error("ein Fehler des Buckets muss durchschlagen")
	}
}
