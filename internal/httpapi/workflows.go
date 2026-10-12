package httpapi

import (
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"covey/internal/identity"
	"covey/internal/workflow"
)

// Workflows (#564): an organisation's definitions and the runs going through
// them. Everybody in the organisation may read them — a workflow says who
// works on what after whom, the same kind of thing the org chart says.
// Writing a definition and starting a run by hand belong to those who
// manage agents; deciding an approval to the person it asks, or a manager.

func (s *Server) workflowsOn(w http.ResponseWriter) bool {
	if s.Workflows == nil {
		writeErr(w, http.StatusServiceUnavailable, "workflows are not configured on this instance")
		return false
	}
	return true
}

type problemView struct {
	Step string `json:"step,omitempty"`
	Code string `json:"code"`
	Text string `json:"text"`
}

// stepView is a step with every field it has, durations in seconds — the
// interface builds its canvas from it and needs no second parser.
type stepView struct {
	ID          string            `json:"id"`
	Kind        string            `json:"kind"`
	Title       string            `json:"title,omitempty"`
	Next        map[string]string `json:"next,omitempty"`
	Role        string            `json:"role,omitempty"`
	Pick        string            `json:"pick,omitempty"`
	Sticky      bool              `json:"sticky,omitempty"`
	MaxParallel int               `json:"max_parallel,omitempty"`
	NotSameAs   string            `json:"not_same_as,omitempty"`
	Optional    bool              `json:"optional,omitempty"`
	Task        string            `json:"task,omitempty"`
	Result      []string          `json:"result,omitempty"`
	QuestionsTo string            `json:"questions_to,omitempty"`
	Limit       int64             `json:"limit,omitempty"`
	On          string            `json:"on,omitempty"`
	MaxRounds   int               `json:"max_rounds,omitempty"`
	System      string            `json:"system,omitempty"`
	Do          string            `json:"do,omitempty"`
	Target      string            `json:"target,omitempty"`
	Value       string            `json:"value,omitempty"`
	Comment     string            `json:"comment,omitempty"`
	Who         string            `json:"who,omitempty"`
	Ask         string            `json:"ask,omitempty"`
	Options     []string          `json:"options,omitempty"`
	Wait        int64             `json:"wait,omitempty"`
	Until       string            `json:"until,omitempty"`
	Max         int64             `json:"max,omitempty"`
	To          string            `json:"to,omitempty"`
	Via         string            `json:"via,omitempty"`
	Text        string            `json:"text,omitempty"`
}

type runCounts struct {
	Open    int `json:"open"`
	Working int `json:"working"`
	Queued  int `json:"queued"`
	Waiting int `json:"waiting"`
	Stuck   int `json:"stuck"`
}

type workflowView struct {
	Slug      string              `json:"slug"`
	Name      string              `json:"name"`
	Version   int                 `json:"version"`
	Mode      string              `json:"mode"`
	Scope     string              `json:"scope"`
	Owner     string              `json:"owner"`
	Roles     map[string][]string `json:"roles"`
	UpdatedAt time.Time           `json:"updated_at"`
	Runs      runCounts           `json:"runs"`
	Steps     []stepView          `json:"steps,omitempty"`
	Source    string              `json:"source,omitempty"`
}

type runView struct {
	ID   string `json:"id"`
	Case string `json:"case"`
	// Title is the case's title as the trigger passed it, for lists.
	Title     string                       `json:"title,omitempty"`
	Step      string                       `json:"step"`
	State     string                       `json:"state"`
	Assignee  string                       `json:"assignee,omitempty"`
	Since     time.Time                    `json:"since"`
	Stuck     bool                         `json:"stuck,omitempty"`
	Unstaffed bool                         `json:"unstaffed,omitempty"`
	Loops     map[string]int               `json:"loops,omitempty"`
	Had       map[string]string            `json:"had,omitempty"`
	Vars      map[string]map[string]string `json:"vars,omitempty"`
	Log       []workflow.Entry             `json:"log,omitempty"`
}

