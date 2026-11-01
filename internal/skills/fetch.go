package skills

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"syscall"
	"time"
)

// Fetching a skill archive from a URL.
//
// Accepted are a URL to a zip, a GitHub repository or tree/blob URL, and a
// GitLab-style tree/blob URL (`…/-/tree/<ref>/<path>`, any host). The two
// forges serve a repository as a zip; the path inside it becomes the subdir
// FromZip filters by. A ref that itself contains a slash cannot be told apart
// from the path in such a URL — give the archive URL directly then.
//
// The control plane fetches on behalf of a user, so the request must not be a
// way into the network the control plane sits in: https only, and every
// connection — redirects included — is checked at dial time against the
// address it really goes to, which a hostname resolving to 10.0.0.1 or
// 169.254.169.254 cannot get past (DNS rebinding included).

// Source is what a URL resolves to.
type Source struct {
	ArchiveURL string
	Subdir     string
}

// ResolveSource turns a user-given URL into an archive URL and a subdir.
func ResolveSource(raw string) (Source, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return Source{}, fmt.Errorf("%w: %q is not a URL", ErrInvalid, raw)
	}
	if u.Scheme != "https" {
		return Source{}, fmt.Errorf("%w: only https URLs are fetched", ErrInvalid)
	}
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	host := strings.ToLower(u.Hostname())

	if host == "github.com" || host == "www.github.com" {
		if len(segs) < 2 || segs[0] == "" {
			return Source{}, fmt.Errorf("%w: a GitHub URL needs owner and repository", ErrInvalid)
		}
		owner, repo := segs[0], strings.TrimSuffix(segs[1], ".git")
		ref, sub := "HEAD", ""
		if len(segs) >= 4 && (segs[2] == "tree" || segs[2] == "blob") {
			ref, sub = segs[3], strings.Join(segs[4:], "/")
			if segs[2] == "blob" {
				sub = blobDir(sub)
			}
		} else if len(segs) > 2 {
			return Source{}, fmt.Errorf("%w: GitHub URL not understood — use the repository, a …/tree/<ref>/<path> or a …/blob/<ref>/<path>/SKILL.md URL", ErrInvalid)
		}
		return Source{
			ArchiveURL: "https://codeload.github.com/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/zip/" + url.PathEscape(ref),
			Subdir:     sub,
		}, nil
	}

	if i := indexSeq(segs, "-", "tree"); i < 0 {
		if j := indexSeq(segs, "-", "blob"); j >= 0 {
			return gitlabSource(u, segs, j, true)
		}
	} else {
		return gitlabSource(u, segs, i, false)
	}
	return Source{ArchiveURL: u.String()}, nil
}

func gitlabSource(u *url.URL, segs []string, i int, blob bool) (Source, error) {
	if i == 0 || len(segs) < i+3 {
		return Source{}, fmt.Errorf("%w: GitLab URL not understood — expected …/-/tree/<ref>/<path>", ErrInvalid)
	}
	project := strings.Join(segs[:i], "/")
	ref := segs[i+2]
	sub := strings.Join(segs[i+3:], "/")
	if blob {
		sub = blobDir(sub)
	}
	a := url.URL{Scheme: "https", Host: u.Host,
		Path: "/" + project + "/-/archive/" + ref + "/" + segs[i-1] + "-" + ref + ".zip"}
	if sub != "" {
		a.RawQuery = url.Values{"path": {sub}}.Encode()
	}
	return Source{ArchiveURL: a.String(), Subdir: sub}, nil
}

// blobDir turns the path of a file link into the directory it names: a link
// to …/SKILL.md means the skill around it.
func blobDir(p string) string {
	if path.Base(p) == EntryFile {
		return dirOf(p)
	}
	return p
}

func indexSeq(segs []string, a, b string) int {
	for i := 0; i+1 < len(segs); i++ {
		if segs[i] == a && segs[i+1] == b {
			return i
		}
	}
	return -1
}

// Fetcher downloads skill archives. The zero value uses the guarded client;
// tests hand in their own.
type Fetcher struct {
	Client *http.Client
}

// Fetch resolves raw and downloads the archive. It returns the archive bytes
// and the subdir to filter by.
func (f Fetcher) Fetch(ctx context.Context, raw string) ([]byte, string, error) {
	src, err := ResolveSource(raw)
	if err != nil {
		return nil, "", err
	}
	client := f.Client
	if client == nil {
		client = guardedClient()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.ArchiveURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("%w: download failed: %v", ErrInvalid, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("%w: download failed: GET %s answered HTTP %d", ErrInvalid, src.ArchiveURL, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxArchiveBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("%w: download failed: %v", ErrInvalid, err)
	}
	if len(body) > maxArchiveBytes {
		return nil, "", fmt.Errorf("%w: archive larger than %d bytes", ErrInvalid, maxArchiveBytes)
	}
	if !bytes.HasPrefix(body, []byte("PK")) {
		return nil, "", fmt.Errorf("%w: %s did not answer with a zip archive", ErrInvalid, src.ArchiveURL)
	}
	return body, src.Subdir, nil
}

// errBlockedAddress is what the dial guard answers for a non-public address.
var errBlockedAddress = errors.New("address is not public — refusing to connect")

func guardedClient() *http.Client {
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil || !PublicIP(ip) {
				return fmt.Errorf("%s: %w", host, errBlockedAddress)
			}
			return nil
		},
	}
	return &http.Client{
		Timeout: 2 * time.Minute,
		Transport: &http.Transport{
			// No proxy: through one, the guard would check the proxy's address
			// and not the target's.
			Proxy:                 nil,
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			MaxIdleConns:          2,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "https" {
				return errors.New("redirect away from https refused")
			}
			return nil
		},
	}
}

// nonPublic are ranges net.IP's own predicates do not cover.
var nonPublic = func() []*net.IPNet {
	var out []*net.IPNet
	for _, c := range []string{
		"0.0.0.0/8",     // "this network"
		"100.64.0.0/10", // carrier-grade NAT
		"192.0.0.0/24",  // IETF protocol assignments
		"198.18.0.0/15", // benchmarking
		"240.0.0.0/4",   // reserved
		"64:ff9b::/96",  // NAT64 — maps onto IPv4, private ones included
		"2001:db8::/32", // documentation
	} {
		_, n, _ := net.ParseCIDR(c)
		out = append(out, n)
	}
	return out
}()

// PublicIP tells whether ip is an address on the public internet.
func PublicIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsMulticast() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	for _, n := range nonPublic {
		if n.Contains(ip) {
			return false
		}
	}
	return true
}
