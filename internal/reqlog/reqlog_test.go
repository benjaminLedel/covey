package reqlog

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// collect gathers the entries of a sink.
func collect() (Sink, *[]Entry) {
	var got []Entry
	return func(e Entry) { got = append(got, e) }, &got
}

func TestTransportProtokolliertRequestUndAntwort(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":"invalid_client"}`)
	}))
	defer srv.Close()

	sink, got := collect()
	ctx := WithSink(context.Background(), sink)
	client := Client("teams", 5*time.Second)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/v3/conversations",
		strings.NewReader(`{"text":"hallo","client_secret":"geheim"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if string(body) != `{"error":"invalid_client"}` {
		t.Fatalf("response mangled: %q", body)
	}
	if len(*got) != 1 {
		t.Fatalf("expected exactly one entry, have %d", len(*got))
	}
	e := (*got)[0]
	if e.Direction != DirectionOut || e.System != "teams" || e.Method != http.MethodPost {
		t.Fatalf("head wrong: %+v", e)
	}
	if e.Status != http.StatusUnauthorized {
		t.Fatalf("status %d", e.Status)
	}
	if !strings.Contains(e.RespBody, "invalid_client") {
		t.Fatalf("response body missing: %q", e.RespBody)
	}
	if strings.Contains(e.ReqBody, "geheim") {
		t.Fatalf("secret not redacted: %q", e.ReqBody)
	}
	if !strings.Contains(e.ReqBody, "hallo") {
		t.Fatalf("payload missing: %q", e.ReqBody)
	}
}

func TestTransportOhneSenkeReichtDurch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	}))
	defer srv.Close()

	resp, err := Client("zammad", 5*time.Second).Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Fatalf("body %q", body)
	}
}

func TestTransportProtokolliertVerbindungsfehler(t *testing.T) {
	sink, got := collect()
	ctx := WithSink(context.Background(), sink)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:1/nix", nil)
	if _, err := Client("teams", time.Second).Do(req); err == nil {
		t.Fatal("expected an error")
	}
	if len(*got) != 1 || (*got)[0].Error == "" {
		t.Fatalf("error not logged: %+v", *got)
	}
}

func TestRedactURLEntferntGeheimeQueryWerte(t *testing.T) {
	u, _ := url.Parse("https://example.test/api?access_token=abc&page=2")
	out := RedactURL(u)
	if strings.Contains(out, "abc") {
		t.Fatalf("token not redacted: %s", out)
	}
	if !strings.Contains(out, "page=2") {
		t.Fatalf("harmless parameters lost: %s", out)
	}
}

func TestRedactTrifftJSONUndForm(t *testing.T) {
	in := `{"access_token":"xyz","name":"Ada"}` + "\n" + `grant_type=client_credentials&client_secret=xyz`
	out := Redact(in)
	if strings.Contains(out, "xyz") {
		t.Fatalf("secret survived: %s", out)
	}
	if !strings.Contains(out, "Ada") || !strings.Contains(out, "grant_type=client_credentials") {
		t.Fatalf("too much redacted: %s", out)
	}
}

func TestSystemFromPath(t *testing.T) {
	if got := SystemFromPath("/api/webhooks/teams/support-1"); got != "teams" {
		t.Fatalf("system %q", got)
	}
	if got := SystemFromPath("/api/trigger/abc"); got != "" {
		t.Fatalf("expected empty, have %q", got)
	}
}

func TestContextSenkeSchlaegtDefault(t *testing.T) {
	defSink, defGot := collect()
	SetDefault(defSink)
	defer SetDefault(nil)

	ctxSink, ctxGot := collect()
	ctx := WithSink(context.Background(), ctxSink)
	Emit(ctx, Entry{Direction: DirectionIn, Method: http.MethodPost})
	Emit(context.Background(), Entry{Direction: DirectionIn, Method: http.MethodGet})

	if len(*ctxGot) != 1 || len(*defGot) != 1 {
		t.Fatalf("distribution wrong: ctx=%d default=%d", len(*ctxGot), len(*defGot))
	}
}
