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

// pickSession opens the session picker. It is a variable so tests can drive
// /resume without a live TUI.
var pickSession = tui.PickSession

// notifyModelChanged updates the active TUI's provider/model line. It is a
// variable so tests can observe the call without a live TUI.
var notifyModelChanged = tui.NotifyModelChanged

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
		modelSlice, err := provider.OllamaGetModelData(context.Background())

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

		modelMetaData, err := provider.OllamaGetModelMetaData(context.Background(), selectedModel)

		if err != nil {
			log.Println("selector conn ref run")
			return handleError(err)

		}

		hasTools := slices.Contains(modelMetaData.Capabilities, "tools")

		log.Println("Tool calling available:", hasTools)
		log.Println("Capablities: ", modelMetaData.Capabilities)

		var ctxLimit int
		if err := withSpinner(fmt.Sprintf("Loading %s into Ollama", selectedModel), func() error {
			cl, err := provider.OllamaContextLength(context.Background(), selectedModel)
			if err != nil {
				return err
			}
			ctxLimit = cl
			return nil
		}); err != nil {
			return fmt.Errorf("loading model %q in Ollama: %w", selectedModel, err)
		}

		log.Printf("Model %s loaded", selectedModel)
		return RunAgentSession("Ollama", selectedModel, hasTools, ctxLimit, "", false, true)

	case "vLLM":
		modelName, err := provider.VLLMGetModelData(context.Background())

		if err != nil {
			return err
		}

		log.Println("Initializing model: ", modelName)

		nativeToolCall, err := provider.VLLMRunSmokeTest(context.Background(), modelName)

		if err != nil {
			return handleError(err)

		}

		log.Println("Tool calling available:", nativeToolCall)

		contextLimit, err := provider.VLLMGetModelContextLength(context.Background())
		if err != nil {
			log.Printf("vLLM context window probe failed: %v", err)
		}
		return RunAgentSession("vLLM", modelName, nativeToolCall, contextLimit, "", false, true)
	}

	return nil
}

// runAgentSession owns the session for the whole TUI lifetime.
func RunAgentSession(providerName string, modelName string, nativeToolCalling bool, contextLimit int, sessionUidfromCli string, fromCLI bool, localOnly bool) error {
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
		LocalOnly:    localOnly,
		ToolCall:     nativeToolCalling,
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
			// Only holds limits that are cheap to get (OpenRouter, vLLM).
			// Ollama limits are resolved lazily after selection.
			modelContextLimits := make(map[string]int)

			ollamaModelNames, err := provider.OllamaGetModelData(ctx)
			if err == nil {
				for _, model := range ollamaModelNames {
					hasTools := false
					meta, metaErr := provider.OllamaGetModelMetaData(ctx, model)
					if metaErr == nil {
						hasTools = slices.Contains(meta.Capabilities, "tools")
					}

					modelProviders[model] = "Ollama"
					modelToolSupport[model] = hasTools
				}
			}

			if !sess.LocalOnly {
				for _, model := range provider.AllOpenrouterModels {
					modelProviders[model.ID] = "Openrouter"
					modelToolSupport[model.ID] = slices.Contains(model.SupportedParameters, "tools")
					modelContextLimits[model.ID] = model.ContextLength
				}
			}

			modelName, vllmErr := provider.VLLMGetModelData(ctx)
			if vllmErr == nil && modelName != "" {
				hasTools, smokeErr := provider.VLLMRunSmokeTest(ctx, modelName)
				if smokeErr != nil {
					log.Printf("vLLM smoke test failed: %v", smokeErr)
					hasTools = false
				}
				ctxLimit, ctxErr := provider.VLLMGetModelContextLength(ctx)
				if ctxErr != nil || ctxLimit <= 0 {
					log.Printf("vLLM context window probe failed: %v", ctxErr)
					ctxLimit = 4096
				}
				modelProviders[modelName] = "vLLM"
				modelToolSupport[modelName] = hasTools
				modelContextLimits[modelName] = ctxLimit
			}

			ids := make([]string, 0, len(modelProviders))
			for id := range modelProviders {
				ids = append(ids, id)
			}
			sort.Strings(ids)

			selected, _ := tui.PickModel(ids)

			if selected != "" {
				sess.Provider = modelProviders[selected]
				sess.Model = selected
				sess.Kind = harnessSession.KindFor(sess.Provider, modelToolSupport[selected])

				var ctxLimit int
				if sess.Provider == "Ollama" {
					// Loads the model into memory, so run it for the selected model only.
					tui.NotifyModelLoading(true, selected)
					cl, clErr := provider.OllamaContextLength(ctx, selected)
					tui.NotifyModelLoading(false, "")
					if clErr != nil {
						log.Printf("ollama context length for %q: %v", selected, clErr)
					} else {
						ctxLimit = cl
					}
				} else {
					ctxLimit = modelContextLimits[selected]
				}
				if ctxLimit <= 0 {
					ctxLimit = 4096
				}

				sess.ContextLimit = ctxLimit
				nextNativeToolSupport := modelToolSupport[selected]

				if sess.ToolCall != nextNativeToolSupport {
					if err := harnessSession.ReplaceSessionSysPrompt(nextNativeToolSupport, sess); err != nil {
						log.Printf("error occurred while replacing the session system prompt: %v", err)
					}
				}

				agentConf.Provider = sess.Provider
				agentConf.Model = sess.Model
				agentConf.NativeToolCalling = modelToolSupport[selected]

				tui.NotifyModelChanged(agentConf.Provider, agentConf.Model)
			}

			return tui.AgentResult{}, nil
		}

		if msg == "/resume" || strings.HasPrefix(msg, "/resume ") {
			next, lines := resumeSession(sess.LocalOnly)
			for _, line := range lines {
				log.Println(line)
			}

			if next == nil {
				return tui.AgentResult{}, nil
			}

			sess = next

			agentConf.Provider = sess.Provider
			agentConf.Model = sess.Model
			agentConf.NativeToolCalling = sess.ToolCall

			// Replace the pane with the newly-loaded conversation.
			return tui.AgentResult{
				Messages: tui.TranscriptMessages(next.Transcript()),
			}, nil
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

// resumeSession lists the saved sessions, lets the user pick one, and loads
// it. It returns the loaded session and any status lines to log; a nil session
// means the user cancelled or there was nothing to resume.
func resumeSession(localAgent bool) (*harnessSession.Session, []string) {
	metas, err := harnessSession.List("", false)
	if err != nil {
		return nil, []string{"cannot read saved sessions: " + err.Error()}
	}

	eligible := make([]harnessSession.Meta, 0, len(metas))

	switch localAgent {
	case false:
		for _, meta := range metas {
			if meta.LocalOnly {
				continue
			}

			eligible = append(eligible, meta)
		}
	case true:
		for _, meta := range metas {
			if !meta.LocalOnly {
				continue
			}

			eligible = append(eligible, meta)
		}
	}

	if len(eligible) == 0 {
		return nil, []string{"no saved sessions for this provider and directory"}
	}

	selected, ok := pickSession(eligible)
	if !ok || selected == "" {
		return nil, nil
	}

	next, err := harnessSession.Load(selected)
	if err != nil {
		return nil, []string{"cannot load session " + selected + ": " + err.Error()}
	}

	if harnessSession.OnActiveSession != nil {
		harnessSession.OnActiveSession(next.UID)
	}

	notifyModelChanged(next.Provider, next.Model)

	return next, nil
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
