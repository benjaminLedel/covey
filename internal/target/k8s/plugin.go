// Package k8s gives an agent read access to a Kubernetes cluster — the
// observability half of operating one, not the control half.
//
// The split is not caution for its own sake, it follows the deployment model.
// A GitOps cluster reconciles its state from a repository (ArgoCD, Flux): an
// agent that applied a manifest directly would either have it reverted on the
// next sync, or cause drift somebody has to chase later. The write path for
// such a cluster already exists and is the gitlab plugin — a merge request
// against the infrastructure repository, reviewed like every other change.
// What is missing, and what this plugin adds, is the ability to LOOK: why is a
// pod restarting, what does its log say, is that Ingress actually pointing
// where the ticket claims, does this namespace have a NetworkPolicy at all.
//
// The authoritative limit is the cluster's own RBAC, not this plugin's scopes.
// A ServiceAccount bound to `view` cannot delete a pod however the agent's
// ACCESS.md reads, and per-agent secrets mean a security agent and an ops agent
// can hold tokens for two different ServiceAccounts. Covey's scopes shape what
// an agent is TOLD it can do; Kubernetes decides what it can actually do. Both
// matter, in that order.
package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"covey/internal/target"
)

type System struct{}

func init() {
	target.Register(target.Descriptor{
		Name:     "k8s",
		Label:    "Kubernetes",
		Category: target.CategoryDev,
		Description: "Read a Kubernetes cluster: pod states and restarts, container logs (including the " +
			"previous, crashed container), events, workloads, Ingresses, and the objects a security " +
			"review needs (RBAC, NetworkPolicies, ServiceAccounts). Secrets are never readable. " +
			"Writes are deliberately limited to two operational actions (restart a Deployment, delete a " +
			"stuck Pod) because a GitOps cluster takes its desired state from the infrastructure " +
			"repository — everything else goes there as a merge request.",
		Kind:   "builtin",
		System: System{},
		SetupDoc: `1. Create a ServiceAccount per agent role in the cluster, and bind it to the
   RIGHT ClusterRole — this is the real limit, not the scope list below:
     - a review/security agent: the built-in "view" role (excludes Secrets)
     - an ops agent: "view", plus a small custom Role if it is to restart
       workloads (patch on deployments, delete on pods, in named namespaces only)
   Put the ServiceAccount and its binding in the GitOps repository like every
   other cluster object, so who may read what is reviewable in git history.

2. Mint a token for it (` + "`kubectl create token <sa> --duration=…`" + ` for a
   short-lived one, or a bound Secret for a long-lived one) and store it as the
   AGENT-scoped secret "k8s_token" — agent-scoped, not org-scoped, so two
   agents can hold two differently privileged tokens under the same name.

3. Store the API server endpoint as "k8s_url" (https://…:6443).

4. If the API server presents a self-signed certificate — the k3s default —
   store the cluster CA as a secret too and pass it per action as
   {{secret:k8s_ca}} in "ca_pem". Without it the system roots apply, which is
   correct only for an API server with a publicly trusted certificate. There is
   no option to skip verification.

5. Release the API server host in the agent's egress allowlist.

6. Enable it in the agent's ACCESS.md:
   - system: k8s scope: read
   Add "logs" for container logs (they can contain anything the application
   printed, credentials included — that is a separate decision from reading
   object state). Add "write" only for an agent that should restart workloads,
   and pair it with a guard rail on k8s:delete_pod if that should stay a
   human decision.`,
	})
}

func (System) Name() string { return "k8s" }

// ActionSubject keeps the two mutating actions separately addressable, so a
// guard rail can require approval for a deletion without touching reads.
func (System) ActionSubject(action string, _ json.RawMessage) string {
	return "k8s:" + action
}

