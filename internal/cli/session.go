package cli

import (
	"fmt"
	"strings"

	"github.com/mattthew/optine/internal/harnessSession"
	"github.com/mattthew/optine/internal/harnessSnapshotter"
)

// gitAvailable is a var so tests can make a resume fail if it probes git.
var gitAvailable = harnessSnapshotter.GitAvailable

// sessionForRun either restores a saved session or creates a new one. Git is
// probed only for a brand-new session; a resumed session keeps the snapshot
// backend stored in its metadata even if git has appeared or disappeared since.
func sessionForRun(cfg harnessSession.Config, sessionUID string) (*harnessSession.Session, error) {
	sessionUID = strings.TrimSpace(sessionUID)
	if sessionUID == "" {
		cfg.GitStatus = gitAvailable()
		return harnessSession.New(cfg), nil
	}

	next, err := harnessSession.Load(sessionUID)
	if err != nil {
		return nil, err
	}

	// Align the loaded session to the active config so the looper kind guard
	// never fires from a decoupled saved kind/provider pair. The session runs
	// under cfg.Provider; Commit persists the aligned provider.
	next.Provider = cfg.Provider
	next.ReconcileKind()

	// Snapshots and tools must share one working directory.
	if next.Directory != cfg.Directory {
		return nil, fmt.Errorf(
			"session %s was started in %q; resume it from that directory",
			next.UID, next.Directory,
		)
	}

	return next, nil
}
