package doctor

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// checkAccounts asks the three questions the account/seat migrations (0058,
// 0059) can fail on. They used to live in the `covey doctor` subcommand and
// moved here when the subcommand became a thin printer around this package —
// otherwise they would have been answerable only from a shell on the host,
// which is precisely the shape this package exists to end.
//
// All three are about the same moment: a login is about to become a thing of
// its own, separate from the seat in an organisation. Where the data does not
// permit that unambiguously, the migration refuses rather than guessing — and
// a refusal arrives as "the service does not come up", at a point when nobody
// is looking for a database question.
func (d *doctor) checkAccounts(ctx context.Context, pool *pgxpool.Pool) {
	// 1. Addresses that differ only in spelling: two rows in humans, one login
	//    in accounts — which would hand one person another's access.
	rows, err := pool.Query(ctx, `SELECT lower(email), count(*) FROM humans
		GROUP BY lower(email) HAVING count(*) > 1`)
	if err == nil {
		defer rows.Close()
		var found int
		for rows.Next() {
			var email string
			var n int
			if err := rows.Scan(&email, &n); err == nil {
				found++
				d.problem("duplicate address",
					fmt.Sprintf("%s exists %d× in humans, in different spellings", email, n),
					"deduplicate the seats before upgrading", true)
			}
		}
		if found == 0 {
			d.ok("addresses", "no seat exists twice under different spellings")
		}
	}

	// 2. A self-registered account on the address of an existing seat.
	//
	// Which question is the right one depends on the state of the database,
	// and that is not a nicety: BEFORE 0059 there is no humans.account_id and
	// a query against it errors — and a swallowed error is worse than no check
	// here, because it looks exactly like a passed one. AFTER 0059 every seat
	// carries an account with the same address, so the unconditional query
	// would be nothing but false alarms.
	var linked bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns
		WHERE table_name='humans' AND column_name='account_id')`).Scan(&linked); err != nil {
		d.problem("accounts", "not readable: "+err.Error(), "", false)
		return
	}
	query := `SELECT a.email FROM accounts a JOIN humans h ON lower(h.email) = a.email`
	if linked {
		query += ` WHERE h.account_id IS NULL`
	}
	arows, err := pool.Query(ctx, query)
	if err != nil {
		// accounts exists only from 0058 — on an older database the question is
		// moot, not unanswered.
		d.ok("accounts", "no accounts table yet — this installation predates the sign-up work")
	} else {
		defer arows.Close()
		var found int
		for arows.Next() {
			var email string
			if err := arows.Scan(&email); err == nil {
				found++
				d.problem("account and seat",
					email+" has a self-registered account AND an existing seat",
					"check which is the person, delete the other", true)
			}
		}
		if found == 0 {
			d.ok("accounts", "no self-registered account collides with a seat")
		}
	}

	// 3. Who administers the instance? After the upgrade, tenant management
	//    hangs on the answer.
	var admins, orgs int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM accounts WHERE platform_role='system_admin'`).Scan(&admins)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM organizations`).Scan(&orgs)
	switch {
	case admins > 0:
		d.ok("instance administration", fmt.Sprintf("%d system administrator(s)", admins))
	case orgs == 1:
		d.ok("instance administration",
			"none yet — the upgrade appoints the org_admin of the single organisation")
	default:
		d.problem("instance administration",
			fmt.Sprintf("no system administrator, and %d organisations", orgs),
			"appoint one with `covey system-admin add <email>` — nobody can administer the instance otherwise",
			false)
	}
}
