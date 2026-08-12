package k8s

import (
	"encoding/json"
	"fmt"
	"strings"
)

// resourceKind is one readable kind: which API group it lives in and whether it
// is namespaced. A curated list rather than the discovery API — discovery would
// let an agent read every CRD in the cluster, and the list below is what a
// diagnosing or reviewing agent actually needs.
type resourceKind struct {
	// Path is the API prefix: "/api/v1" for core, "/apis/<group>/<version>"
	// otherwise.
	Path string
	// Plural is the resource name in the URL.
	Plural string
	// Namespaced: false for cluster-scoped kinds (nodes, namespaces, the
	// cluster-wide RBAC objects).
	Namespaced bool
}

// kinds is the allowlist. Everything a diagnosis needs (workloads, events,
// networking) plus the objects a security review reads (RBAC, network policies,
// service accounts).
//
// SECRETS ARE ABSENT, and that is the single most important line in this
// package. A token that may read Secrets can read every credential in the
// cluster — database passwords, the Keycloak admin, image-pull secrets — and an
// action that returned them would copy all of it into an LLM's context and from
// there into a recording that outlives the run. Kubernetes' own `view` role
// excludes secrets for exactly this reason; this list refuses them a second
// time, so a cluster whose token was bound too broadly still cannot leak them
// through this plugin.
var kinds = map[string]resourceKind{
	"pods":                     {"/api/v1", "pods", true},
	"services":                 {"/api/v1", "services", true},
	"configmaps":               {"/api/v1", "configmaps", true},
	"events":                   {"/api/v1", "events", true},
	"persistentvolumeclaims":   {"/api/v1", "persistentvolumeclaims", true},
	"serviceaccounts":          {"/api/v1", "serviceaccounts", true},
	"namespaces":               {"/api/v1", "namespaces", false},
	"nodes":                    {"/api/v1", "nodes", false},
	"deployments":              {"/apis/apps/v1", "deployments", true},
	"statefulsets":             {"/apis/apps/v1", "statefulsets", true},
	"daemonsets":               {"/apis/apps/v1", "daemonsets", true},
	"replicasets":              {"/apis/apps/v1", "replicasets", true},
	"jobs":                     {"/apis/batch/v1", "jobs", true},
	"cronjobs":                 {"/apis/batch/v1", "cronjobs", true},
	"ingresses":                {"/apis/networking.k8s.io/v1", "ingresses", true},
	"networkpolicies":          {"/apis/networking.k8s.io/v1", "networkpolicies", true},
	"roles":                    {"/apis/rbac.authorization.k8s.io/v1", "roles", true},
	"rolebindings":             {"/apis/rbac.authorization.k8s.io/v1", "rolebindings", true},
	"clusterroles":             {"/apis/rbac.authorization.k8s.io/v1", "clusterroles", false},
	"clusterrolebindings":      {"/apis/rbac.authorization.k8s.io/v1", "clusterrolebindings", false},
	"horizontalpodautoscalers": {"/apis/autoscaling/v2", "horizontalpodautoscalers", true},
}

// forbiddenKinds are named explicitly so the refusal can say why instead of
// answering "unknown kind", which would read like a typo and invite retries.
var forbiddenKinds = map[string]string{
	"secrets":       "Secrets are never readable through this plugin — they would end up in the run's context and recording. Read the sealed manifests in the GitOps repository instead, or check what a workload MOUNTS (that is visible on the pod).",
	"secret":        "see \"secrets\"",
	"sealedsecrets": "SealedSecrets are safe by construction but belong to the GitOps repository, not here — read them there, with their history.",
}

// resourcePath builds the collection or object path for a kind.
func resourcePath(kind, namespace, name string) (string, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if why, bad := forbiddenKinds[kind]; bad {
		return "", fmt.Errorf("%s", why)
	}
	rk, ok := kinds[kind]
	if !ok {
		return "", fmt.Errorf("kind %q is not readable through this plugin — available: %s", kind, strings.Join(kindNames(), ", "))
	}
	var b strings.Builder
	b.WriteString(rk.Path)
	if rk.Namespaced {
		if strings.TrimSpace(namespace) == "" {
			return "", fmt.Errorf("namespace is required for %s", kind)
		}
		b.WriteString("/namespaces/" + namespace)
	} else if strings.TrimSpace(namespace) != "" {
		return "", fmt.Errorf("%s is cluster-scoped — do not pass a namespace", kind)
	}
	b.WriteString("/" + rk.Plural)
	if n := strings.TrimSpace(name); n != "" {
		b.WriteString("/" + n)
	}
	return b.String(), nil
}

func kindNames() []string {
	out := make([]string, 0, len(kinds))
	for k := range kinds {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}

// sortStrings: a three-line insertion sort beats importing sort for one call
// site whose input is twenty compile-time constants.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// declutter removes the fields that make raw Kubernetes objects unusable as
// model input. `managedFields` alone is routinely larger than the spec it
// describes, and the last-applied-configuration annotation is a second copy of
// the whole object. Neither ever answers a question anybody asked.
func declutter(raw json.RawMessage) json.RawMessage {
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return raw
	}
	strip(obj)
	if items, ok := obj["items"].([]any); ok {
		for _, it := range items {
			if m, ok := it.(map[string]any); ok {
				strip(m)
			}
		}
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return raw
	}
	return out
}

func strip(obj map[string]any) {
	md, ok := obj["metadata"].(map[string]any)
	if !ok {
		return
	}
	delete(md, "managedFields")
	if ann, ok := md["annotations"].(map[string]any); ok {
		delete(ann, "kubectl.kubernetes.io/last-applied-configuration")
		if len(ann) == 0 {
			delete(md, "annotations")
		}
	}
}
