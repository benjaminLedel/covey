package homestore

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// S3 is the BlobStore backend for an S3-compatible object store — the second
// implementation of the port, for when durability, replication or separation
// from the control plane's disk is wanted (spec/16, "Where the blocks live").
//
// S3-compatible and not "AWS S3": the protocol is the common denominator of
// Hetzner Object Storage, Garage, MinIO, Ceph RadosGW and SeaweedFS. covey does
// not prescribe a server, it speaks the protocol.
type S3 struct {
	// Endpoint is the base address, e.g. https://s3.eu-central-1.example.com.
	Endpoint string
	Bucket   string
	Creds    Credentials
	// PathStyle addresses the bucket in the path (…/bucket/key) instead of in
	// the host name. The default, because it is what the self-hosted servers
	// speak without a wildcard certificate — and the ones this is written for
	// are mostly self-hosted.
	PathStyle bool
	// Prefix puts the blocks under a common path, for a bucket that holds more
	// than covey.
	Prefix string

	Client *http.Client
	// now is overridable for tests; nil = time.Now.
	now func() time.Time
}

func NewS3(endpoint, bucket, prefix string, creds Credentials, pathStyle bool) (*S3, error) {
	if endpoint == "" || bucket == "" {
		return nil, fmt.Errorf("s3: endpoint and bucket are required")
	}
	if creds.AccessKey == "" || creds.SecretKey == "" {
		return nil, fmt.Errorf("s3: access key and secret key are required")
	}
	if creds.Region == "" {
		// Servers that have no notion of regions still want one in the
		// signature. us-east-1 is what practically all of them accept.
		creds.Region = "us-east-1"
	}
	return &S3{
		Endpoint:  strings.TrimRight(endpoint, "/"),
		Bucket:    bucket,
		Prefix:    strings.Trim(prefix, "/"),
		Creds:     creds,
		PathStyle: pathStyle,
		// Long enough for a 4 MB block over a slow line, short enough that a
		// hung transfer does not hold a wake for ever.
		Client: &http.Client{Timeout: 5 * time.Minute},
	}, nil
}

// key is where a block lives. The organisation is the first element, as with
// the built-in backend: a block namespace shared across tenants would be an
// existence oracle over hashes.
func (s *S3) key(orgID uuid.UUID, hash string) string {
	parts := []string{}
	if s.Prefix != "" {
		parts = append(parts, s.Prefix)
	}
	return strings.Join(append(parts, orgID.String(), hash[:2], hash), "/")
}

func (s *S3) url(key string) string {
	if s.PathStyle {
		return s.Endpoint + "/" + s.Bucket + "/" + key
	}
	u, err := url.Parse(s.Endpoint)
	if err != nil {
		return s.Endpoint + "/" + key
	}
	u.Host = s.Bucket + "." + u.Host
	u.Path = "/" + key
	return u.String()
}

func (s *S3) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *S3) do(ctx context.Context, method, rawURL string, body []byte, payloadHash string) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.ContentLength = int64(len(body))
	}
	Sign(req, s.Creds, payloadHash, s.clock())
	return s.Client.Do(req)
}

func (s *S3) Has(ctx context.Context, orgID uuid.UUID, hash string) (bool, error) {
	if err := validHash(hash); err != nil {
		return false, err
	}
	resp, err := s.do(ctx, http.MethodHead, s.url(s.key(orgID, hash)), nil, emptyPayload)
	if err != nil {
		return false, err
	}
	defer drain(resp)
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound, http.StatusForbidden:
		// 403 on HEAD is what a bucket without ListBucket permission answers
		// for a missing key. Treated as "not there": the alternative would be
		// to abort every sync on a bucket policy that is otherwise perfectly
		// workable.
		return false, nil
	default:
		return false, s.fail(resp, "head")
	}
}

