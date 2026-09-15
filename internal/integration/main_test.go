package integration

import (
	"context"
	"os"
	"testing"

	"covey/internal/db"
)

// TestMain drops the migrated template once the suite is done (tmpl in
// stack_test.go). A run that crashes leaves it behind, the same way it leaves
// the per-test databases of that run; its name starts with covey_tpl_.
func TestMain(m *testing.M) {
	code := m.Run()
	if tmpl.name != "" {
		ctx := context.Background()
		if admin, err := db.Connect(ctx, adminDBURL); err == nil {
			admin.Exec(ctx, "DROP DATABASE "+tmpl.name+" WITH (FORCE)")
			admin.Close()
		}
	}
	os.Exit(code)
}
