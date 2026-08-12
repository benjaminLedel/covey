package k8s

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"covey/internal/target"
)

// TestSecretsAreNeverReadable is the test that matters most in this package. A
// token bound too broadly (or a future ClusterRole change) must not turn this
// plugin into a way to copy every credential in the cluster into a run's
// context and its recording. The refusal has to happen HERE, before any
// request, and it has to explain itself — a bare "unknown kind" would read like
// a typo and invite the agent to try spellings.
func TestSecretsAreNeverReadable(t *testing.T) {
	for _, kind := range []string{"secrets", "Secrets", "SECRETS", "secret", "sealedsecrets"} {
		path, err := resourcePath(kind, "default", "some-secret")
		if err == nil {
			t.Fatalf("resourcePath(%q) returned a path (%q) — secrets must never be readable", kind, path)
		}
		if !strings.Contains(err.Error(), "secret") && !strings.Contains(err.Error(), "Secret") &&
			!strings.Contains(err.Error(), "SealedSecret") {
			t.Errorf("resourcePath(%q) refused, but the reason does not mention secrets: %v", kind, err)
		}
	}
	// The refusal must not depend on a namespace being supplied.
	if _, err := resourcePath("secrets", "", ""); err == nil {
		t.Error("a cluster-wide secrets listing must be refused too")
	}
}

func TestResourcePath(t *testing.T) {
	cases := []struct {
		kind, ns, name, want string
	}{
		{"pods", "prod", "web-0", "/api/v1/namespaces/prod/pods/web-0"},
		{"pods", "prod", "", "/api/v1/namespaces/prod/pods"},
		{"deployments", "prod", "web", "/apis/apps/v1/namespaces/prod/deployments/web"},
		{"ingresses", "prod", "", "/apis/networking.k8s.io/v1/namespaces/prod/ingresses"},
		{"networkpolicies", "prod", "", "/apis/networking.k8s.io/v1/namespaces/prod/networkpolicies"},
		{"nodes", "", "node-1", "/api/v1/nodes/node-1"},
		{"clusterrolebindings", "", "", "/apis/rbac.authorization.k8s.io/v1/clusterrolebindings"},
		{"  Deployments  ", "prod", "web", "/apis/apps/v1/namespaces/prod/deployments/web"},
	}
	for _, c := range cases {
		got, err := resourcePath(c.kind, c.ns, c.name)
		if err != nil {
			t.Errorf("resourcePath(%q,%q,%q): %v", c.kind, c.ns, c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("resourcePath(%q,%q,%q) = %q, want %q", c.kind, c.ns, c.name, got, c.want)
		}
	}

	// A namespaced kind without a namespace would otherwise silently read across
	// the whole cluster — that is a different question from the one asked.
	if _, err := resourcePath("pods", "", "web-0"); err == nil {
		t.Error("a namespaced kind without a namespace must be refused")
	}
	// And the reverse: a namespace on a cluster-scoped kind means the caller has
	// the wrong mental model and should hear about it.
	if _, err := resourcePath("nodes", "prod", ""); err == nil {
		t.Error("a namespace on a cluster-scoped kind must be refused")
	}
	if _, err := resourcePath("customresourcedefinitions", "", ""); err == nil {
		t.Error("a kind outside the allowlist must be refused")
	}
}

// TestNewClientRefusesUnverifiedTLS: there is no opt-out from certificate
// verification, and the failure modes that would tempt someone to add one have
// to fail loudly instead.
func TestNewClientRefusesUnverifiedTLS(t *testing.T) {
	if _, err := newClient("https://c:6443", "tok", "not a certificate"); err == nil {
		t.Error("an unparseable ca_pem must fail rather than fall back to skipping verification")
	}
	if _, err := newClient("http://c:6443", "tok", ""); err == nil {
		t.Error("a plaintext API server URL must be refused — the token would go over the wire")
	}
	if _, err := newClient("", "tok", ""); err == nil {
		t.Error("a missing k8s_url must name itself")
	}
	if _, err := newClient("https://c:6443", "  ", ""); err == nil {
		t.Error("a missing token must name itself")
	}
	if _, err := newClient("https://c:6443/", "tok", ""); err != nil {
		t.Errorf("a valid endpoint must be accepted: %v", err)
	}
}

// TestNewClientRequiresAnUnambiguousOrigin catches URL forms that can change
// how a later fixed Kubernetes API path is interpreted. k8s_url authorizes one
// API origin, not credentials, a preselected resource path, or URL metadata.
func TestNewClientRequiresAnUnambiguousOrigin(t *testing.T) {
	for _, raw := range []string{
		"https:///api/v1",
		"https://user@example.test",
		"https://example.test/prefix",
		"https://example.test?target=other",
		"https://example.test#fragment",
	} {
		t.Run(raw, func(t *testing.T) {
			if _, err := newClient(raw, "tok", ""); err == nil {
				t.Fatalf("ambiguous k8s_url %q must be refused", raw)
			}
		})
	}
}

// TestClientRefusesCrossOriginRedirect ensures the ServiceAccount token can be
// used only against the configured API origin. Redirects within that origin
// remain available for normal HTTP canonicalization, but a different origin
// is never contacted.
func TestClientRefusesCrossOriginRedirect(t *testing.T) {
	var destinationRequests int
	destination := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		destinationRequests++
		w.Write([]byte(`{"items":[]}`))
	}))
	defer destination.Close()

	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()

	var caPEM strings.Builder
	for _, cert := range []*x509.Certificate{origin.Certificate(), destination.Certificate()} {
		if err := pem.Encode(&caPEM, &pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}); err != nil {
			t.Fatal(err)
		}
	}
	c, err := newClient(origin.URL, "service-account-token", caPEM.String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.do(context.Background(), http.MethodGet, "/api/v1/namespaces", nil, nil); err == nil ||
		!strings.Contains(err.Error(), "redirect") {
		t.Fatalf("cross-origin redirect must be refused explicitly, got %v", err)
	}
	if destinationRequests != 0 {
		t.Fatalf("cross-origin destination was contacted %d time(s)", destinationRequests)
	}
}

