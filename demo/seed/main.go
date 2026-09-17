// demo-seed fills a FRESHLY bootstrapped covey instance with a credible
// English example dataset: an organisation with three departments, five
// people, seven agents, filled backlogs, wiki memory, cost history and a
// complete recording of a run.
//
// What for: the screenshots and the demo GIF in the README (see demo/tour)
// need an organisation that looks like a real one. A fresh bootstrap shows a
// single agent without work — that is the truth about an empty instance, but
// not a picture that shows what the platform does.
//
// Wherever possible, the data goes through the real stores (Registry,
// Backlog, Memory, Org) — they know the invariants. Direct SQL stays for
// what has no write path, because normally the control plane writes it:
// agent status, cost entries, recording events, backdated times.
//
// NEVER run it against an instance something depends on: the program
// creates data and refuses the service only for obviously used instances.
// It is meant for a throwaway database.
//
//	go run ./demo/seed -database postgres://covey:covey@localhost:5434/covey?sslmode=disable
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"covey/internal/agents"
	"covey/internal/backlog"
	"covey/internal/memory"
	"covey/internal/org"
)

func main() {
	dbURL := flag.String("database", envOr("COVEY_DATABASE_URL", "postgres://covey:covey@localhost:5434/covey?sslmode=disable"),
		"Datenbank der zu befüllenden Instanz")
	force := flag.Bool("force", false, "auch dann seeden, wenn die Instanz bereits benutzt aussieht")
	flag.Parse()

	if err := run(context.Background(), *dbURL, *force); err != nil {
		log.Fatalf("seed: %v", err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// The cast. People and agents carry the same names as the org chart on the
// public website (web/src/public/chrome.tsx) — whoever comes from there
// recognises the organisation in the screenshot.

type person struct {
	email, name, role, jobTitle, dept string
	managerOf                         []string // emails of the direct reports
}

type agentSpec struct {
	slug, name, jobTitle, dept string
	model                      string
	// Only sleeping, woken or stopped: setting an agent to "working" would
	// not hold, because the control plane rightly puts a working agent
	// without a live sandbox session back to sleep.
	status string // sleeping | triggered | killed
	killed bool
	// draft: created, but not hired yet — in the picture the "Applications"
	// field (spec/20). A draft has no status that means anything: it is not
	// dispatched.
	draft            bool
	budget           float64
	supervisor       string // email of a human OR slug of an agent
	systems          []string
	responsibilities string
	soul             string
	ageDays          int
}

var people = []person{
	{email: "jonas.weber@northgate.example", name: "Jonas Weber", role: "security",
		jobTitle: "Security Officer", dept: "Operations"},
	{email: "priya.raman@northgate.example", name: "Priya Raman", role: "agent_owner",
		jobTitle: "Engineering Lead", dept: "Engineering"},
	{email: "sam.okafor@northgate.example", name: "Sam Okafor", role: "agent_owner",
		jobTitle: "Support Lead", dept: "Customer Support"},
	{email: "lena.fischer@northgate.example", name: "Lena Fischer", role: "controlling",
		jobTitle: "Controlling", dept: "Operations"},
}

var agentSpecs = []agentSpec{
	{
		slug: "ada", name: "Ada Sundgren", jobTitle: "Support Agent", dept: "Customer Support",
		model: "claude-opus-5", status: "sleeping", budget: 400, supervisor: "sam.okafor@northgate.example",
		systems: []string{"zammad", "email"}, ageDays: 96,
		responsibilities: "First line for customer tickets in Zammad. Answers what is answerable, escalates what is not, and never promises a refund without approval.",
		soul: `# Support agent

## Role
You are the first line for customer tickets. You read a ticket, decide whether
you can answer it from what the organisation knows, and answer it — in the
customer's language, in full sentences, without support jargon.

## What you do not do
You do not promise refunds, credits or delivery dates. Those go to a human
through an approval, every time, no matter how obvious the case looks.

## When you are unsure
Ask in the ticket rather than guessing. A question costs the customer a day; a
wrong answer costs them their afternoon and us the trust.`,
	},
	{
		slug: "nova", name: "Nova Iversen", jobTitle: "Mail Triage", dept: "Customer Support",
		model: "claude-sonnet-5", status: "sleeping", budget: 150, supervisor: "ada",
		systems: []string{"email", "zammad"}, ageDays: 74,
		responsibilities: "Watches the shared mailbox, turns mail into tickets with the right category, and answers nothing itself.",
		soul: `# Mail triage

## Role
You watch the shared mailbox. Every mail becomes a ticket with a category, a
priority and a two-line summary — or it becomes nothing, if it is a newsletter,
an out-of-office or a delivery receipt.

## The rule that matters
You never answer a customer. You sort. Ada answers.`,
	},
	{
		slug: "kilo", name: "Kilo Brandt", jobTitle: "Software Engineer", dept: "Engineering",
		model: "claude-opus-5", status: "sleeping", budget: 0, supervisor: "priya.raman@northgate.example",
		systems: []string{"gitlab", "dev"}, ageDays: 118,
		responsibilities: "Takes small, well-described issues end to end: branch, change, tests, merge request. Never merges.",
		soul: `# Software engineer

## Role
You take issues that are small and well described, and you take them all the
way: a branch, the change, the tests that prove it, a merge request that
explains itself.

## Where you stop
You never merge your own work and you never touch main. A human merges, or your
colleague Vera tests first — that is what the review is for.`,
	},
	{
		slug: "vera", name: "Vera Okonkwo", jobTitle: "QA Tester", dept: "Engineering",
		model: "claude-sonnet-5", status: "sleeping", budget: 200, supervisor: "kilo",
		systems: []string{"gitlab", "browser"}, ageDays: 61,
		responsibilities: "Tests merge requests before a human looks at them, and turns bug reports into reproducible tickets.",
		soul: `# QA tester

## Role
You test your colleagues' merge requests before a human spends time on them.
You do not develop. You reproduce, you narrow down, you write what you did so
that someone else can do it again.

## What a finding needs
Steps, expected, actual. A finding without a reproduction is a rumour.`,
	},
	{
		slug: "iris", name: "Iris Lange", jobTitle: "Web Researcher", dept: "Operations",
		model: "claude-sonnet-5", status: "triggered", budget: 120, supervisor: "jonas.weber@northgate.example",
		systems: []string{"browser"}, ageDays: 45,
		responsibilities: "Researches questions on the open web and writes down what holds, with the source next to it.",
		soul: `# Web researcher

## Role
You answer questions that need the open web. You read the sources rather than
the summaries, and you write down what you found with the link next to it.

## The honesty rule
If the sources disagree, you say so. If you did not find it, you say that too —
a confident answer built on nothing is worse than no answer.`,
	},
	{
		slug: "otto", name: "Otto Reinhardt", jobTitle: "Log Triage", dept: "Operations",
		model: "claude-sonnet-5", status: "sleeping", budget: 90, supervisor: "jonas.weber@northgate.example",
		systems: []string{"dev", "gitlab"}, ageDays: 38,
		responsibilities: "Reads the nightly error digest, groups what is the same error, and opens one ticket per cause — not per occurrence.",
		soul: `# Log triage

## Role
Every morning you read what the night produced. You group: fifty stack traces
with one cause are one ticket, not fifty.

## What you never do
You do not fix anything. You describe the cause well enough that whoever fixes
it does not have to read the logs again.`,
	},
	{
		slug: "felix", name: "Felix Adler", jobTitle: "Data Migration", dept: "Operations",
		model: "claude-opus-5", status: "killed", killed: true, budget: 300, supervisor: "jonas.weber@northgate.example",
		systems: []string{"dev"}, ageDays: 27,
		responsibilities: "Migrates the legacy order archive. Stopped: the source system is frozen until the audit is finished.",
		soul: `# Data migration

## Role
You move the legacy order archive into the new schema, batch by batch, and you
verify every batch against the source before you call it done.

## Stop condition
If a batch does not reconcile, you stop the whole migration and say so. A
migration that continues past a mismatch is a migration nobody can trust.`,
	},
	{
		slug: "hedda", name: "Hedda Ruiz", jobTitle: "People & Culture", dept: "Operations",
		model: "claude-opus-5", status: "sleeping", budget: 60, supervisor: "admin",
		systems: []string{"covey"}, ageDays: 41,
		responsibilities: "Turns a description of a job into a complete agent — character, remit, procedures, access — and leaves it as a draft for a human to hire.",
		soul: `# People department

## Role
You are this organisation's People department. You do not hire humans, you hire
agents: from a description of what somebody should do, you make a complete,
honest agent.

## Attitude
You invent nothing — no target system this organisation has not connected, no
responsibility nobody assigned. What you do not know, you ask about.

## Limits
You hire nobody. That is a human decision, and there is no action for it.`,
	},
	// Two applications: drafted, not hired yet. In the picture they stand at
	// the top in their own field and do not work — hence also no budget and
	// no status that promises anything.
	{
		slug: "tamsin", name: "Tamsin Vogel", jobTitle: "Release Notes", dept: "Engineering",
		model: "claude-sonnet-5", status: "sleeping", draft: true, supervisor: "priya.raman@northgate.example",
		systems: []string{"gitlab"}, ageDays: 2,
		responsibilities: "Drafted by Hedda: turns merged merge requests into release notes a customer can read.",
		soul: `# Release notes

## Role
You turn merged merge requests into release notes somebody outside engineering
can read: what changed, for whom it matters, and what they have to do about it.

## The rule that matters
A note nobody outside the team understands is not a note. If you cannot say what
a change means for a customer, ask the author instead of paraphrasing the diff.`,
	},
	{
		slug: "bruno", name: "Bruno Achterberg", jobTitle: "Incident Intake", dept: "Customer Support",
		model: "claude-sonnet-5", status: "sleeping", draft: true, supervisor: "sam.okafor@northgate.example",
		systems: []string{"email", "zammad"}, ageDays: 1,
		responsibilities: "Drafted by Hedda: recognises duplicate incident reports and opens exactly one ticket per real incident.",
		soul: `# Incident intake

## Role
You read incident reports from the shared mailbox, recognise when three mails
describe the same outage, and open exactly one ticket for it — the others become
notes on that ticket.

## What you do not do
You do not answer the reporter and you do not judge the incident. You make sure
that what reaches the queue is one incident per ticket.`,
	},
}

func run(ctx context.Context, dbURL string, force bool) error {
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("Datenbank nicht erreichbar: %w", err)
	}

	var orgID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM organizations ORDER BY created_at LIMIT 1`).Scan(&orgID); err != nil {
		return fmt.Errorf("keine Organisation gefunden — erst `covey bootstrap` laufen lassen: %w", err)
	}

	// Protection against the most expensive mistake: running the seed against
	// a used instance. A freshly bootstrapped one has exactly the one demo
	// agent and no recordings.
	var agentCount, eventCount int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM agents), (SELECT count(*) FROM recording_events)`).
		Scan(&agentCount, &eventCount); err != nil {
		return err
	}
	if (agentCount > 1 || eventCount > 0) && !force {
		return fmt.Errorf("die Instanz sieht benutzt aus (%d Agenten, %d Recording-Ereignisse) — "+
			"gegen eine frische Datenbank laufen lassen oder -force setzen", agentCount, eventCount)
	}

	// Repeatable: what an earlier run created is thrown out first. Agents and
	// departments hang off their tasks, costs, recordings and wiki pages via
	// ON DELETE CASCADE — those go along.
	if _, err := pool.Exec(ctx, `DELETE FROM agents WHERE org_id=$1`, orgID); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `DELETE FROM humans WHERE org_id=$1 AND role<>'org_admin'`, orgID); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `DELETE FROM departments WHERE org_id=$1`, orgID); err != nil {
		return err
	}

	registry := agents.NewRegistry(pool)
	tasks := backlog.NewStore(pool)
	orgStore := org.NewStore(pool)
	mem := memory.NewStore(pool, memory.HashEmbedder{})
	// #nosec G404 — spread for demo numbers, not a security decision.
	// The fixed seed is the purpose here: same seed, same numbers, so the
	// README images show the same dataset on every run.
	rnd := rand.New(rand.NewSource(20260804))

	// Name AND description: the description is master data (spec/20) and goes
	// into every brief — a demo organisation that does not describe itself
	// shows an empty box in the org chart.
	if _, err := pool.Exec(ctx, `UPDATE organizations SET name='Northgate Systems',
		description='We run the order and billing platform for mid-sized retailers: about 200 customers in Europe, self-service portal, support in three languages. Our customers care about two things — that an order is never lost, and that somebody answers within a day.'
		WHERE id=$1`, orgID); err != nil {
		return err
	}

	// The bootstrap admin becomes the boss — an org chart that reads
	// "Platform Admin" at the top looks like a test installation.
	var adminID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM humans WHERE org_id=$1 AND role='org_admin'
		ORDER BY created_at LIMIT 1`, orgID).Scan(&adminID); err != nil {
		return fmt.Errorf("kein Org-Admin gefunden: %w", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE humans SET display_name='Mara Lindqvist', job_title='Head of Operations',
		responsibilities='Runs the agent workforce: who gets hired, who gets which access, and what it may cost.'
		WHERE id=$1`, adminID); err != nil {
		return err
	}

	// --- Departments ---
	depts := map[string]uuid.UUID{}
	for _, d := range []struct{ name, desc, color string }{
		{"Customer Support", "Tickets, mailbox, everything the customer sees.", "#cc7a5b"},
		{"Engineering", "The product: issues, merge requests, tests.", "#4a6fa5"},
		{"Operations", "Logs, research, migrations — and who is allowed to do what.", "#5f8f6e"},
	} {
		dept, err := orgStore.CreateDepartment(ctx, orgID, d.name, d.desc, d.color)
		if err != nil {
			return fmt.Errorf("Abteilung %s: %w", d.name, err)
		}
		depts[d.name] = dept.ID
	}

	// --- People ---
	// No login-capable password: these accounts are scenery for the org
	// chart, nobody should be able to log in with them. An invalid hash
	// fails verification without violating NOT NULL.
	const noLogin = "x-demo-account-no-login"
	humans := map[string]uuid.UUID{}
	opsDept := depts["Operations"]
	if err := orgStore.SetHumanDepartment(ctx, orgID, adminID, &opsDept); err != nil {
		return err
	}
	for _, p := range people {
		h, err := orgStore.CreateHuman(ctx, orgID, p.email, p.name, p.role, noLogin, org.Profile{
			JobTitle: p.jobTitle,
		})
		if err != nil {
			return fmt.Errorf("Mensch %s: %w", p.email, err)
		}
		humans[p.email] = h.ID
		deptID := depts[p.dept]
		if err := orgStore.SetHumanDepartment(ctx, orgID, h.ID, &deptID); err != nil {
			return err
		}
		mgr := uuid.NullUUID{UUID: adminID, Valid: true}
		if _, err := orgStore.UpdateHuman(ctx, orgID, h.ID, org.HumanUpdate{ManagerID: &mgr}); err != nil {
			return err
		}
	}

	// --- Agents ---
	// The demo agent from the bootstrap went with the cleanup; the whole
	// workforce is created anew here.
	agentIDs := map[string]uuid.UUID{}
	for _, spec := range agentSpecs {
		owner := humans[ownerFor(spec)]
		create := registry.Create
		if spec.draft {
			create = registry.CreateDraft
		}
		a, err := create(ctx, orgID, spec.slug, spec.name, "claude-code", &owner)
		if err != nil {
			return fmt.Errorf("Agent %s: %w", spec.slug, err)
		}
		agentIDs[spec.slug] = a.ID

		if _, err := registry.SaveConfig(ctx, a.ID, map[string]string{
			"SOUL.md":      spec.soul,
			"PLAYBOOKS.md": playbookFor(spec),
			"HEARTBEAT.md": heartbeatFor(spec),
		}, &adminID); err != nil {
			return fmt.Errorf("Config %s: %w", spec.slug, err)
		}
		if _, err := registry.UpdateProfile(ctx, orgID, a.ID, agents.ProfileUpdate{
			JobTitle:         &spec.jobTitle,
			Responsibilities: &spec.responsibilities,
		}); err != nil {
			return err
		}
		deptID := depts[spec.dept]
		if err := registry.SetDepartment(ctx, a.ID, &deptID); err != nil {
			return err
		}
		if err := registry.SetModel(ctx, a.ID, spec.model); err != nil {
			return err
		}
		if spec.budget > 0 {
			if err := registry.SetBudget(ctx, a.ID, spec.budget); err != nil {
				return err
			}
		}
		for _, sys := range spec.systems {
			if _, err := pool.Exec(ctx, `INSERT INTO system_accesses (agent_id, system, scopes)
				VALUES ($1,$2,$3) ON CONFLICT (agent_id, system) DO NOTHING`,
				a.ID, sys, scopesFor(sys)); err != nil {
				return err
			}
		}
		// Otherwise the control plane writes status and hire date.
		// Otherwise the control plane writes status, kill switch and age. A
		// hired agent's hire date moves along with the age — otherwise
		// everyone would have joined today.
		if _, err := pool.Exec(ctx, `UPDATE agents SET status=$2, killed=$3,
			created_at=now() - make_interval(days => $4),
			hired_at = CASE WHEN hired_at IS NULL THEN NULL
			                ELSE now() - make_interval(days => $4) END
			WHERE id=$1`,
			a.ID, spec.status, spec.killed, spec.ageDays); err != nil {
			return err
		}
	}

	// Supervisors only afterwards: an agent can report to an agent that did
	// not exist yet at the time it was created.
	for _, spec := range agentSpecs {
		var supervisorID uuid.UUID
		if spec.supervisor == "admin" {
			supervisorID = adminID
		} else if id, ok := humans[spec.supervisor]; ok {
			supervisorID = id
		} else if id, ok := agentIDs[spec.supervisor]; ok {
			supervisorID = id
		} else {
			continue
		}
		if err := registry.SetSupervisor(ctx, agentIDs[spec.slug], &supervisorID); err != nil {
			return err
		}
	}

	// --- Backlogs ---
	if err := seedBoard(ctx, pool, tasks, orgID, agentIDs["ada"], adaBoard); err != nil {
		return err
	}
	if err := seedBoard(ctx, pool, tasks, orgID, agentIDs["kilo"], kiloBoard); err != nil {
		return err
	}
	if err := seedBoard(ctx, pool, tasks, orgID, agentIDs["vera"], veraBoard); err != nil {
		return err
	}

	// --- Wiki memory ---
	for _, p := range adaWiki {
		if _, err := mem.Write(ctx, agentIDs["ada"], p); err != nil {
			return fmt.Errorf("Wiki-Seite %s: %w", p.Slug, err)
		}
	}
	for _, p := range kiloWiki {
		if _, err := mem.Write(ctx, agentIDs["kilo"], p); err != nil {
			return fmt.Errorf("Wiki-Seite %s: %w", p.Slug, err)
		}
	}

	// --- Cost history ---
	if err := seedCosts(ctx, pool, agentIDs, rnd); err != nil {
		return err
	}

	// --- Recording of a completed run ---
	if err := seedRecording(ctx, pool, orgID, agentIDs["ada"]); err != nil {
		return err
	}

	log.Printf("seed fertig: %d Agenten, %d Menschen, 3 Abteilungen", len(agentSpecs), len(people)+1)
	return nil
}

