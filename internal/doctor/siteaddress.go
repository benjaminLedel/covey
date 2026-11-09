package doctor

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"covey/internal/settings"
)

// checkSiteAddress reports an installation without a configured site address.
// Password-reset and confirmation mails carry a one-time token in a link, and
// that link is built only on a configured address — taken from the request it
// would be the requester's to choose (#693). Without one, those mails are not
// sent: nobody can reset a password or confirm a registration.
func (d *doctor) checkSiteAddress(ctx context.Context, pool *pgxpool.Pool) {
	if strings.TrimSpace(d.cfg.SiteURL) != "" {
		d.ok("site address", strings.TrimSpace(d.cfg.SiteURL))
		return
	}
	var v string
	_ = pool.QueryRow(ctx, `SELECT value FROM system_settings WHERE key=$1`, settings.SiteURL).Scan(&v)
	if strings.TrimSpace(v) != "" {
		d.ok("site address", strings.TrimSpace(v)+" (setting site.url)")
		return
	}
	d.problem("site address", "not configured — password-reset and confirmation mails are not sent",
		"set COVEY_SITE_URL, or the setting site.url under Platform → Settings", false)
}
