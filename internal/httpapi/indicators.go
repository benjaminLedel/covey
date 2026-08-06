package httpapi

import (
	"net/http"
	"sort"
	"time"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/observability"
)

// The price list: what the workforce delivered, and what a unit of it cost
// (spec/17-kpis.md).
//
// The indicators are defined per agent in KPIS.md, and `kennzahl:` is the
// comparison anchor: two agents that both define `geloeste-tickets` are counted
// into the same line org-wide. Which is why this sits in the API layer and not
// in the observability store — the store counts, the config defines, and the
// two only meet here.

// sparkBuckets is the number of points a course is drawn with. Fixed rather
// than derived from the period, because the sparkline has a fixed width: 24
// hours and 90 days have to yield the same number of points, or the same pixels
// would mean something different depending on the filter.
const sparkBuckets = 14

// IndicatorReport is the price list for one scope (the whole org or one agent).
type IndicatorReport struct {
	Indicators []observability.IndicatorResult `json:"indicators"`
	// Failed are the runs that ended without a result. It stands next to the
	// prices rather than among them: an agent that abandons every hard case
	// looks excellent on the rest, and the price list alone would reward that.
	Failed int64 `json:"failed"`
	// TotalUSD is the denominator behind every price — shown so the figures can
	// be checked rather than believed.
	TotalUSD float64 `json:"total_usd"`
	// Quality qualifies the prices: came the case back, did a human refuse the
	// proposal, how fast was the first reaction. A price without them gets
	// quoted alone, and alone it says less than it appears to.
	Quality observability.Quality `json:"quality"`
}

// handleOrgIndicators is the price list over the organization.
func (s *Server) handleOrgIndicators(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	_, since := costWindow(r)
	list, err := s.Registry.List(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	ids := make([]uuid.UUID, 0, len(list))
	for _, a := range list {
		ids = append(ids, a.ID)
	}
	rep, err := s.indicatorReport(r, ids, since)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// handleAgentIndicators is the same list for a single agent.
func (s *Server) handleAgentIndicators(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	_, since := costWindow(r)
	rep, err := s.indicatorReport(r, []uuid.UUID{id}, since)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// indicatorReport collects the indicators of the given agents, groups them by
// key and prices each line.
//
// The denominator per line is the cost of exactly those agents that carry the
// key — not the cost of everybody. Otherwise the price of a resolved ticket
// would include a QA agent that never touched one.
func (s *Server) indicatorReport(r *http.Request, agentIDs []uuid.UUID, since time.Time) (IndicatorReport, error) {
	ctx := r.Context()
	rep := IndicatorReport{Indicators: []observability.IndicatorResult{}}

	// key → definition (first one wins) and the agents that carry it.
	defs := map[string]observability.Indicator{}
	carriers := map[string][]uuid.UUID{}
	for _, id := range agentIDs {
		cfg, err := s.Registry.CurrentConfig(ctx, id)
		if err != nil {
			continue // an agent without a config version has no indicators
		}
		kpis, err := agents.ParseKPIs(cfg.Files["KPIS.md"])
		if err != nil {
			continue // saved before the parser existed, or hand-edited: skip, do not fail the report
		}
		for _, k := range kpis {
			if _, ok := defs[k.Key]; !ok {
				defs[k.Key] = observability.Indicator{
					Key: k.Key, Title: k.Title, Action: k.Action,
					Origin: k.Origin, Per: k.Per, Goal: k.Goal, Period: k.Period,
				}
			}
			carriers[k.Key] = append(carriers[k.Key], id)
		}
	}

	keys := make([]string, 0, len(defs))
	for k := range defs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// The previous period, for the trend: the same span again, directly before.
	vor := since.Add(-time.Since(since))

	for _, key := range keys {
		ids := carriers[key]
		count, returned, prevCount, err := s.Obs.CountIndicator(ctx, defs[key], ids, since)
		if err != nil {
			return rep, err
		}
		usd, err := s.Obs.CostOfAgents(ctx, ids, since)
		if err != nil {
			return rep, err
		}
		// The denominator of the previous period is that period's cost, not
		// today's — otherwise the "trend" would only ever show the change in
		// the count, dressed up as a price.
		prevUSD, err := s.Obs.CostOfAgentsBetween(ctx, ids, vor, since)
		if err != nil {
			return rep, err
		}
		series, err := s.Obs.IndicatorSeries(ctx, defs[key], ids, since, sparkBuckets)
		if err != nil {
			return rep, err
		}
		rep.Indicators = append(rep.Indicators, observability.IndicatorResult{
			Indicator: defs[key], Count: count, Returned: returned, PrevCount: prevCount,
			UnitUSD:     observability.UnitCost(usd, count),
			PrevUnitUSD: observability.UnitCost(prevUSD, prevCount),
			Series:      series,
		})
	}
	sort.SliceStable(rep.Indicators, func(i, j int) bool {
		return rep.Indicators[i].Count > rep.Indicators[j].Count
	})

	failed, err := s.Obs.FailedTasks(ctx, agentIDs, since)
	if err != nil {
		return rep, err
	}
	rep.Failed = failed
	if rep.TotalUSD, err = s.Obs.CostOfAgents(ctx, agentIDs, since); err != nil {
		return rep, err
	}
	if rep.Quality, err = s.Obs.QualityReport(ctx, agentIDs, since); err != nil {
		return rep, err
	}
	return rep, nil
}