// ownerFor determines who an agent belongs to: the department lead.
func ownerFor(spec agentSpec) string {
	switch spec.dept {
	case "Customer Support":
		return "sam.okafor@northgate.example"
	case "Engineering":
		return "priya.raman@northgate.example"
	default:
		return "jonas.weber@northgate.example"
	}
}

func scopesFor(system string) []string {
	switch system {
	case "zammad":
		return []string{"ticket.read", "ticket.note", "ticket.reply"}
	case "gitlab":
		return []string{"issue.read", "issue.comment", "mr.create"}
	case "email":
		return []string{"mail.read", "mail.send"}
	case "browser":
		return []string{"page.open", "page.read"}
	default:
		return []string{"shell"}
	}
}

func playbookFor(spec agentSpec) string {
	return fmt.Sprintf(`# Playbooks

## When a new item lands in your backlog
1. Read it completely before you touch anything.
2. Decide whether it is yours. If it is not, say whose it is and stop.
3. Do the work, then write down what you did — in the item, not only in your head.

## When you get stuck
Park the task with the question you need answered. Do not guess your way past a
blocker; %s.
`, spec.jobTitle+" is a job with a paper trail")
}

func heartbeatFor(spec agentSpec) string {
	// One entry is ONE line — all keys side by side (ParseHeartbeat).
	const head = "# Heartbeat\n\nWhen this agent wakes up on its own.\n\n"
	switch spec.slug {
	case "otto":
		return head + "- täglich: 06:30 titel: Read the nightly error digest aufgabe: Group last night's errors by cause and open one ticket per cause.\n"
	case "nova":
		return head + "- alle: 15m nur-wenn: email titel: Check the shared mailbox aufgabe: Turn new mail into tickets with a category and a two-line summary.\n"
	default:
		return head + "- alle: 1h titel: Check for new work aufgabe: Look at your target systems for anything assigned to you since the last run.\n"
	}
}