type params struct {
	Namespace  string `json:"namespace"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Selector   string `json:"selector"`
	Container  string `json:"container"`
	TailLines  int    `json:"tail_lines"`
	Previous   bool   `json:"previous"`
	Limit      int    `json:"limit"`
	Deployment string `json:"deployment"`
	CAPEM      string `json:"ca_pem"`
}

func (System) Execute(ctx context.Context, action string, raw json.RawMessage, cred target.Credential) (any, error) {
	var in params
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, fmt.Errorf("params: %w", err)
		}
	}
	c, err := newClient(cred.BaseURL, cred.Token, in.CAPEM)
	if err != nil {
		return nil, err
	}

	switch action {
	case "namespaces":
		return c.namespaces(ctx)
	case "pods":
		return c.pods(ctx, in)
	case "logs":
		return c.logs(ctx, in)
	case "events":
		return c.events(ctx, in)
	case "get":
		return c.get(ctx, in)
	case "list":
		return c.list(ctx, in)
	case "restart":
		return c.restart(ctx, in)
	case "delete_pod":
		return c.deletePod(ctx, in)
	default:
		return nil, fmt.Errorf("unknown action %q", action)
	}
}

func (c *client) namespaces(ctx context.Context) (any, error) {
	var out struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Status struct {
				Phase string `json:"phase"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := c.getJSON(ctx, "/api/v1/namespaces", nil, &out); err != nil {
		return nil, err
	}
	list := make([]map[string]string, 0, len(out.Items))
	for _, n := range out.Items {
		list = append(list, map[string]string{"name": n.Metadata.Name, "phase": n.Status.Phase})
	}
	return map[string]any{"namespaces": list}, nil
}

// pods returns the compact projection a human reads from `kubectl get pods`:
// phase, readiness, restarts, image, node. Raw pod objects are enormous and
// answer this question worse.
func (c *client) pods(ctx context.Context, in params) (any, error) {
	if strings.TrimSpace(in.Namespace) == "" {
		return nil, fmt.Errorf("namespace is required")
	}
	q := url.Values{}
	if s := strings.TrimSpace(in.Selector); s != "" {
		q.Set("labelSelector", s)
	}
	var out struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				NodeName   string `json:"nodeName"`
				Containers []struct {
					Name  string `json:"name"`
					Image string `json:"image"`
				} `json:"containers"`
			} `json:"spec"`
			Status struct {
				Phase             string `json:"phase"`
				ContainerStatuses []struct {
					Name         string `json:"name"`
					Ready        bool   `json:"ready"`
					RestartCount int    `json:"restartCount"`
					State        map[string]struct {
						Reason  string `json:"reason"`
						Message string `json:"message"`
					} `json:"state"`
				} `json:"containerStatuses"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := c.getJSON(ctx, "/api/v1/namespaces/"+in.Namespace+"/pods", q, &out); err != nil {
		return nil, err
	}

	pods := make([]map[string]any, 0, len(out.Items))
	for _, p := range out.Items {
		images := make([]string, 0, len(p.Spec.Containers))
		for _, ct := range p.Spec.Containers {
			images = append(images, ct.Image)
		}
		restarts := 0
		notReady, reasons := []string{}, []string{}
		for _, cs := range p.Status.ContainerStatuses {
			restarts += cs.RestartCount
			if !cs.Ready {
				notReady = append(notReady, cs.Name)
			}
			for st, v := range cs.State {
				if st != "running" && v.Reason != "" {
					reasons = append(reasons, cs.Name+": "+v.Reason)
				}
			}
		}
		entry := map[string]any{
			"name": p.Metadata.Name, "phase": p.Status.Phase,
			"restarts": restarts, "images": images, "node": p.Spec.NodeName,
		}
		if len(notReady) > 0 {
			entry["not_ready"] = notReady
		}
		if len(reasons) > 0 {
			entry["state"] = reasons
		}
		pods = append(pods, entry)
	}
	return map[string]any{"namespace": in.Namespace, "pods": pods}, nil
}

const (
	defaultTailLines = 100
	maxTailLines     = 2000
)

func (c *client) logs(ctx context.Context, in params) (any, error) {
	if strings.TrimSpace(in.Namespace) == "" || strings.TrimSpace(in.Name) == "" {
		return nil, fmt.Errorf("namespace and name (the pod) are required")
	}
	tail := in.TailLines
	if tail <= 0 {
		tail = defaultTailLines
	}
	if tail > maxTailLines {
		tail = maxTailLines
	}
	q := url.Values{"tailLines": {strconv.Itoa(tail)}}
	if ct := strings.TrimSpace(in.Container); ct != "" {
		q.Set("container", ct)
	}
	if in.Previous {
		q.Set("previous", "true")
	}
	text, err := c.getText(ctx, "/api/v1/namespaces/"+in.Namespace+"/pods/"+in.Name+"/log", q)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"pod": in.Name, "container": in.Container, "previous": in.Previous,
		"tail_lines": tail, "log": text,
	}, nil
}

// events sorts nothing and filters to Warning by default: the Normal ones are
// the cluster narrating itself, and a diagnosis wants the complaints.
func (c *client) events(ctx context.Context, in params) (any, error) {
	if strings.TrimSpace(in.Namespace) == "" {
		return nil, fmt.Errorf("namespace is required")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	q := url.Values{"limit": {strconv.Itoa(limit)}}
	var out struct {
		Items []struct {
			Type           string `json:"type"`
			Reason         string `json:"reason"`
			Message        string `json:"message"`
			Count          int    `json:"count"`
			LastTimestamp  string `json:"lastTimestamp"`
			InvolvedObject struct {
				Kind string `json:"kind"`
				Name string `json:"name"`
			} `json:"involvedObject"`
		} `json:"items"`
	}
	if err := c.getJSON(ctx, "/api/v1/namespaces/"+in.Namespace+"/events", q, &out); err != nil {
		return nil, err
	}
	list := make([]map[string]any, 0, len(out.Items))
	for _, e := range out.Items {
		list = append(list, map[string]any{
			"type": e.Type, "reason": e.Reason, "message": e.Message,
			"count": e.Count, "last": e.LastTimestamp,
			"object": e.InvolvedObject.Kind + "/" + e.InvolvedObject.Name,
		})
	}
	return map[string]any{"namespace": in.Namespace, "events": list}, nil
}

func (c *client) get(ctx context.Context, in params) (any, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, fmt.Errorf("name is required — use list to enumerate a kind")
	}
	path, err := resourcePath(in.Kind, in.Namespace, in.Name)
	if err != nil {
		return nil, err
	}
	var raw json.RawMessage
	if err := c.getJSON(ctx, path, nil, &raw); err != nil {
		return nil, err
	}
	return map[string]any{"kind": in.Kind, "object": declutter(raw)}, nil
}

// list enumerates a kind by name only. The full objects go through get, one at
// a time — a list of twenty decluttered Deployments is still far more than any
// question needs, and "which ones exist" is what the caller is actually asking.
func (c *client) list(ctx context.Context, in params) (any, error) {
	path, err := resourcePath(in.Kind, in.Namespace, "")
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	if s := strings.TrimSpace(in.Selector); s != "" {
		q.Set("labelSelector", s)
	}
	var out struct {
		Items []struct {
			Metadata struct {
				Name              string `json:"name"`
				Namespace         string `json:"namespace"`
				CreationTimestamp string `json:"creationTimestamp"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := c.getJSON(ctx, path, q, &out); err != nil {
		return nil, err
	}
	list := make([]map[string]string, 0, len(out.Items))
	for _, it := range out.Items {
		e := map[string]string{"name": it.Metadata.Name, "created": it.Metadata.CreationTimestamp}
		if it.Metadata.Namespace != "" {
			e["namespace"] = it.Metadata.Namespace
		}
		list = append(list, e)
	}
	return map[string]any{"kind": in.Kind, "items": list}, nil
}

