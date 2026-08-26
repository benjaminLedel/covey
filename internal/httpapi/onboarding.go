package httpapi

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// Onboarding: the first steps towards the first working agent — not a clicked
// tour, but a checklist that reads the **actual state of the organisation**.
//
// The difference is the point: a tour tells you what to do, and keeps telling
// you long after you have done it — or after the UI has changed and the tour
// points at a button that no longer exists. This list asks the database
// instead: is a runtime credential present? Does an agent exist? Does it have
// a SOUL.md? Was a task ever queued? Did a run ever happen? Every tick is
// therefore a fact, not a claim, and the list disappears by itself once it has
// nothing left to say.
//
// The steps are the same as in the help ("Getting started") — one order, not
// two.

// onboardingStep is a single step plus its done status. The text lives in the
// UI (i18n), here we only record what is true.
type onboardingStep struct {
	Key  string `json:"key"`
	Done bool   `json:"done"`
}

type onboardingState struct {
	Steps []onboardingStep `json:"steps"`
	// Done is true once every step is done — the UI then hides the list
	// without having to count through it.
	Done bool `json:"done"`
	// DataPlane carries what stands between the platform and a running
	// sandbox. Deliberately NOT a sixth step: no click in this interface fixes
	// a missing Docker socket, and a step nobody can tick off would keep the
	// list on screen forever. It is a warning, and it appears whether or not
	// the steps are done.
	DataPlane *dataPlaneState `json:"data_plane,omitempty"`
}

type dataPlaneState struct {
	Ready    bool     `json:"ready"`
	Problems []string `json:"problems,omitempty"`
}

// cachedCheck holds the data plane's answer for a short while. The check runs
// two docker CLI calls, and the first steps are fetched with every load of the
// agent overview — asking every time would put a process spawn behind a view
// that is polled. Half a minute is short enough that whoever builds the image
// in the terminal sees the tick shortly afterwards, without having to know
// they should reload.
//
// Once filled, it is refreshed BESIDE the request and no longer inside it. The
// check asks the hosts, and a host answers out of the read loop a sandbox start
// occupies — so when the half minute ran out while a runner was pulling an
// image, whoever loaded the page next waited for that pull. Everyone else too:
// the lock was held for the whole time.
type cachedCheck struct {
	mu       sync.Mutex
	at       time.Time
	problems []string
	filled   bool
	running  bool
}

const dataPlaneCheckTTL = 30 * time.Second

// dataPlaneCheckWait bounds one refresh. Nobody is waiting on it — but a check
// that never ends would keep the next one from ever starting.
const dataPlaneCheckWait = 2 * time.Minute

func (s *Server) dataPlaneProblems(ctx context.Context) []string {
	s.dataPlane.mu.Lock()
	defer s.dataPlane.mu.Unlock()
	// The very first one is asked while somebody waits: there is nothing to
	// show yet, and "everything fine" is the wrong thing to say when the answer
	// is simply not there. It happens once per process, and an idle runner
	// answers it in milliseconds.
	if !s.dataPlane.filled {
		s.dataPlane.problems = s.DataPlane.Check(ctx)
		s.dataPlane.at = time.Now()
		s.dataPlane.filled = true
		return s.dataPlane.problems
	}
	if time.Since(s.dataPlane.at) >= dataPlaneCheckTTL && !s.dataPlane.running {
		s.dataPlane.running = true
		// Detached from the request: whoever asked has already been served with
		// the previous answer, and the connection they came in on is gone
		// before this returns.
		go s.refreshDataPlane(context.WithoutCancel(ctx))
	}
	return s.dataPlane.problems
}

func (s *Server) refreshDataPlane(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, dataPlaneCheckWait)
	defer cancel()
	problems := s.DataPlane.Check(ctx)
	s.dataPlane.mu.Lock()
	s.dataPlane.problems = problems
	s.dataPlane.at = time.Now()
	s.dataPlane.running = false
	s.dataPlane.mu.Unlock()
}

// runtimeCredentialKeys are the secret names the Claude Code runtime starts
// with. Without one of them every task fails — which is why this is the first
// step and not creating an agent.
var runtimeCredentialKeys = []string{"anthropic_api_key", "claude_code_oauth_token"}

// The single query deliberately bypasses the store boundary: it asks five
// domains at once (secrets, agents, config, backlog, recording) and cares
// about none of them in substance — only whether anything exists at all. Split
// across five store calls it would be five round trips to the database for a
// view that runs along with every load of the agent overview; parked inside
// one store it would not belong there. This is the job of a BFF: build one
// view out of many sources.
func (s *Server) handleOnboarding(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)

	var cred, agent, config, task, run bool
	err := s.Pool.QueryRow(r.Context(), `SELECT
		EXISTS (SELECT 1 FROM secrets WHERE org_id=$1 AND key = ANY($2)),
		EXISTS (SELECT 1 FROM agents WHERE org_id=$1),
		EXISTS (SELECT 1 FROM agent_config_versions v JOIN agents a ON a.id = v.agent_id
		        WHERE a.org_id=$1 AND coalesce(v.files->>'SOUL.md', '') <> ''),
		EXISTS (SELECT 1 FROM backlog_tasks WHERE org_id=$1),
		EXISTS (SELECT 1 FROM recording_events WHERE org_id=$1)`,
		p.OrgID, runtimeCredentialKeys).Scan(&cred, &agent, &config, &task, &run)
	if err != nil {
		mapErr(w, err)
		return
	}

	state := onboardingState{Steps: []onboardingStep{
		{Key: "credential", Done: cred},
		{Key: "agent", Done: agent},
		{Key: "config", Done: config},
		{Key: "task", Done: task},
		{Key: "run", Done: run},
	}}
	state.Done = true
	for _, st := range state.Steps {
		if !st.Done {
			state.Done = false
			break
		}
	}
	if s.DataPlane != nil {
		problems := s.dataPlaneProblems(r.Context())
		state.DataPlane = &dataPlaneState{Ready: len(problems) == 0, Problems: problems}
	}
	writeJSON(w, http.StatusOK, state)
}
