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

	// Snapshots and tools must share one working directory. Older sessions may
	// not have stored one, so adopt the current directory for those.
	switch {
	case next.Directory == "":
		next.Directory = cfg.Directory
	case next.Directory != cfg.Directory:
		return nil, fmt.Errorf(
			"session %s was started in %q; resume it from that directory",
			next.UID, next.Directory,
		)
	}

	return next, nil
}
