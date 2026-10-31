package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/mattthew/optine/internal/dataTypes"
	"github.com/mattthew/optine/internal/harnessCore"
	"github.com/mattthew/optine/internal/harnessPermissions"
	"github.com/mattthew/optine/internal/harnessSession"
	"github.com/mattthew/optine/internal/provider"
	"github.com/mattthew/optine/internal/tui"
)

var providers = []string{
	"Ollama",
	"vLLM",
}

var connections = []string{
	"Tavily",
	"OpenAI",
	"Anthropic",
	"Openrouter",
}

func handleError(err error) error {
	log.Println(err)
	return err
}

func selectOption(reader *bufio.Reader, maxRange int, dataSlice []string) (string, error) {
	for range maxRange {

		input, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}

		input = strings.TrimSpace(input)

		n, err := strconv.Atoi(input)
		if err != nil || n < 1 || n > len(dataSlice) {
			log.Println("Invalid selection")
			continue
		}
		return dataSlice[n-1], nil
	}
	return "", fmt.Errorf("too many invalid attempts")
}

func selectProvider(reader *bufio.Reader) error {

	log.Println("<provider list>")

	for i, provider := range providers {
		log.Printf("%d. %s\n", i+1, provider)
	}

	selectedProvider, err := selectOption(reader, 5, providers)

	if err != nil {
		return handleError(err)
	}

	switch selectedProvider {
	case "Ollama":
		modelSlice, err := provider.OllamaGetModelData()

		if err != nil {
			return err

		}
		selectedModel, err := selectModel(modelSlice, reader)

		if err != nil {
			return handleError(err)

		}

		if selectedModel == "" {
			log.Println("Model name cant be empty, this is the backends fault not users")
			return fmt.Errorf("model name cant be empty, this is the backends fault not users")
		}

		modelMetaData, err := provider.OllamaGetModelMetaData(selectedModel)

		if err != nil {
			log.Println("selector conn ref run")
			return handleError(err)

		}

		hasTools := slices.Contains(modelMetaData.Capabilities, "tools")

		log.Println("Tool calling available:", hasTools)
		log.Println("Capablities: ", modelMetaData.Capabilities)

		if err := withSpinner(fmt.Sprintf("Loading %s into Ollama", selectedModel), func() error {
			return provider.OllamaLoadModel(selectedModel)
		}); err != nil {
			return fmt.Errorf("loading model %q in Ollama: %w", selectedModel, err)
		}

		log.Printf("Model %s loaded", selectedModel)
		return RunAgentSession("Ollama", selectedModel, hasTools, modelMetaData.ContextLength(selectedModel), "", false)

	case "vLLM":
		modelName, err := provider.VLLMGetModelData()

		if err != nil {
			return err
		}

		log.Println("Initializing model: ", modelName)

		nativeToolCall, err := provider.VLLMRunSmokeTest(modelName)

		if err != nil {
			return handleError(err)

		}

		log.Println("Tool calling available:", nativeToolCall)

		contextLimit, err := provider.VLLMGetModelContextLength()
		if err != nil {
			log.Printf("vLLM context window probe failed: %v", err)
		}
		return RunAgentSession("vLLM", modelName, nativeToolCall, contextLimit, "", false)
	}

	return nil
}