// s3AskWorkers: wie viele HEAD-Anfragen gleichzeitig unterwegs sein dürfen.
//
// S3 kennt keine Bündelfrage — es gibt kein HEAD für viele Schlüssel. Der
// einzige Hebel ist also, nicht hintereinander zu fragen. Sechzehn ist die
// Zahl, die ein Sync eines gewachsenen Homes von Stunden auf Minuten bringt,
// ohne dass ein Bucket sie als Sturm liest; wer mehr will, hat ein anderes
// Problem als diese Konstante.
const s3AskWorkers = 16

// HasMany satisfies BulkAsker for a bucket: sixteen questions at a time
// instead of one after another.
//
// Why this matters here and not for a directory: every Has against S3 is a
// signed HEAD across the network. A home with 150,000 files asks that many
// times before its first new byte travels — serially that is hours, and the
// control plane gives a sync thirty minutes.
func (s *S3) HasMany(ctx context.Context, orgID uuid.UUID, hashes []string) (map[string]bool, error) {
	return AskEach(ctx, s, orgID, hashes, s3AskWorkers)
}

func (s *S3) Put(ctx context.Context, orgID uuid.UUID, hash string, r io.Reader) error {
	if err := validHash(hash); err != nil {
		return err
	}
	body, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	// The block's hash IS the SHA-256 of its content — exactly what
	// x-amz-content-sha256 wants. Nothing is hashed twice.
	resp, err := s.do(ctx, http.MethodPut, s.url(s.key(orgID, hash)), body, hash)
	if err != nil {
		return err
	}
	defer drain(resp)
	if resp.StatusCode >= 300 {
		return s.fail(resp, "put")
	}
	return nil
}

func (s *S3) Get(ctx context.Context, orgID uuid.UUID, hash string) (io.ReadCloser, error) {
	if err := validHash(hash); err != nil {
		return nil, err
	}
	resp, err := s.do(ctx, http.MethodGet, s.url(s.key(orgID, hash)), nil, emptyPayload)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		drain(resp)
		return nil, fmt.Errorf("%w: %s", ErrNotFound, hash)
	}
	if resp.StatusCode != http.StatusOK {
		err := s.fail(resp, "get")
		drain(resp)
		return nil, err
	}
	return resp.Body, nil
}

func (s *S3) Delete(ctx context.Context, orgID uuid.UUID, hash string) error {
	if err := validHash(hash); err != nil {
		return err
	}
	resp, err := s.do(ctx, http.MethodDelete, s.url(s.key(orgID, hash)), nil, emptyPayload)
	if err != nil {
		return err
	}
	defer drain(resp)
	// 404 is not a failure: deleting something that is not there has the
	// outcome the caller wanted.
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotFound {
		return s.fail(resp, "delete")
	}
	return nil
}

// listResult is as much of ListObjectsV2 as is needed.
type listResult struct {
	XMLName               xml.Name `xml:"ListBucketResult"`
	IsTruncated           bool     `xml:"IsTruncated"`
	NextContinuationToken string   `xml:"NextContinuationToken"`
	Contents              []struct {
		Key  string `xml:"Key"`
		Size int64  `xml:"Size"`
	} `xml:"Contents"`
}

// List enumerates an organisation's blocks — what the mark-and-sweep of the
// retention needs. Paged, because a grown store holds hundreds of thousands of
// blocks and the answer comes a thousand at a time.
func (s *S3) List(ctx context.Context, orgID uuid.UUID) ([]string, error) {
	names, _, err := s.list(ctx, orgID)
	return names, err
}

// Size is what an organisation's blocks occupy — the fill level for the
// dashboard. It falls out of the same listing, so it costs no second pass.
func (s *S3) Size(ctx context.Context, orgID uuid.UUID) (int64, error) {
	_, total, err := s.list(ctx, orgID)
	return total, err
}