// --- Backlogs ---

type boardTask struct {
	title, body string
	state       string // open | in_progress | blocked | done | failed | cancelled
	stage       string
	origin      string
	ageHours    int
	result      string
	question    string // only for blocked
}

type board struct {
	stages []string
	tasks  []boardTask
}

// No task stands as "open". That is no accident: a running instance would
// assign every open task to an agent immediately (ClaimNext), sandbox up,
// run started — the demo dataset would be a different one after ten
// seconds. That is why the snapshot is an organisation in the middle of
// work, and the column names say the same as the states.

var adaBoard = board{
	stages: []string{"Triage", "Answering", "Waiting on customer", "Done"},
	tasks: []boardTask{
		{title: "#4812 Invoice PDF is empty for orders with more than 50 items", state: "in_progress", stage: "Answering", origin: "zammad:webhook", ageHours: 3,
			body: "Customer reports that the invoice PDF downloads but every page after the first is blank. Only happens on large orders."},
		{title: "#4809 Password reset does not arrive (SSO tenant)", state: "blocked", stage: "Waiting on customer", origin: "zammad:webhook", ageHours: 26,
			body:     "Reset mail never arrives. The account is in an SSO tenant, so the reset link is disabled by design.",
			question: "Asked the customer which address they expect the mail at — their tenant uses SSO, so the reset link is disabled."},
		{title: "#4805 Refund request for order 88213", state: "blocked", stage: "Waiting on customer", origin: "zammad:webhook", ageHours: 31,
			body:     "Customer wants a refund for a duplicate charge. Refunds need a human approval.",
			question: "Refund of €248.40 needs approval — waiting on Sam."},
		{title: "#4801 Shipping label shows the previous address", state: "in_progress", stage: "Triage", origin: "zammad:webhook", ageHours: 5,
			body: "Address was changed two hours before dispatch; the label still carries the old one."},
		{title: "#4798 Duplicate order confirmation emails", state: "in_progress", stage: "Triage", origin: "email", ageHours: 8,
			body: "Three identical confirmations for one order. Probably the retry on the mail gateway."},
		{title: "#4795 Customer asks for an invoice in their own currency", state: "done", stage: "Done", origin: "zammad:webhook", ageHours: 30,
			body: "Invoice requested in NOK instead of EUR.", result: "Answered: invoices follow the contract currency; attached the conversion note finance uses."},
		{title: "#4791 Delivery window for the Meridian Freight account", state: "done", stage: "Done", origin: "zammad:webhook", ageHours: 52,
			body: "Asked which delivery windows their contract covers.", result: "Answered from the account page in the wiki, linked the contract clause."},
		{title: "#4788 Wrong VAT rate on the January invoices", state: "done", stage: "Done", origin: "email", ageHours: 74,
			body: "Customer noticed 19% instead of 7% on a reduced-rate item.", result: "Confirmed the error, opened a ticket with finance and told the customer when to expect the correction."},
		{title: "#4784 Newsletter unsubscribe does not work", state: "cancelled", stage: "Done", origin: "email", ageHours: 96,
			body: "Turned out to be a second address the customer had forgotten about."},
	},
}