// runAgentSession owns the session for the whole TUI lifetime.
func RunAgentSession(providerName string, modelName string, nativeToolCalling bool, contextLimit int, sessionUidfromCli string, fromCLI bool) error {
	// Tools run against the process working directory, so that is the
	// session's Directory.
	CWD, wdErr := os.Getwd()
	if wdErr != nil {
		log.Printf("working directory unavailable: %v", wdErr)
		CWD = "unknown"
	}

	agentConf := dataTypes.AgentConfig{
		Provider:          providerName,
		Model:             modelName,
		NativeToolCalling: nativeToolCalling,
	}

	sess, err := sessionForRun(harnessSession.Config{
		Provider:     providerName,
		Model:        modelName,
		Directory:    CWD,
		Kind:         harnessSession.KindFor(providerName, nativeToolCalling),
		ContextLimit: contextLimit,
		MaxTurns:     harnessCore.MaxTurns,
	}, sessionUidfromCli)
	if err != nil {
		return handleError(err)
	}

	var initialMessages []tui.Message
	if strings.TrimSpace(sessionUidfromCli) != "" {
		initialMessages = tui.TranscriptMessages(sess.Transcript())
	}

	log.Printf("session %s running (%s, %s) in %s", sess.UID, providerName, modelName, sess.Directory)

	configDir := filepath.Dir(harnessPermissions.AuthPath)
	agent := func(ctx context.Context, modelProvider string, modelName string, msg string, r *bufio.Reader) (tui.AgentResult, error) {
		msg = strings.TrimSpace(msg)

		if msg == "/model" {
			modelProviders := make(map[string]string)
			modelToolSupport := make(map[string]bool)
			modelContextLimits := make(map[string]int)

			ollamaModelNames, err := provider.OllamaGetModelData()
			if err == nil {
				for _, model := range ollamaModelNames {
					meta, metaErr := provider.OllamaGetModelMetaData(model)
					hasTools := false
					ctxLimit := 0
					if metaErr == nil {
						hasTools = slices.Contains(meta.Capabilities, "tools")
						ctxLimit = meta.ContextLength(model)
					}
					modelProviders[model] = "Ollama"
					modelToolSupport[model] = hasTools
					modelContextLimits[model] = ctxLimit
				}
			}

			for _, model := range provider.AllOpenrouterModels {
				modelProviders[model.ID] = "Openrouter"
				modelToolSupport[model.ID] = slices.Contains(model.SupportedParameters, "tools")
				modelContextLimits[model.ID] = model.ContextLength
			}

			ids := make([]string, 0, len(modelProviders))
			for id := range modelProviders {
				ids = append(ids, id)
			}
			sort.Strings(ids)

			selected, _ := tui.PickModel(ids)

			if selected != "" {
				selectedProvider := modelProviders[selected]
				selectedNative := modelToolSupport[selected]
				selectedContext := modelContextLimits[selected]

				agentConf.Provider = selectedProvider
				agentConf.Model = selected
				agentConf.NativeToolCalling = selectedNative

				if selectedProvider != sess.Provider {
					ctxLimit := selectedContext
					if ctxLimit <= 0 {
						ctxLimit = sess.ContextLimit
					}
					newSess, err := sessionForRun(harnessSession.Config{
						Provider:     selectedProvider,
						Model:        selected,
						Directory:    sess.Directory,
						Kind:         harnessSession.KindFor(selectedProvider, selectedNative),
						ContextLimit: ctxLimit,
						MaxTurns:     harnessCore.MaxTurns,
					}, "")
					if err == nil {
						sess = newSess
						if harnessSession.OnActiveSession != nil {
							harnessSession.OnActiveSession(newSess.UID)
						}
					}
				} else {
					sess.Model = selected
				}

				sess.Provider = selectedProvider
				sess.Model = selected
				if selectedContext > 0 {
					sess.ContextLimit = selectedContext
				}

				tui.NotifyModelChanged(agentConf.Provider, agentConf.Model)
			}

			return tui.AgentResult{}, nil
		}
		if msg == "/resume" || strings.HasPrefix(msg, "/resume ") {
			next, lines := resumeSession(sess, strings.TrimSpace(strings.TrimPrefix(msg, "/resume")))
			if next != nil {
				sess = next

				// Replace the pane with the newly-loaded conversation.
				return tui.AgentResult{
					Messages: tui.TranscriptMessages(next.Transcript()),
				}, nil
			}

			for _, line := range lines {
				log.Println(line)
			}

			return tui.AgentResult{}, nil
		}

		// Only the latest message's files are reverted; multi-message reversion
		// and transcript undo are not handled here.
		if msg == "/revert" {

			if sess.TotalUserMessages == 0 {
				log.Println("Can not revert files in a session without messages.")
				return tui.AgentResult{}, nil
			}

			revertedPaths, err := revertLatestSnapshot(sess, sess.Directory, configDir)
			if err != nil {
				if errors.Is(err, errNoRevertSnapshotPair) {
					log.Println("No completed snapshot pair is available to revert.")
				} else {
					log.Printf("error occurred while trying to revert in %s, err: %v", sess.Directory, err)
				}
				return tui.AgentResult{}, nil
			}

			if len(revertedPaths) == 0 {
				log.Println("No file changes were made, thus no files reverted.")
			} else {
				for _, revertedPath := range revertedPaths {
					log.Printf("Reverted: %s", revertedPath)
				}
			}

			return tui.AgentResult{}, nil

		}

		if strings.HasPrefix(msg, "/") {
			RunCommand(msg, true, r)
			return tui.AgentResult{}, nil
		}

		answer, err := harnessCore.AgentLoop(ctx, sess, agentConf, msg, r)
		return tui.AgentResult{Response: answer}, err
	}

	if ifaceErr := tui.Run(agentConf.Provider, agentConf.Model, CWD, initialMessages, agent); ifaceErr != nil {
		return handleError(ifaceErr)
	}

	return nil
}

