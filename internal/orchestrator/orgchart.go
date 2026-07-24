package orchestrator

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
)

// Organigramm-Broker: beantwortet die request_org_chart-Anfrage eines Daemons
// (Meta-Aktion covey/org_chart). Die Sicht entspricht GET /org/chart — Menschen
// und Agenten samt Profilen, Abteilungen, Vorgesetzten-Beziehungen — reduziert
// auf das, was ein Agent braucht (keine Kill-Switches, Budgets, Webhooks).

type chartPerson struct {
	ID               uuid.UUID         `json:"id"`
	Name             string            `json:"name"`
	Email            string            `json:"email,omitempty"`
	Slug             string            `json:"slug,omitempty"`
	JobTitle         string            `json:"job_title,omitempty"`
	Phone            string            `json:"phone,omitempty"`
	Identities       map[string]string `json:"identities,omitempty"`
	Responsibilities string            `json:"responsibilities,omitempty"`
	Custom           map[string]string `json:"custom,omitempty"`
	// ManagerID: bei Menschen humans.manager_id, bei Agenten
	// agents.supervisor_id (Mensch ODER Agent — polymorph).
	ManagerID    *uuid.UUID `json:"manager_id,omitempty"`
	DepartmentID *uuid.UUID `json:"department_id,omitempty"`
	// Self markiert den anfragenden Agenten selbst.
	Self bool `json:"self,omitempty"`
}

type chartField struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

type chartDepartment struct {
	ID          uuid.UUID   `json:"id"`
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Leads       []chartLead `json:"leads,omitempty"`
}

type chartLead struct {
	Kind string    `json:"kind"` // human | agent
	ID   uuid.UUID `json:"id"`
}

// orgChartPayload baut das Organigramm der Organisation als JSON. Fehler in
// Teilabfragen sind nicht fatal — der Agent bekommt dann eine Teilsicht statt
// gar keiner Antwort.
func (o *Orchestrator) orgChartPayload(ctx context.Context, orgID, selfID uuid.UUID) json.RawMessage {
	chart := struct {
		ProfileFields []chartField      `json:"profile_fields"`
		Humans        []chartPerson     `json:"humans"`
		Agents        []chartPerson     `json:"agents"`
		Departments   []chartDepartment `json:"departments"`
	}{
		ProfileFields: []chartField{},
		Humans:        []chartPerson{},
		Agents:        []chartPerson{},
		Departments:   []chartDepartment{},
	}

	// Definitionen der konfigurierbaren Profilfelder — damit der Agent die
	// Schlüssel in custom auf Anzeige-Labels abbilden kann.
	if rows, err := o.Pool.Query(ctx, `SELECT key, label FROM profile_fields
		WHERE org_id=$1 ORDER BY created_at`, orgID); err == nil {
		for rows.Next() {
			var f chartField
			if rows.Scan(&f.Key, &f.Label) == nil {
				chart.ProfileFields = append(chart.ProfileFields, f)
			}
		}
		rows.Close()
	}

	if rows, err := o.Pool.Query(ctx, `SELECT id, display_name, email, job_title, phone,
			identities, responsibilities, custom, manager_id, department_id
		FROM humans WHERE org_id=$1 ORDER BY created_at`, orgID); err == nil {
		for rows.Next() {
			var p chartPerson
			if rows.Scan(&p.ID, &p.Name, &p.Email, &p.JobTitle, &p.Phone,
				&p.Identities, &p.Responsibilities, &p.Custom, &p.ManagerID, &p.DepartmentID) == nil {
				chart.Humans = append(chart.Humans, p)
			}
		}
		rows.Close()
	}

	if rows, err := o.Pool.Query(ctx, `SELECT id, display_name, slug, job_title, phone,
			identities, responsibilities, custom, supervisor_id, department_id
		FROM agents WHERE org_id=$1 ORDER BY created_at`, orgID); err == nil {
		for rows.Next() {
			var p chartPerson
			if rows.Scan(&p.ID, &p.Name, &p.Slug, &p.JobTitle, &p.Phone,
				&p.Identities, &p.Responsibilities, &p.Custom, &p.ManagerID, &p.DepartmentID) == nil {
				p.Self = p.ID == selfID
				chart.Agents = append(chart.Agents, p)
			}
		}
		rows.Close()
	}

	depts := map[uuid.UUID]int{}
	if rows, err := o.Pool.Query(ctx, `SELECT id, name, description
		FROM departments WHERE org_id=$1 ORDER BY name`, orgID); err == nil {
		for rows.Next() {
			var d chartDepartment
			if rows.Scan(&d.ID, &d.Name, &d.Description) == nil {
				depts[d.ID] = len(chart.Departments)
				chart.Departments = append(chart.Departments, d)
			}
		}
		rows.Close()
	}
	if rows, err := o.Pool.Query(ctx, `SELECT l.department_id, l.human_id, l.agent_id
		FROM department_leads l JOIN departments d ON d.id=l.department_id
		WHERE d.org_id=$1`, orgID); err == nil {
		for rows.Next() {
			var deptID uuid.UUID
			var humanID, agentID *uuid.UUID
			if rows.Scan(&deptID, &humanID, &agentID) != nil {
				continue
			}
			idx, ok := depts[deptID]
			if !ok {
				continue
			}
			lead := chartLead{Kind: "human"}
			if humanID != nil {
				lead.ID = *humanID
			} else if agentID != nil {
				lead = chartLead{Kind: "agent", ID: *agentID}
			} else {
				continue
			}
			chart.Departments[idx].Leads = append(chart.Departments[idx].Leads, lead)
		}
		rows.Close()
	}

	raw, err := json.Marshal(chart)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}