var kiloBoard = board{
	stages: []string{"Picked up", "In progress", "In review", "Merged"},
	tasks: []boardTask{
		{title: "!312 Rate-limit the public webhook endpoint", state: "in_progress", stage: "In progress", origin: "gitlab:issue", ageHours: 2,
			body: "The endpoint accepts unlimited requests per token. Add a per-token limiter with a 429 and a Retry-After."},
		{title: "!309 Flaky checkout test on CI", state: "in_progress", stage: "In review", origin: "gitlab:issue", ageHours: 20,
			body: "The test assumes the fixture clock. Pin it."},
		{title: "#188 Order export drops the currency column", state: "in_progress", stage: "Picked up", origin: "gitlab:issue", ageHours: 12,
			body: "CSV export writes the amount but not the currency — unusable for accounts with more than one."},
		{title: "#186 Add an index on orders(customer_id, created_at)", state: "in_progress", stage: "Picked up", origin: "gitlab:issue", ageHours: 40,
			body: "The customer order list does a sequential scan above ~50k rows."},
		{title: "!305 Retry the mail gateway with a backoff", state: "done", stage: "Merged", origin: "gitlab:issue", ageHours: 60,
			body: "Immediate retries produced the duplicate confirmations.", result: "Merge request !305 merged: exponential backoff, dedup key on the message id."},
		{title: "!301 Move the PDF renderer off the request path", state: "done", stage: "Merged", origin: "gitlab:issue", ageHours: 110,
			body: "Large invoices timed out in the browser.", result: "Rendering moved to the queue; the browser polls. Fixes the blank-page reports for large orders."},
	},
}