// resumeSession loads a saved session without blocking on input. Listing and
// picking are plain text because an approval prompt cannot run here.
func resumeSession(current *harnessSession.Session, arg string) (*harnessSession.Session, []string) {
	metas, err := harnessSession.List("", false)
	if err != nil {
		return nil, []string{"cannot read saved sessions: " + err.Error()}
	}

	eligible := make([]harnessSession.Meta, 0, len(metas))

	for _, meta := range metas {
		if meta.Kind != current.Kind || meta.Provider != current.Provider || meta.UID == current.UID || meta.Directory != current.Directory {
			continue
		}

		eligible = append(eligible, meta)
	}

	if len(eligible) == 0 {
		return nil, []string{"no other saved sessions for this provider, tool mode and directory"}
	}

	if arg == "" {
		lines := []string{"saved sessions (newest first):"}

		for i, meta := range eligible {
			line := fmt.Sprintf(
				"  %d. %s  %s  %d messages  saved %s",
				i+1, meta.UID, meta.Model, meta.Messages, meta.UpdatedAt.Format("2006-01-02 15:04"),
			)

			lines = append(lines, line)
		}

		lines = append(lines, "load one with /resume <number|uid|last>")

		return nil, lines
	}

	pick := func(uid string) (*harnessSession.Session, []string) {
		next, err := harnessSession.Load(uid)
		if err != nil {
			return nil, []string{"cannot load session " + uid + ": " + err.Error()}
		}

		if err := next.Resumeable(current.Kind); err != nil {
			return nil, []string{err.Error()}
		}

		lines := []string{fmt.Sprintf("resumed session %s — %d messages", next.UID, next.Len())}
		if next.Model != current.Model {
			lines = append(lines, fmt.Sprintf("model changed from %s to %s", current.Model, next.Model))
		}

		// Keep the session record aligned with where tools actually run.
		switch {
		case next.Directory == "":
			next.Directory = current.Directory
		case next.Directory != current.Directory:
			return nil, []string{fmt.Sprintf(
				"session %s was started in %s; resume it from that directory",
				next.UID, next.Directory,
			)}
		}

		if harnessSession.OnActiveSession != nil {
			harnessSession.OnActiveSession(next.UID)
		}

		return next, lines
	}

	if arg == "last" {
		return pick(eligible[0].UID)
	}

	if n, err := strconv.Atoi(arg); err == nil {
		if n < 1 || n > len(eligible) {
			return nil, []string{fmt.Sprintf("no session %d (choose 1-%d, or /resume to list)", n, len(eligible))}
		}

		return pick(eligible[n-1].UID)
	}

	matches := make([]harnessSession.Meta, 0, 1)

	for _, meta := range eligible {
		if strings.HasPrefix(meta.UID, arg) {
			matches = append(matches, meta)
		}
	}

	switch len(matches) {
	case 0:
		return nil, []string{"no saved session matches " + arg}
	case 1:
		return pick(matches[0].UID)
	default:
		return nil, []string{fmt.Sprintf("%q matches %d sessions; use more characters", arg, len(matches))}
	}
}

func selectModel(modelSlice []string, reader *bufio.Reader) (string, error) {
	log.Println("<model list>")
	for i, model := range modelSlice {
		log.Printf("%d. %s\n", i+1, model)
	}

	selectedModel, err := selectOption(reader, 5, modelSlice)

	if err != nil {
		return "", err

	}

	return selectedModel, nil

}
