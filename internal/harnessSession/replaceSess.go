package harnessSession

import "errors"

// ReplaceSessionSysPrompt switches the session to the tool protocol selected by
// toolCall, rebuilds its system prompt, and persists it so the next inference
// and any later resume use the new prompt.
func ReplaceSessionSysPrompt(toolCall bool, activeSession *Session) error {
	if activeSession == nil {
		return errors.New("nil session")
	}

	activeSession.Kind = KindFor(activeSession.Provider, toolCall)
	activeSession.ToolCall = toolCall
	activeSession.SystemCache.refreshBase(toolCall, availableCrawlers())
	activeSession.SystemPrompt = activeSession.SystemCache.compose()

	if err := Commit(activeSession); err != nil {
		return err
	}

	return nil
}