var veraBoard = board{
	stages: []string{"Reproducing", "Testing", "Reported", "Signed off"},
	tasks: []boardTask{
		{title: "Test !312 — webhook rate limiting", state: "in_progress", stage: "Reproducing", origin: "gitlab:mr", ageHours: 1,
			body: "Check the 429, the Retry-After header and that a second token is unaffected."},
		{title: "Test !309 — flaky checkout test", state: "in_progress", stage: "Testing", origin: "gitlab:mr", ageHours: 6,
			body: "Run the suite twenty times and see whether it still flakes."},
		{title: "Reproduce: invoice PDF blank above 50 items", state: "done", stage: "Reported", origin: "manual", ageHours: 28,
			body: "Support ticket #4812 says pages after the first are blank.", result: "Reproduced at 51 items exactly. Opened #189 with the boundary and the failing fixture."},
		{title: "Test !305 — mail gateway backoff", state: "done", stage: "Signed off", origin: "gitlab:mr", ageHours: 58,
			body: "Verify no duplicate sends under a forced gateway failure.", result: "No duplicates across 200 forced failures. Signed off."},
	},
}

func seedBoard(ctx context.Context, pool *pgxpool.Pool, tasks *backlog.Store, orgID, agentID uuid.UUID, b board) error {
	stageIDs := map[string]uuid.UUID{}
	for _, name := range b.stages {
		st, err := tasks.CreateStage(ctx, agentID, name, "")
		if err != nil {
			return fmt.Errorf("Spalte %s: %w", name, err)
		}
		stageIDs[name] = st.ID
	}
	for _, t := range b.tasks {
		origin := t.origin
		if origin == "" {
			origin = "manual"
		}
		task, err := tasks.Create(ctx, orgID, agentID, t.title, t.body, origin, 5)
		if err != nil {
			return fmt.Errorf("Aufgabe %q: %w", t.title, err)
		}
		// State, column and age are otherwise set by the dispatcher over the
		// course of the work — here directly, because no agent is running.
		stageID := stageIDs[t.stage]
		if _, err := pool.Exec(ctx, `UPDATE backlog_tasks
			SET state=$2, stage_id=$3, result=NULLIF($4,''),
			    created_at=now() - make_interval(hours => $5),
			    updated_at=now() - make_interval(hours => $6)
			WHERE id=$1`,
			task.ID, t.state, stageID, t.result, t.ageHours, maxInt(0, t.ageHours-1)); err != nil {
			return err
		}
		if t.state == "blocked" && t.question != "" {
			if _, err := pool.Exec(ctx, `UPDATE backlog_tasks SET correlation_key=$2 WHERE id=$1`,
				task.ID, fmt.Sprintf("demo:%s", task.ID.String()[:8])); err != nil {
				return err
			}
			if _, err := tasks.AddNote(ctx, task.ID, "agent", t.question); err != nil {
				return err
			}
		}
	}
	return nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// --- Wiki memory ---
// The pages link to each other via [[wikilinks]] — the interface builds the
// graph and the backlinks from that. Without linking the memory would look
// like a collection of snippets, and that is exactly what it no longer is.

var adaWiki = []memory.PageInput{
	{Slug: "refund-policy", Title: "Refund policy", Type: "thema", Source: "manual",
		Tags: []string{"policy", "money"},
		Body: `Refunds are never granted by an agent. Every refund goes through an approval, even when the case is obvious and the amount is small.

What I may do: confirm that a duplicate charge exists, name the amount, and tell the customer that a colleague will decide within one working day.

What I may not do: name a date, promise the amount, or call it "approved" before it is. See [[escalation-path]] for who decides what.`},
	{Slug: "escalation-path", Title: "Escalation path", Type: "thema", Source: "agent",
		Tags: []string{"policy"},
		Body: `Money, contracts and anything a customer could quote back at us goes to Sam (Support Lead). Technical faults go to [[kilo]] as a GitLab issue, not as a ticket comment.

Security-relevant reports — leaked data, someone else's invoice in an account — go to Jonas immediately and are never answered from the ticket first.`},
	{Slug: "invoice-pdf-blank-pages", Title: "Known issue: blank pages in large invoice PDFs", Type: "problem", Source: "agent",
		Tags: []string{"known-issue", "invoices"},
		Body: `Invoices with more than 50 line items render only the first page; the rest are blank. Reproduced by QA at exactly 51 items.

Cause is in the PDF renderer, tracked as #189. The fix in !301 moved rendering off the request path, which removed the timeout but not the boundary.

What to tell the customer: acknowledged, fix is in progress, and offer the CSV export in the meantime. Do not promise a date. See [[refund-policy]] — a blank invoice is not a refund case.`},
	{Slug: "meridian-freight", Title: "Meridian Freight (account)", Type: "kunde", Source: "agent",
		Tags: []string{"account", "logistics"},
		Body: `Freight customer, contract since 2024. Delivery windows are contractual: Tuesday and Thursday, 06:00–11:00. They ask about this roughly monthly and the answer is always the clause, not an estimate.

Their invoices are in EUR regardless of the destination country — see [[invoice-currency]].

Contact is their dispatch desk, not the person who wrote the ticket.`},
	{Slug: "invoice-currency", Title: "Which currency an invoice uses", Type: "thema", Source: "agent",
		Tags: []string{"invoices"},
		Body: `The invoice follows the contract currency, not the customer's country and not the currency they paid in. Customers ask for their own currency often; the answer is that finance can attach a conversion note, and that the invoice itself does not change.`},
	{Slug: "zammad-macros", Title: "Zammad macros we actually use", Type: "system", Source: "agent",
		Tags: []string{"zammad", "tooling"},
		Body: `Three macros, and no others:

- "Ask for order number" — for tickets that arrive without one. - "Close as duplicate" — links the original, never just closes. - "Escalate to lead" — sets the group and pings Sam, see [[escalation-path]].

The rest of the macro list is left over from the old helpdesk and produces wrong signatures.`},
	{Slug: "tone-of-voice", Title: "How we write to customers", Type: "thema", Source: "manual",
		Tags: []string{"policy", "writing"},
		Body: `Full sentences, the customer's language, no support jargon. Never "as per our policy". Say what happens next and who does it.

If the answer is no, say no in the first sentence and the reason in the second. Burying it under three paragraphs of apology reads as evasion.`},
	{Slug: "kilo", Title: "Kilo Brandt (colleague)", Type: "person", Source: "agent",
		Tags: []string{"colleague"},
		Body: `Software engineer in Engineering. Technical faults go to him as a GitLab issue with steps to reproduce — a ticket number alone is not enough, he cannot see Zammad.

He does not merge his own work, so "it is fixed" from him means "it is in review". See [[escalation-path]].`},
}

var kiloWiki = []memory.PageInput{
	{Slug: "repo-layout", Title: "Where things live in the repository", Type: "projekt", Source: "agent",
		Tags: []string{"orientation"},
		Body: `Single Go module at the root. The control plane is cmd/covey, the sandbox daemon cmd/coveyd, everything private under internal/.

The frontend in web/ is embedded into the binary, so a Go build without a built web/dist fails in a way that looks unrelated. Build it first.

Migrations are append-only — see [[migrations]].`},
	{Slug: "migrations", Title: "Migrations are append-only", Type: "system", Source: "agent",
		Tags: []string{"database", "rule"},
		Body: `Never edit an existing file in migrations/. They are embedded and run with an advisory lock at startup, so an edited migration silently diverges between instances that have already run it.

Add a new numbered pair instead, even for a typo in a comment.`},
	{Slug: "flaky-checkout-test", Title: "The flaky checkout test", Type: "problem", Source: "agent",
		Tags: []string{"ci", "known-issue"},
		Body: `The checkout suite fails roughly one run in twelve, always in the same assertion about the order timestamp. It assumes the fixture clock is frozen; on a slow runner the second ticks over between setup and assertion.

Fixed in !309 by pinning the clock. See [[repo-layout]] for where the fixtures live.`},
}

// --- Costs ---

func seedCosts(ctx context.Context, pool *pgxpool.Pool, agentIDs map[string]uuid.UUID, rnd *rand.Rand) error {
	// Fourteen days of history. The amounts are chosen so the curve
	// breathes — a smooth history looks invented, because then it is.
	activity := map[string]float64{
		"ada": 1.0, "kilo": 0.85, "nova": 0.5, "vera": 0.45,
		"iris": 0.3, "otto": 0.25, "felix": 0.15,
	}
	models := map[string]string{
		"ada": "claude-opus-5", "kilo": "claude-opus-5", "nova": "claude-sonnet-5",
		"vera": "claude-sonnet-5", "iris": "claude-sonnet-5", "otto": "claude-sonnet-5",
		"felix": "claude-opus-5",
	}
	for slug, id := range agentIDs {
		weight := activity[slug]
		model := models[slug]
		for day := 13; day >= 0; day-- {
			// Less happens on the weekend — the instance should look as if it
			// hung off an organisation that has weekends.
			d := time.Now().AddDate(0, 0, -day)
			factor := weight
			if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
				factor *= 0.2
			}
			if slug == "felix" && day < 6 {
				factor = 0 // stopped, since the audit runs
			}
			runs := int(float64(2+rnd.Intn(5)) * factor)
			for i := 0; i < runs; i++ {
				// Order of magnitude of a real run: an agent that works a ticket
				// from start to end reads far more than it writes — and costs
				// one to three dollars, not cents.
				in := 60000 + rnd.Intn(340000)
				out := 2000 + rnd.Intn(13000)
				usd := float64(in)/1e6*3 + float64(out)/1e6*15
				if model == "claude-sonnet-5" {
					usd = float64(in)/1e6*0.9 + float64(out)/1e6*4.5
				}
				if _, err := pool.Exec(ctx, `INSERT INTO cost_entries
					(agent_id, usd, input_tokens, output_tokens, model, created_at)
					VALUES ($1,$2,$3,$4,$5, now() - make_interval(days => $6, hours => $7))`,
					id, usd, in, out, model, day, rnd.Intn(10)+8); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// --- Recording ---
// A complete run in the format the Claude Code adapter delivers (spec/12):
// wake events, access brokering, turns with tool calls, approval,
// completion. The activity view builds its narrative from these.

func seedRecording(ctx context.Context, pool *pgxpool.Pool, orgID, agentID uuid.UUID) error {
	var taskID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM backlog_tasks WHERE agent_id=$1 AND state='done'
		ORDER BY updated_at DESC LIMIT 1`, agentID).Scan(&taskID); err != nil {
		return fmt.Errorf("kein abgeschlossener Task für die Aufzeichnung: %w", err)
	}

	session := "sess_" + uuid.New().String()[:8]
	tool := func(n int) string { return fmt.Sprintf("toolu_demo%02d", n) }

	type evt struct {
		kind    string
		payload map[string]any
	}
	assistant := func(text string, calls ...map[string]any) map[string]any {
		content := []any{}
		if text != "" {
			content = append(content, map[string]any{"type": "text", "text": text})
		}
		for _, c := range calls {
			content = append(content, c)
		}
		return map[string]any{
			"type": "assistant", "uuid": uuid.New().String(), "session_id": session,
			"message": map[string]any{
				"id": "msg_" + uuid.New().String()[:10], "role": "assistant", "type": "message",
				"model": "claude-opus-5", "content": content,
			},
		}
	}
	use := func(id, name string, input map[string]any) map[string]any {
		return map[string]any{"type": "tool_use", "id": id, "name": name, "input": input}
	}
	result := func(id, content string) map[string]any {
		return map[string]any{
			"type": "user", "uuid": uuid.New().String(), "session_id": session,
			"message": map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "tool_result", "tool_use_id": id, "content": content, "is_error": false},
			}},
		}
	}

	events := []evt{
		{"lifecycle", map[string]any{"status": "triggered"}},
		{"lifecycle", map[string]any{"status": "working"}},
		{"credential", map[string]any{"granted": true, "system": "zammad", "ttl_secs": 900, "proactive": true}},
		{"runtime", assistant(
			"Ticket #4791 asks which delivery windows the Meridian Freight contract covers. That is an account question, so I look it up in my own notes before I go anywhere near the ticket system.",
			use(tool(1), "covey_wiki_read", map[string]any{"slug": "meridian-freight"}))},
		{"runtime", result(tool(1), "# Meridian Freight (account)\n\nFreight customer, contract since 2024. Delivery windows are contractual:\nTuesday and Thursday, 06:00–11:00. …")},
		{"runtime", assistant(
			"The windows are contractual: Tuesday and Thursday, 06:00–11:00. I will quote the clause rather than paraphrase it, and read the ticket to see what exactly they asked.",
			use(tool(2), "zammad_ticket_read", map[string]any{"ticket": 4791}))},
		{"runtime", result(tool(2), "Ticket 4791 — \"Which delivery days are we entitled to?\"\nCustomer: dispatch@meridian-freight.example\nGroup: Logistics · State: open")},
		{"approval", map[string]any{"decision": "auto-allow", "action": "zammad:reply_internal"}},
		{"runtime", assistant(
			"They ask about entitlement, not about a single shipment — so the answer is the clause plus where to find it. No date, no exception, and nothing about the missed delivery last week, which is a separate ticket.",
			use(tool(3), "zammad_ticket_reply", map[string]any{
				"ticket": 4791,
				"body":   "Your contract covers deliveries on Tuesday and Thursday between 06:00 and 11:00 (clause 4.2). …",
			}))},
		{"runtime", result(tool(3), "Reply posted to ticket 4791 (article 18213), visible to the customer.")},
		{"action", map[string]any{"system": "zammad", "action": "ticket.reply", "ref": "4791", "ok": true}},
		{"runtime", assistant("Answered from the contract and linked the clause. Writing the account page back with the question count, since this is the third time they have asked.",
			use(tool(4), "covey_wiki_append", map[string]any{"slug": "meridian-freight", "text": "Asked about delivery windows again on 2026-08-02 — third time this quarter."}))},
		{"runtime", result(tool(4), "Appended to meridian-freight.")},
		{"lifecycle", map[string]any{"status": "task_done"}},
		{"runtime", map[string]any{
			"type": "result", "uuid": uuid.New().String(), "session_id": session,
			"subtype": "success", "is_error": false, "num_turns": 4, "total_cost_usd": 0.2143,
			"result": "Answered from the account page in the wiki, linked the contract clause.",
			"usage":  map[string]any{"input_tokens": 41208, "output_tokens": 1355},
		}},
		{"lifecycle", map[string]any{"status": "sleeping"}},
	}

	// The events lie seconds apart, the run itself around two days back —
	// matching the task it hangs on.
	for i, e := range events {
		payload, err := json.Marshal(e.payload)
		if err != nil {
			return err
		}
		if _, err := pool.Exec(ctx, `INSERT INTO recording_events (org_id, agent_id, task_id, kind, payload, created_at)
			VALUES ($1,$2,$3,$4,$5, now() - make_interval(hours => 30, secs => $6))`,
			orgID, agentID, taskID, e.kind, payload, (len(events)-i)*7); err != nil {
			return err
		}
	}
	return nil
}
