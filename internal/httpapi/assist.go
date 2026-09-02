package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/llm"
)

// AI assistant for adapting agents ("config copilot", FR-001).
//
// The idea: as soon as an org-wide Claude credential is configured in the
// organization (the same one that feeds the agent runtime), the control plane
// can reuse it server-side to support a human in phrasing the agent config
// (SOUL.md & co.) — an LLM that helps configuring the other LLMs.
//
// Guard rails: the credential never leaves the control plane (no browser, no
// sandbox). The assistant commits nothing itself — it delivers proposals that
// the human reviews as a diff and saves deliberately (config-as-code stays
// intact).

// assistMaxTokens caps the response; non-streamed, so stay below the SDK
// timeout range (~16k). The model itself is not named here any more — the
// copilot asks for the best tier and the provider knows which one that is
// (internal/llm).
const assistMaxTokens = 8000

// anthropicBaseURL is shared from credcheck.go (a variable so tests can slip
// in an httptest server).

// assistEditableFiles are the config files the assistant may propose — all
// files managed by the editor. ACCESS.md and EGRESS.md are structured text
// views onto the tools/egress stores; saving them runs through a write-through
// (may require the security role). TOOLS.md is legacy and has been merged into
// ACCESS.md — hence deliberately not included.
var assistEditableFiles = map[string]bool{
	"SOUL.md":         true,
	"CAPABILITIES.md": true,
	"PLAYBOOKS.md":    true,
	"ORG.md":          true,
	"HEARTBEAT.md":    true,
	"ACCESS.md":       true,
	"EGRESS.md":       true,
}

// assistFileOrder is the order in which files are shown to the model as
// context.
var assistFileOrder = []string{
	"SOUL.md", "CAPABILITIES.md", "PLAYBOOKS.md", "ORG.md",
	"HEARTBEAT.md", "ACCESS.md", "EGRESS.md",
}

// assistMessage is one turn in the dialogue human↔assistant. Content is a
// plain string — every provider accepts that as a single text block.
type assistMessage = llm.Message

// assistProposal is a proposed new content for a config file.
type assistProposal struct {
	File    string `json:"file"`
	Content string `json:"content"`
}

// assistResponse is the answer to the UI: prose message plus optional file
// proposals the human can accept as a diff.
type assistResponse struct {
	Reply     string           `json:"reply"`
	Proposals []assistProposal `json:"proposals"`
}

// resolveOrgLLM finds the provider the control plane may use for this
// organisation (order and key names live in internal/llm, so that copilot,
// dream and setup see the same).
func (s *Server) resolveOrgLLM(ctx context.Context, orgID uuid.UUID) (llm.Provider, error) {
	return llm.Resolve(ctx, s.Secrets, orgID)
}

// handleAssistStatus tells the UI whether the config copilot is available —
// that is, whether a control-plane credential can be resolved. Without one the
// feature is not offered in the UI at all (no dead UI, no costs).
func (s *Server) handleAssistStatus(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	_, err := s.resolveOrgLLM(r.Context(), p.OrgID)
	writeJSON(w, http.StatusOK, map[string]bool{"available": err == nil})
}