// restart is a rollout restart: the same annotation bump kubectl writes. It is
// safe against GitOps because the annotation lives on the pod template and the
// desired state in git is unchanged — ArgoCD sees no drift in what it manages.
func (c *client) restart(ctx context.Context, in params) (any, error) {
	ns, dep := strings.TrimSpace(in.Namespace), strings.TrimSpace(in.Deployment)
	if ns == "" || dep == "" {
		return nil, fmt.Errorf("namespace and deployment are required")
	}
	// The annotation value has to CHANGE for the pod template hash to change and
	// the pods to be recreated — a fixed marker would be a no-op on the second
	// call. A timestamp is what kubectl writes here too.
	stamp := time.Now().UTC().Format(time.RFC3339)
	patch := []byte(`{"spec":{"template":{"metadata":{"annotations":{"covey.restartedAt":"` + stamp + `"}}}}}`)
	path := "/apis/apps/v1/namespaces/" + ns + "/deployments/" + dep
	if _, err := c.do(ctx, http.MethodPatch, path, nil, patch); err != nil {
		return nil, err
	}
	return map[string]any{"deployment": dep, "namespace": ns, "restarted_at": stamp,
		"note": "rollout restarted — watch it with pods; the desired state in git is unchanged"}, nil
}

func (c *client) deletePod(ctx context.Context, in params) (any, error) {
	ns, name := strings.TrimSpace(in.Namespace), strings.TrimSpace(in.Name)
	if ns == "" || name == "" {
		return nil, fmt.Errorf("namespace and name are required")
	}
	if _, err := c.do(ctx, http.MethodDelete, "/api/v1/namespaces/"+ns+"/pods/"+name, nil, nil); err != nil {
		return nil, err
	}
	return map[string]any{"pod": name, "namespace": ns,
		"note": "deleted — its controller recreates it; if nothing recreates it, it was not managed and that is the finding"}, nil
}