func viewOf(st workflow.Stored, runs []workflow.Run, full bool) workflowView {
	v := workflowView{
		Slug: st.Slug, Name: st.Def.Name, Version: st.Version, Mode: string(st.Def.Mode),
		Scope: st.Def.Scope, Owner: st.Def.Owner, Roles: st.Def.Roles, UpdatedAt: st.UpdatedAt,
	}
	if v.Name == "" {
		v.Name = st.Slug
	}
	for _, r := range runs {
		if r.State == workflow.StateDone || r.State == workflow.StateFailed {
			continue
		}
		v.Runs.Open++
		switch r.State {
		case workflow.StateWorking:
			v.Runs.Working++
		case workflow.StateQueued:
			v.Runs.Queued++
		case workflow.StateWaiting:
			v.Runs.Waiting++
		}
		if r.Stuck {
			v.Runs.Stuck++
		}
	}
	if full {
		v.Source = st.Source
		for _, id := range st.Def.Order {
			s := st.Def.Steps[id]
			v.Steps = append(v.Steps, stepView{
				ID: id, Kind: string(s.Kind), Title: s.Title, Next: s.Next,
				Role: s.Role, Pick: string(s.Pick), Sticky: s.Sticky, MaxParallel: s.MaxParallel, NotSameAs: s.NotSameAs,
				Optional: s.Optional, Task: s.Task, Result: s.Result, QuestionsTo: s.QuestionsTo, Limit: secs(s.Limit),
				On: s.On, MaxRounds: s.MaxRounds,
				System: s.System, Do: s.Do, Target: s.Target, Value: s.Value, Comment: s.Comment,
				Who: s.Who, Ask: s.Ask, Options: s.Options, Wait: secs(s.Wait),
				Until: s.Until, Max: secs(s.Max), To: s.To, Via: s.Via, Text: s.Text,
			})
		}
	}
	return v
}

func runViewOf(r workflow.Run, full bool) runView {
	v := runView{ID: r.ID, Case: r.Case, Title: r.Vars["case"]["title"], Step: r.Step, State: string(r.State), Assignee: r.Assignee,
		Since: r.Since, Stuck: r.Stuck, Unstaffed: r.Unstaffed}
	if full {
		v.Loops, v.Had, v.Vars, v.Log = r.Loops, r.Had, r.Vars, r.Log
	}
	return v
}

func secs(d time.Duration) int64 { return int64(d / time.Second) }

