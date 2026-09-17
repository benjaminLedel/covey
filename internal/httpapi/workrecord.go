package httpapi

// The work record on the employee profile (spec/21).
//
// It is built here for PEOPLE first and only after that for the
// covey Doctor, which reads it over covey/work_record. That order is no
// matter of convenience: what a person cannot read on a page, they cannot
// verify in an agent's answer either — and the answer to "why is this
// agent not delivering" is the first one somebody must be able to
// recompute.
//
// WHO MAY READ IT is decided here and not inherited. spec/17 says
// performance data per agent is more sensitive than a cost total; the record
// therefore follows the RECORDINGS, not the cost figures: org_admin,
// security, the agent's owner and the auditor may read. Controlling
// is missing. "whoever may see the bill may also see this" would be the
// answer that makes the feature unusable in any company with a works council.

import (
	"net/http"

	"covey/internal/identity"
	"covey/internal/workrecord"
)

// workRecordRoles: the same boundary for the record and for the metrics of a
// single agent — reading them from the record is the same act as reading
// the record (spec/21, and with it the open question from spec/17).
func workRecordRoles() []string {
	return []string{identity.RoleOrgAdmin, identity.RoleAgentOwner,
		identity.RoleSecurity, identity.RoleAuditor}
}

func (s *Server) handleWorkRecord(w http.ResponseWriter, r *http.Request) {
	agent := agentFrom(r)
	_, since := costWindow(r)
	rec, err := s.workRecords().Build(r.Context(), agent.ID, since)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// workRecords builds the collector anew on every call — it holds no state,
// only pointers to the stores the server carries anyway.
func (s *Server) workRecords() *workrecord.Builder {
	return &workrecord.Builder{
		Pool: s.Pool, Registry: s.Registry, Obs: s.Obs, Skills: s.lintSkills(),
	}
}

// handleAgentReviews is the history on the employee profile: what the
// operation has written about this colleague, dated, newest first.
//
// The same role boundary as the work record — a review is the record in
// words. And deliberately a READ-only path for people: there is no action
// with which an agent retrieves reviews. An open suggestion and an assessment
// reach the assessed agent by no route, and that stays so because
// the route is missing, not because a rule forbids it.
func (s *Server) handleAgentReviews(w http.ResponseWriter, r *http.Request) {
	agent := agentFrom(r)
	list, err := s.Registry.Reviews(r.Context(), agent.ID, 20)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}
