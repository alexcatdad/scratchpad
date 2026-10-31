package httpapi

import (
	"context"
	"database/sql"
	"net/http"
	"time"
)

// Build identifies the running server binary. Release builds stamp Version and
// Commit with -ldflags; source builds report "dev" and an empty commit.
type Build struct {
	Version string
	Commit  string
}

// About reports the server build and storage to an authenticated owner or
// machine session, so clients can show which server they reached.
func (s *SSH) About(build Build, githubConfigured bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		tx, err := s.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			s.fail(w, err)
			return
		}
		defer tx.Rollback()
		if _, err = apiActor(ctx, tx, r, s.Origin); err != nil {
			s.fail(w, err)
			return
		}
		JSON(w, 200, map[string]any{
			"version":          build.Version,
			"commit":           build.Commit,
			"database":         s.Store.Engine,
			"schemaVersion":    s.Store.Version,
			"githubConfigured": githubConfigured,
		})
	}
}
