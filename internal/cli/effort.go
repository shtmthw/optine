package cli

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/mattthew/optine/internal/harnessSession"
	"github.com/mattthew/optine/internal/provider"
	"github.com/mattthew/optine/internal/tui"
)

// These are variables so tests can drive /effort without a live TUI or a
// model server.
var (
	pickEffort          = tui.PickEffort
	notifyEffortChanged = tui.NotifyEffortChanged
	effortSupportFor    = provider.EffortSupportFor
)

// refreshEffortSupport works out what the session's current model accepts
// and keeps it on the session. It returns a notice when the saved level is
// not available on this model, or "" when there is nothing to say. A model
// whose support cannot be read is treated as having no effort control, so
// nothing is sent to it.
func refreshEffortSupport(ctx context.Context, sess *harnessSession.Session) string {
	support, err := effortSupportFor(ctx, sess.Provider, sess.Model)
	if err != nil {
		log.Printf("effort levels for %s: %v", sess.Model, err)
		support = harnessSession.EffortSupport{}
	}
	sess.EffortSupport = support
	notifyEffortChanged(headerEffort(sess))

	return effortFallbackNotice(sess)
}

// headerEffort is the level the header shows: the saved one while the model
// takes it, default while it is kept but not sent.
func headerEffort(sess *harnessSession.Session) string {
	if !sess.EffortSupport.Accepts(sess.Effort) {
		return harnessSession.EffortDefault.String()
	}

	return sess.Effort.String()
}

// effortFallbackNotice says when the saved level is kept but not sent.
func effortFallbackNotice(sess *harnessSession.Session) string {
	if sess.EffortSupport.Accepts(sess.Effort) {
		return ""
	}

	return fmt.Sprintf("%s is not available on %s; using the model default", sess.Effort, sess.Model)
}

// runEffort handles /effort and /effort <level>. With no level it opens the
// picker over what the model accepts. It returns the lines for the command's
// output box.
func runEffort(sess *harnessSession.Session, arg string) []string {
	support := sess.EffortSupport
	if !support.Supported() {
		lines := []string{sess.Model + " has no effort setting"}
		if support.Note != "" {
			lines = append(lines, support.Note)
		}
		if sess.Effort != harnessSession.EffortDefault {
			lines = append(lines, fmt.Sprintf("the saved level %s comes back on a model that has one", sess.Effort))
		}
		return lines
	}

	var level harnessSession.Effort
	if arg = strings.TrimSpace(arg); arg == "" {
		picked, ok := pickEffort(effortChoices(sess))
		if !ok {
			return nil
		}
		level, _ = harnessSession.ParseEffort(picked)
	} else {
		parsed, ok := harnessSession.ParseEffort(arg)
		if !ok || !support.Accepts(parsed) {
			return []string{fmt.Sprintf("%s does not take effort %q; it accepts %s", sess.Model, arg, acceptedLevels(support))}
		}
		level = parsed
	}

	sess.Effort = level
	notifyEffortChanged(level.String())

	// The level is saved with the session; a session that has not run yet
	// is written on its first message instead.
	if err := harnessSession.Commit(sess); err != nil {
		log.Printf("session %s: could not save the effort level: %v", sess.UID, err)
	}

	switch level {
	case harnessSession.EffortDefault:
		return []string{fmt.Sprintf("Effort set to default for %s; the model decides", sess.Model)}
	case harnessSession.EffortOff:
		return []string{fmt.Sprintf("Effort set to off for %s; thinking is turned off", sess.Model)}
	}

	return []string{fmt.Sprintf("Effort set to %s for %s", level, sess.Model)}
}

// effortChoices are the picker rows: the current level first, then default
// and the model's levels lowest first.
func effortChoices(sess *harnessSession.Session) []tui.EffortChoice {
	support := sess.EffortSupport

	rows := make([]tui.EffortChoice, 0, len(support.Levels)+1)
	for _, level := range append([]harnessSession.Effort{harnessSession.EffortDefault}, support.Levels...) {
		row := tui.EffortChoice{Level: level.String(), Label: effortLabel(level, support), Current: level == sess.Effort}
		if row.Current {
			rows = append([]tui.EffortChoice{row}, rows...)
			continue
		}
		rows = append(rows, row)
	}

	return rows
}

// effortLabel is a level's picker row. Off says what it does, and default
// names the model's own default when the provider reports it.
func effortLabel(level harnessSession.Effort, support harnessSession.EffortSupport) string {
	switch {
	case level == harnessSession.EffortOff:
		return "off (turns thinking off)"
	case level == harnessSession.EffortDefault && support.Default != harnessSession.EffortDefault:
		return fmt.Sprintf("default (%s)", support.Default)
	}

	return level.String()
}

// acceptedLevels lists what /effort <level> takes on this model.
func acceptedLevels(support harnessSession.EffortSupport) string {
	names := []string{harnessSession.EffortDefault.String()}
	for _, level := range support.Levels {
		names = append(names, level.String())
	}

	return strings.Join(names, ", ")
}