// handleConfigAssist is the dialogue endpoint: it takes the conversation
// history plus the current (possibly unsaved) config draft, assembles the
// platform context, asks Claude and returns message + file proposals.
func (s *Server) handleConfigAssist(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	p := principalFrom(r)
	provider, err := s.resolveOrgLLM(r.Context(), p.OrgID)
	if err != nil {
		writeErr(w, http.StatusPreconditionFailed,
			"no control-plane LLM credential configured — the AI assistant is not available")
		return
	}
	var in struct {
		Messages []assistMessage   `json:"messages"`
		Files    map[string]string `json:"files"`
	}
	if err := readJSON(r, &in); err != nil || len(in.Messages) == 0 {
		writeErr(w, http.StatusBadRequest, "messages is missing")
		return
	}
	a, err := s.Registry.Get(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}
	system := s.buildAssistSystem(r.Context(), p.OrgID, a, in.Files)

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	raw, err := provider.Complete(ctx, llm.Request{
		Tier: llm.TierBest, MaxTokens: assistMaxTokens,
		System: system, Messages: in.Messages,
	})
	if err != nil {
		s.Log.Error("config-assist", "agent", id, "err", err)
		writeErr(w, http.StatusBadGateway, "AI assistant unreachable: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, parseAssistReply(raw))
}

// buildAssistSystem builds the system prompt: role, platform protocol,
// connected target systems, applicable guard rails, current config state and
// the JSON response contract. That way the assistant only proposes behaviour
// the platform actually permits.
func (s *Server) buildAssistSystem(ctx context.Context, orgID uuid.UUID, a agents.Agent, files map[string]string) string {
	var b strings.Builder
	b.WriteString(`You are the config assistant of the covey platform: you help a human (the agent owner) write and revise the configuration of an AI agent. Answer in English, matter-of-factly and precisely.

An agent on covey is configured through markdown files (config-as-code). You may propose all of these files:
- SOUL.md — character, role, tone, basic attitude of the agent (the heart of it).
- CAPABILITIES.md — concrete capabilities and what the agent may/should do.
- PLAYBOOKS.md — recurring procedures step by step.
- ORG.md — embedding into the organization, whom to escalate to.
- HEARTBEAT.md — periodic "what is up?" impulses (optional).
- ACCESS.md — which target systems the agent may use (broker scopes, tool allowlist). Structured text view onto the tools store; keep exactly the format of the current state (comment lines with '#', then entries). Changes take effect on the broker accesses and may require the security role when saving.
- EGRESS.md — which hosts the agent may reach outbound (templates + own hosts). Structured text view onto the egress store; likewise keep exactly the existing format; saving may require the security role.

Your task: draft concrete, well-phrased config content from the human's description, or revise existing content in a targeted way. Only propose behaviour the platform actually permits (do not invent target systems/actions that are not listed below; respect the applicable guard rails). For ACCESS.md/EGRESS.md only use systems/templates that really exist (see the context below).

`)
	// Whose house this is. Until now the assistant knew the agent, its target
	// systems and its guard rails — but not the company it writes for, which is
	// the one thing every one of its proposals depends on (spec/20).
	if o, err := s.Org.GetOrg(ctx, orgID); err == nil {
		b.WriteString("## The organisation\n")
		b.WriteString("Name: " + o.Name + "\n")
		if d := strings.TrimSpace(o.Description); d != "" {
			b.WriteString(d + "\n")
		}
		b.WriteString("\n")
	}

	b.WriteString("## Platform protocol (applies to every agent)\n")
	b.WriteString(agents.ProtocolInstructions)
	b.WriteString("\n\n")

	b.WriteString("## This agent\n")
	b.WriteString("Display name: " + a.DisplayName + "\n")
	if a.JobTitle != "" {
		b.WriteString("Job title: " + a.JobTitle + "\n")
	}
	b.WriteString("Runtime: " + a.Runtime + "\n\n")

	if docs, err := s.Targets.EnabledDocsForAgent(ctx, orgID, a.ID); err == nil && len(docs) > 0 {
		b.WriteString("## Connected target systems (available actions)\n")
		for _, d := range docs {
			b.WriteString(d)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	} else {
		b.WriteString("## Connected target systems\nNone — the agent currently has no target systems connected.\n\n")
	}

	if rules, err := s.Rails.List(ctx, orgID); err == nil {
		var active []string
		for _, ru := range rules {
			if !ru.Enabled {
				continue
			}
			if ru.ScopeLevel == "agent" && (ru.AgentID == nil || *ru.AgentID != a.ID) {
				continue // agent-scoped rule of a different agent
			}
			active = append(active, "- "+ru.RuleType+": "+ru.Pattern)
		}
		if len(active) > 0 {
			b.WriteString("## Applicable guard rails (platform-enforced)\n")
			b.WriteString(strings.Join(active, "\n"))
			b.WriteString("\n\n")
		}
	}

	b.WriteString("## Current config state\n")
	wroteAny := false
	for _, name := range assistFileOrder {
		content := strings.TrimSpace(files[name])
		if content == "" {
			continue
		}
		wroteAny = true
		b.WriteString("### " + name + "\n```\n" + content + "\n```\n")
	}
	if !wroteAny {
		b.WriteString("All config files are still empty — the agent is being set up right now.\n")
	}
	b.WriteString("\n")

	b.WriteString(`## Response format (IMPORTANT)
Answer EXCLUSIVELY with a single JSON object, without a markdown code fence, without text before or after it:
{"reply": "<your message to the human>", "proposals": [{"file": "SOUL.md", "content": "<complete new file content>"}]}

Rules:
- "reply" is your short prose answer (what you propose and why).
- "proposals" contains one entry per file you want to change, with the COMPLETE new content (not just a diff). Empty array if you only explain/ask back and do not change anything (yet).
- Allowed file names: SOUL.md, CAPABILITIES.md, PLAYBOOKS.md, ORG.md, HEARTBEAT.md, ACCESS.md, EGRESS.md.
- For ACCESS.md/EGRESS.md keep the exact format of the current state (comment lines, entries) and only use systems/templates that really exist.
- Do not propose anything the platform cannot do.`)
	return b.String()
}

// parseAssistReply extracts the JSON object from the model's answer. If
// parsing fails (the model delivered prose after all), the whole text is
// returned as the message without proposals — never a hard error.
func parseAssistReply(raw string) assistResponse {
	txt := strings.TrimSpace(raw)
	// Strip an eventual ```json … ``` fence.
	if strings.HasPrefix(txt, "```") {
		if i := strings.IndexByte(txt, '\n'); i >= 0 {
			txt = txt[i+1:]
		}
		txt = strings.TrimSuffix(strings.TrimSpace(txt), "```")
		txt = strings.TrimSpace(txt)
	}
	// Narrow down to the first '{' … last '}' (in case there is text around it).
	if i := strings.IndexByte(txt, '{'); i > 0 {
		txt = txt[i:]
	}
	if j := strings.LastIndexByte(txt, '}'); j >= 0 && j < len(txt)-1 {
		txt = txt[:j+1]
	}

	var out assistResponse
	if err := json.Unmarshal([]byte(txt), &out); err != nil || strings.TrimSpace(out.Reply) == "" {
		return assistResponse{Reply: strings.TrimSpace(raw), Proposals: nil}
	}
	// Filter the proposals down to the editable files.
	filtered := out.Proposals[:0]
	for _, pr := range out.Proposals {
		if assistEditableFiles[pr.File] {
			filtered = append(filtered, pr)
		}
	}
	out.Proposals = filtered
	return out
}