func (s *S3) list(ctx context.Context, orgID uuid.UUID) ([]string, int64, error) {
	prefix := s.key(orgID, "00")
	prefix = strings.TrimSuffix(prefix, "/00/00") + "/"

	var names []string
	var total int64
	token := ""
	for {
		q := url.Values{}
		q.Set("list-type", "2")
		q.Set("prefix", prefix)
		if token != "" {
			q.Set("continuation-token", token)
		}
		base := s.url("")
		resp, err := s.do(ctx, http.MethodGet, strings.TrimSuffix(base, "/")+"?"+q.Encode(), nil, emptyPayload)
		if err != nil {
			return nil, 0, err
		}
		if resp.StatusCode != http.StatusOK {
			err := s.fail(resp, "list")
			drain(resp)
			return nil, 0, err
		}
		var out listResult
		if err := xml.NewDecoder(resp.Body).Decode(&out); err != nil {
			drain(resp)
			return nil, 0, err
		}
		drain(resp)

		for _, item := range out.Contents {
			total += item.Size
			// The block name is the last path element; everything before it is
			// the layout, which is ours and not the caller's business.
			if idx := strings.LastIndex(item.Key, "/"); idx >= 0 {
				names = append(names, item.Key[idx+1:])
			}
		}
		if !out.IsTruncated || out.NextContinuationToken == "" {
			return names, total, nil
		}
		token = out.NextContinuationToken
	}
}

// BlockSize is how much one block occupies — what the cleanup preview adds up.
// BlockModified is the object's Last-Modified — the age a sweep judges by
// before it deletes something a running sync may still need (#137).
func (s *S3) BlockModified(ctx context.Context, orgID uuid.UUID, hash string) (time.Time, error) {
	if err := validHash(hash); err != nil {
		return time.Time{}, err
	}
	resp, err := s.do(ctx, http.MethodHead, s.url(s.key(orgID, hash)), nil, emptyPayload)
	if err != nil {
		return time.Time{}, err
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		return time.Time{}, s.fail(resp, "head")
	}
	return http.ParseTime(resp.Header.Get("Last-Modified"))
}

func (s *S3) BlockSize(ctx context.Context, orgID uuid.UUID, hash string) (int64, error) {
	if err := validHash(hash); err != nil {
		return 0, err
	}
	resp, err := s.do(ctx, http.MethodHead, s.url(s.key(orgID, hash)), nil, emptyPayload)
	if err != nil {
		return 0, err
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		return 0, s.fail(resp, "head")
	}
	return strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64)
}

// Check reports whether this store can be reached and written to at all —
// asked at startup, so a wrong key or a missing bucket says so there instead of
// at the first agent's falling asleep.
func (s *S3) Check(ctx context.Context) error {
	probe := Hash([]byte("covey-store-check"))
	if err := s.Put(ctx, uuid.Nil, probe, strings.NewReader("covey-store-check")); err != nil {
		return fmt.Errorf("the object store is not writable: %w", err)
	}
	if _, err := s.Has(ctx, uuid.Nil, probe); err != nil {
		return fmt.Errorf("the object store is not readable: %w", err)
	}
	return s.Delete(ctx, uuid.Nil, probe)
}

// fail turns an error response into a message somebody can act on. The body
// carries the reason (SignatureDoesNotMatch, NoSuchBucket, AccessDenied), and
// without it a 403 is a wall.
func (s *S3) fail(resp *http.Response, op string) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2000))
	var out struct {
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	}
	_ = xml.Unmarshal(raw, &out)
	if out.Code != "" {
		return fmt.Errorf("s3 %s: %s: %s (%s)", op, resp.Status, out.Code, out.Message)
	}
	return fmt.Errorf("s3 %s: %s: %s", op, resp.Status, strings.TrimSpace(string(raw)))
}

func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
}

// validHash keeps anything that is not a hash out of a key. The hashes come
// from our own hashing, but also back over the protocol from a runner — and a
// "hash" with a slash in it would write somewhere else entirely.
func validHash(hash string) error {
	if len(hash) < 4 || !isHex(hash) {
		return fmt.Errorf("%w: invalid block hash %q", ErrNotFound, hash)
	}
	return nil
}