// TestDeclutter: raw Kubernetes objects carry two fields that are routinely
// larger than the object they describe and never answer a question. They cost
// context on every single call, so their removal is load-bearing, not cosmetic.
func TestDeclutter(t *testing.T) {
	raw := []byte(`{
	  "metadata": {
	    "name": "web",
	    "managedFields": [{"manager":"kubectl","fieldsV1":{"f:spec":{}}}],
	    "annotations": {
	      "kubectl.kubernetes.io/last-applied-configuration": "{\"a\":1}",
	      "keep.me/please": "yes"
	    }
	  },
	  "spec": {"replicas": 2}
	}`)
	var got map[string]any
	if err := json.Unmarshal(declutter(raw), &got); err != nil {
		t.Fatal(err)
	}
	md := got["metadata"].(map[string]any)
	if _, ok := md["managedFields"]; ok {
		t.Error("managedFields survived")
	}
	ann := md["annotations"].(map[string]any)
	if _, ok := ann["kubectl.kubernetes.io/last-applied-configuration"]; ok {
		t.Error("last-applied-configuration survived")
	}
	if ann["keep.me/please"] != "yes" {
		t.Error("an unrelated annotation was removed — only the two noisy ones may go")
	}
	if md["name"] != "web" {
		t.Error("name was lost")
	}
	if got["spec"].(map[string]any)["replicas"].(float64) != 2 {
		t.Error("spec was altered")
	}

	// Lists get the same treatment per item, otherwise a `list` of twenty pods
	// carries twenty managedFields blocks.
	rawList := []byte(`{"items":[{"metadata":{"name":"a","managedFields":[{"m":1}]}}]}`)
	var gotList map[string]any
	if err := json.Unmarshal(declutter(rawList), &gotList); err != nil {
		t.Fatal(err)
	}
	item := gotList["items"].([]any)[0].(map[string]any)["metadata"].(map[string]any)
	if _, ok := item["managedFields"]; ok {
		t.Error("managedFields survived inside a list item")
	}

	// Malformed input must pass through rather than blank the answer.
	if string(declutter([]byte("not json"))) != "not json" {
		t.Error("unparseable input must be returned unchanged")
	}
}

// TestExecuteRejectsBadInputBeforeDialling: every action validates its
// arguments before a request goes out, so a missing namespace is an immediate,
// readable error rather than a call against the wrong scope.
func TestExecuteRejectsBadInputBeforeDialling(t *testing.T) {
	cred := target.Credential{BaseURL: "https://127.0.0.1:1", Token: "tok"}
	cases := []struct{ action, params string }{
		{"pods", `{}`},                                // no namespace
		{"logs", `{"namespace":"prod"}`},              // no pod
		{"logs", `{"name":"web-0"}`},                  // no namespace
		{"events", `{}`},                              // no namespace
		{"get", `{"kind":"pods","namespace":"prod"}`}, // no name
		{"get", `{"kind":"secrets","namespace":"prod","name":"db"}`},
		{"restart", `{"namespace":"prod"}`},    // no deployment
		{"delete_pod", `{"namespace":"prod"}`}, // no name
		{"nonsense", `{}`},
	}
	for _, c := range cases {
		if _, err := (System{}).Execute(context.Background(), c.action, json.RawMessage(c.params), cred); err == nil {
			t.Errorf("Execute(%s, %s) succeeded; expected a validation error", c.action, c.params)
		}
	}
}