func (System) PromptDoc() string {
	return `Available k8s actions — READING a Kubernetes cluster. This is how you find out what is
   actually running, as opposed to what a ticket claims is running.
   namespaces {} — which namespaces exist. Start here if you do not know the layout.
   pods {"namespace":"…","selector":"app=foo"} — the compact view: phase, restart count, images,
   which containers are not ready and why (CrashLoopBackOff, ImagePullBackOff, OOMKilled …).
   A restart count that keeps climbing is the finding; the reason is in the next two actions.
   logs {"namespace":"…","name":"<pod>","container":"…","tail_lines":200,"previous":true} —
   container logs. "previous":true reads the log of the CRASHED container, which is where the
   stack trace of a CrashLoopBackOff actually is — the running container was started after it.
   Logs contain whatever the application printed, so treat them as you would any production data.
   events {"namespace":"…","limit":50} — the cluster's own complaints: failed scheduling,
   probe failures, image pulls, evictions. Often answers "why is it not starting" in one call.
   list {"kind":"deployments","namespace":"…"} — which objects of a kind exist (names only).
   get {"kind":"deployment","namespace":"…","name":"…"} — one full object, decluttered.
   Readable kinds: pods, services, configmaps, events, persistentvolumeclaims, serviceaccounts,
   namespaces, nodes, deployments, statefulsets, daemonsets, replicasets, jobs, cronjobs,
   ingresses, networkpolicies, roles, rolebindings, clusterroles, clusterrolebindings,
   horizontalpodautoscalers.
   SECRETS ARE NOT READABLE, by design — they would land in your context and in the recording.
   To find out WHICH secret a workload expects, read the workload: the env/volume references name
   it without revealing the value. To see a secret's content, a human reads it.

   THE CLUSTER IS NOT WHERE YOU CHANGE THINGS. Its desired state comes from the infrastructure
   repository and is reconciled continuously (GitOps/ArgoCD). A manifest you applied here would be
   reverted on the next sync, or would drift silently from git — either way the change does not
   hold and the next person cannot see it in the history. So: a change to what is DEPLOYED (image
   tag, replicas, env, resources, Ingress, a new object) is a merge request against the
   infrastructure repository via the gitlab actions, reviewed like any other change. This plugin is
   how you gather the evidence for that merge request, and how you check afterwards that it took
   effect.
   Two exceptions exist because they are operations, not state — and only if your ACCESS.md grants
   the "write" scope:
   restart {"namespace":"…","deployment":"…"} — rollout restart (a stuck cache, a config the
   application only reads at boot after a ConfigMap changed). Changes nothing git manages.
   delete_pod {"namespace":"…","name":"…"} — remove one wedged pod and let its controller
   recreate it. If nothing recreates it, the pod was unmanaged, and THAT is the finding to report.
   Anything beyond these two — scaling, applying, editing, exec into a container, port-forward —
   is deliberately absent. If you find yourself wanting one, what you actually want is either a
   merge request or a human.`
}

// PromptDocForScopes keeps the model's action contract aligned with ACCESS.md.
// Kubernetes RBAC remains the hard authorization boundary, but an agent should
// not be invited to call operations its Covey role does not carry.
func (System) PromptDocForScopes(scopes []string) string {
	if len(scopes) == 0 {
		return (System{}).PromptDoc()
	}
	granted := make(map[string]bool, len(scopes))
	for _, scope := range scopes {
		granted[strings.TrimSpace(scope)] = true
	}

	doc := (System{}).PromptDoc()
	if !granted["logs"] {
		start := strings.Index(doc, `   logs {`)
		end := strings.Index(doc, `   events {`)
		if start >= 0 && end > start {
			doc = doc[:start] + doc[end:]
		}
		doc = strings.Replace(doc,
			"A restart count that keeps climbing is the finding; the reason is in the next two actions.",
			"A restart count that keeps climbing is the finding; events explain many causes.", 1)
	}
	if !granted["write"] {
		if start := strings.Index(doc, "\n   Two exceptions exist because"); start >= 0 {
			doc = strings.TrimRight(doc[:start], " \n")
		}
	}
	return doc
}