// handleListWorkflows — GET /api/v1/workflows: the organisation's workflows
// with how many runs stand where.
func (s *Server) handleListWorkflows(w http.ResponseWriter, r *http.Request) {
	if !s.workflowsOn(w) {
		return
	}
	orgID := principalFrom(r).OrgID
	list, err := s.Workflows.Store.List(r.Context(), orgID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]workflowView, 0, len(list))
	for _, st := range list {
		out = append(out, viewOf(st, s.Workflows.Runs(orgID, st.Slug), false))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleGetWorkflow — GET /api/v1/workflows/{slug}: the current version, as
// text and as steps.
func (s *Server) handleGetWorkflow(w http.ResponseWriter, r *http.Request) {
	if !s.workflowsOn(w) {
		return
	}
	orgID := principalFrom(r).OrgID
	st, err := s.Workflows.Store.Get(r.Context(), orgID, r.PathValue("slug"))
	if errors.Is(err, workflow.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "workflow not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, viewOf(st, s.Workflows.Runs(orgID, st.Slug), true))
}

// handleCheckWorkflow — POST /api/v1/workflows/check {source}: what keeps a
// definition from running, without saving it — for the editor, while typing.
func (s *Server) handleCheckWorkflow(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Source string `json:"source"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"problems": problemsOf(in.Source)})
}

func problemsOf(source string) []problemView {
	out := []problemView{}
	d, err := workflow.Parse([]byte(source))
	if err != nil {
		return append(out, problemView{Code: "unreadable", Text: err.Error()})
	}
	for _, p := range workflow.Check(d) {
		out = append(out, problemView{Step: p.Step, Code: p.Code, Text: p.Text})
	}
	return out
}

// handlePutWorkflow — PUT /api/v1/workflows/{slug} {source}: the next
// version. A definition with problems is refused with them (422); a saved
// one is taken up by the running engine at once.
func (s *Server) handlePutWorkflow(w http.ResponseWriter, r *http.Request) {
	if !s.workflowsOn(w) {
		return
	}
	var in struct {
		Source string `json:"source"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	p := principalFrom(r)
	by := p.ID
	st, err := s.Workflows.Store.Save(r.Context(), p.OrgID, r.PathValue("slug"), in.Source, &by)
	var invalid *workflow.InvalidError
	if errors.As(err, &invalid) {
		problems := make([]problemView, 0, len(invalid.Problems))
		for _, pr := range invalid.Problems {
			problems = append(problems, problemView{Step: pr.Step, Code: pr.Code, Text: pr.Text})
		}
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "the workflow cannot run as written", "problems": problems})
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp := map[string]any{"slug": st.Slug, "version": st.Version}
	// Saved but not taken up: runs stand at a step the new version lacks.
	// The old version keeps running them; the caller hears why.
	if err := s.Workflows.Reload(r.Context(), p.OrgID, st.Slug); err != nil {
		resp["warning"] = "saved, but the running workflow keeps its previous version: " + err.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleListRuns — GET /api/v1/workflows/{slug}/runs[?open=1]: the runs,
// what needs someone first: stuck, then waiting, then the oldest.
func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	if !s.workflowsOn(w) {
		return
	}
	orgID := principalFrom(r).OrgID
	if _, _, ok := s.Workflows.Engine(orgID, r.PathValue("slug")); !ok {
		writeErr(w, http.StatusNotFound, "workflow not found")
		return
	}
	openOnly := r.URL.Query().Get("open") == "1"
	runs := s.Workflows.Runs(orgID, r.PathValue("slug"))
	out := make([]runView, 0, len(runs))
	for _, run := range runs {
		if openOnly && (run.State == workflow.StateDone || run.State == workflow.StateFailed) {
			continue
		}
		out = append(out, runViewOf(run, false))
	}
	rank := func(v runView) int {
		switch {
		case v.Stuck || v.Unstaffed:
			return 0
		case v.State == string(workflow.StateWaiting):
			return 1
		case v.State == string(workflow.StateDone) || v.State == string(workflow.StateFailed):
			return 3
		}
		return 2
	}
	sort.SliceStable(out, func(i, j int) bool {
		if rank(out[i]) != rank(out[j]) {
			return rank(out[i]) < rank(out[j])
		}
		return out[i].Since.Before(out[j].Since)
	})
	writeJSON(w, http.StatusOK, out)
}

// handleGetRun — GET /api/v1/workflows/{slug}/runs/{id}: one run with what
// each step reported and its log.
func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	if !s.workflowsOn(w) {
		return
	}
	eng, _, ok := s.Workflows.Engine(principalFrom(r).OrgID, r.PathValue("slug"))
	if !ok {
		writeErr(w, http.StatusNotFound, "workflow not found")
		return
	}
	run, ok := eng.Run(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}
	writeJSON(w, http.StatusOK, runViewOf(run, true))
}

// handleStartRun — POST /api/v1/workflows/{slug}/runs {case, vars}: start a
// run by hand. A case with an open run gets that run back (200), a new one
// is 201.
func (s *Server) handleStartRun(w http.ResponseWriter, r *http.Request) {
	if !s.workflowsOn(w) {
		return
	}
	var in struct {
		Case string            `json:"case"`
		Vars map[string]string `json:"vars"`
	}
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Case) == "" {
		writeErr(w, http.StatusBadRequest, "a run needs the case it is about")
		return
	}
	eng, _, ok := s.Workflows.Engine(principalFrom(r).OrgID, r.PathValue("slug"))
	if !ok {
		writeErr(w, http.StatusNotFound, "workflow not found")
		return
	}
	caseRef := strings.TrimSpace(in.Case)
	existed := false
	for _, r := range eng.Runs() {
		if r.Case == caseRef && r.State != workflow.StateDone && r.State != workflow.StateFailed {
			existed = true
		}
	}
	run, err := eng.Start(caseRef, in.Vars)
	if errors.Is(err, workflow.ErrOff) {
		writeErr(w, http.StatusConflict, "the workflow is off")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	status := http.StatusCreated
	if existed {
		status = http.StatusOK
	}
	writeJSON(w, status, runViewOf(run, true))
}

// handleDecideRun — POST /api/v1/workflows/{slug}/runs/{id}/decide {option}.
// The approval names who decides; that person may, and so may whoever
// manages agents. Nobody else answers a question put to someone.
func (s *Server) handleDecideRun(w http.ResponseWriter, r *http.Request) {
	if !s.workflowsOn(w) {
		return
	}
	var in struct {
		Option string `json:"option"`
	}
	if err := readJSON(r, &in); err != nil || in.Option == "" {
		writeErr(w, http.StatusBadRequest, "a decision needs an option")
		return
	}
	p := principalFrom(r)
	eng, st, ok := s.Workflows.Engine(p.OrgID, r.PathValue("slug"))
	if !ok {
		writeErr(w, http.StatusNotFound, "workflow not found")
		return
	}
	run, ok := eng.Run(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}
	step := st.Def.Steps[run.Step]
	if step == nil || step.Kind != workflow.KindApproval {
		writeErr(w, http.StatusConflict, "the run does not wait for a decision")
		return
	}
	if !mayDecide(p, step.Who) {
		writeErr(w, http.StatusForbidden, "this decision is asked of "+step.Who)
		return
	}
	switch err := eng.Decide(run.ID, in.Option); {
	case errors.Is(err, workflow.ErrUnknownExit):
		writeErr(w, http.StatusBadRequest, "options: "+strings.Join(step.Options, ", "))
		return
	case errors.Is(err, workflow.ErrWrongState):
		writeErr(w, http.StatusConflict, "the run does not wait for a decision")
		return
	case err != nil:
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	run, _ = eng.Run(run.ID)
	writeJSON(w, http.StatusOK, runViewOf(run, true))
}

// waitingView is an approval that names the person asking.
type waitingView struct {
	Workflow string    `json:"workflow"`
	Name     string    `json:"name"`
	Run      string    `json:"run"`
	Case     string    `json:"case"`
	Title    string    `json:"title,omitempty"`
	Step     string    `json:"step"`
	Question string    `json:"question,omitempty"`
	Options  []string  `json:"options"`
	Since    time.Time `json:"since"`
}

// handleMyApprovals — GET /api/v1/workflows/mine: the approvals across all
// workflows that name the person asking. A manager may decide any of them,
// but these are the ones put to them.
func (s *Server) handleMyApprovals(w http.ResponseWriter, r *http.Request) {
	if !s.workflowsOn(w) {
		return
	}
	p := principalFrom(r)
	list, err := s.Workflows.Store.List(r.Context(), p.OrgID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := []waitingView{}
	for _, st := range list {
		for _, run := range s.Workflows.Runs(p.OrgID, st.Slug) {
			step := st.Def.Steps[run.Step]
			if run.State != workflow.StateWaiting || step == nil || step.Kind != workflow.KindApproval || !named(p, step.Who) {
				continue
			}
			name := st.Def.Name
			if name == "" {
				name = st.Slug
			}
			out = append(out, waitingView{Workflow: st.Slug, Name: name, Run: run.ID, Case: run.Case,
				Title: run.Vars["case"]["title"], Step: step.Title, Question: render(step.Ask, run.Vars), Options: step.Options, Since: run.Since})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Since.Before(out[j].Since) })
	writeJSON(w, http.StatusOK, out)
}

// render fills a question's {{step.field}} the way the engine did when it
// asked — the stored run has the values.
func render(text string, vars map[string]map[string]string) string {
	for step, fields := range vars {
		for k, v := range fields {
			text = strings.ReplaceAll(text, "{{"+step+"."+k+"}}", v)
		}
	}
	return text
}

// mayDecide: the person named in the approval — by e-mail, by the part of it
// before the @, or by display name — or a manager.
func mayDecide(p identity.Principal, who string) bool {
	for _, role := range manageRoles {
		if p.Role == role {
			return true
		}
	}
	return named(p, who)
}

// named: the approval names this person — by e-mail, by the part of it
// before the @, or by display name.
func named(p identity.Principal, who string) bool {
	who = strings.ToLower(strings.TrimSpace(who))
	if who == "" {
		return false
	}
	email := strings.ToLower(p.Email)
	local, _, _ := strings.Cut(email, "@")
	return who == email || who == local || who == strings.ToLower(strings.TrimSpace(p.DisplayName))
}

// handleRunEvent — POST /api/v1/workflows/{slug}/runs/{id}/events {name}:
// deliver what a wait step waits for, by hand, until target systems send
// their events themselves.
func (s *Server) handleRunEvent(w http.ResponseWriter, r *http.Request) {
	if !s.workflowsOn(w) {
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &in); err != nil || in.Name == "" {
		writeErr(w, http.StatusBadRequest, "an event needs a name")
		return
	}
	eng, _, ok := s.Workflows.Engine(principalFrom(r).OrgID, r.PathValue("slug"))
	if !ok {
		writeErr(w, http.StatusNotFound, "workflow not found")
		return
	}
	switch err := eng.Event(r.PathValue("id"), in.Name); {
	case errors.Is(err, workflow.ErrUnknownRun):
		writeErr(w, http.StatusNotFound, "run not found")
		return
	case errors.Is(err, workflow.ErrWrongState):
		writeErr(w, http.StatusConflict, "the run does not wait for "+in.Name)
		return
	case err != nil:
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	run, _ := eng.Run(r.PathValue("id"))
	writeJSON(w, http.StatusOK, runViewOf(run, true))
}