// TestActionSubjects: the two mutating actions must be separately addressable so
// a guard rail can gate a deletion without also blocking every read.
func TestActionSubjects(t *testing.T) {
	for action, want := range map[string]string{
		"pods": "k8s:pods", "logs": "k8s:logs",
		"restart": "k8s:restart", "delete_pod": "k8s:delete_pod",
	} {
		if got := (System{}).ActionSubject(action, nil); got != want {
			t.Errorf("ActionSubject(%q) = %q, want %q", action, got, want)
		}
	}
}

// TestRegistered: the plugin has to be in the registry, and it must NOT be
// marked NoCredentials — it needs a brokered token and a base URL, and the
// broker decides that from the descriptor.
func TestRegistered(t *testing.T) {
	d, ok := target.Describe("k8s")
	if !ok {
		t.Fatal("k8s is not registered")
	}
	if d.NoCredentials {
		t.Error("k8s needs brokered credentials — NoCredentials would skip the token entirely")
	}
	if d.CredentialsOptional {
		t.Error("k8s without a token cannot do anything — the credential is not optional")
	}
	if !strings.Contains(d.SetupDoc, "view") {
		t.Error("the setup doc has to name the read-only ClusterRole — RBAC is the real limit")
	}
	if _, ok := target.Get("k8s"); !ok {
		t.Error("k8s has no System implementation in the registry")
	}
}

// TestPromptDocNamesTheGitOpsRule: the doc is the only place an agent learns
// why it must not try to change the cluster directly. If that paragraph is ever
// dropped, agents will start applying manifests that ArgoCD silently reverts.
func TestPromptDocNamesTheGitOpsRule(t *testing.T) {
	doc := (System{}).PromptDoc()
	for _, want := range []string{"merge request", "SECRETS ARE NOT READABLE", "previous"} {
		if !strings.Contains(doc, want) {
			t.Errorf("PromptDoc does not mention %q", want)
		}
	}
}

func TestPromptDocForScopesReadOnlyHidesLogsAndWrites(t *testing.T) {
	doc := (System{}).PromptDocForScopes([]string{"read"})
	for _, phrase := range []string{"pods {", "events {", "list {", "get {", "merge request"} {
		if !strings.Contains(doc, phrase) {
			t.Errorf("read-only prompt doc must include %q", phrase)
		}
	}
	for _, phrase := range []string{"logs {", "restart {", "delete_pod {"} {
		if strings.Contains(doc, phrase) {
			t.Errorf("read-only prompt doc must hide %q", phrase)
		}
	}
}

func TestPromptDocForScopesAddsOnlyGrantedCapabilities(t *testing.T) {
	logsDoc := (System{}).PromptDocForScopes([]string{"read", "logs"})
	if !strings.Contains(logsDoc, "logs {") {
		t.Fatal("logs scope must add the logs action")
	}
	for _, phrase := range []string{"restart {", "delete_pod {"} {
		if strings.Contains(logsDoc, phrase) {
			t.Errorf("logs scope must not add write action %q", phrase)
		}
	}

	writeDoc := (System{}).PromptDocForScopes([]string{"read", "write"})
	for _, phrase := range []string{"restart {", "delete_pod {"} {
		if !strings.Contains(writeDoc, phrase) {
			t.Errorf("write scope must add %q", phrase)
		}
	}
	if strings.Contains(writeDoc, "logs {") {
		t.Error("write scope alone must not add the logs action")
	}
}

func TestPromptDocForScopesEmptyAndFullStayCompatible(t *testing.T) {
	full := (System{}).PromptDoc()
	if got := (System{}).PromptDocForScopes(nil); got != full {
		t.Fatal("an empty scope list must fail open to the full prompt doc")
	}
	if got := (System{}).PromptDocForScopes([]string{"read", "logs", "write"}); got != full {
		t.Fatal("all scopes must produce the unchanged full prompt doc")
	}
}
