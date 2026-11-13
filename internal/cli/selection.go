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
	"github.com/mattthew/optine/internal/slashCommands"
	"github.com/mattthew/optine/internal/tui"
)

// providers are the local model servers /local can start on.
var providers = []option{
	{value: "Ollama", label: "Ollama   localhost:11434"},
	{value: "vLLM", label: "vLLM     localhost:8000"},
}

// connections are the services /connect can store an API key for.
var connections = []option{
	{value: "Openrouter", label: "OpenRouter   hosted models for /remote"},
	{value: "Tavily", label: "Tavily       hosted search backend for web_search"},
	{value: "OpenAI", label: "OpenAI       key is stored; the provider is still in development"},
	{value: "Anthropic", label: "Anthropic    key is stored; the provider is still in development"},
}

// pickSession opens the session picker. It is a variable so tests can drive
// /resume without a live TUI.
var pickSession = tui.PickSession

// notifyModelChanged updates the active TUI's provider/model line. It is a
// variable so tests can observe the call without a live TUI.
var notifyModelChanged = tui.NotifyModelChanged

func selectOption(reader *bufio.Reader, maxRange int, dataSlice []string) (string, error) {
	for range maxRange {

		input, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}

		input = strings.TrimSpace(input)

		n, err := strconv.Atoi(input)
		if err != nil || n < 1 || n > len(dataSlice) {
			fmt.Fprintf(os.Stderr, "enter a number from 1 to %d: ", len(dataSlice))
			continue
		}
		return dataSlice[n-1], nil
	}
	return "", fmt.Errorf("too many invalid attempts")
}

// selectProvider runs /local: pick a local server, pick a model, probe it,
// and start a session on it.
func selectProvider(reader *bufio.Reader) error {
	selectedProvider, err := chooseOption(reader, "Local provider", providers)
	if err != nil {
		return err
	}

	switch selectedProvider {
	case "Ollama":
		modelSlice, err := provider.OllamaGetModelData(context.Background())
		if err != nil {
			return fmt.Errorf("cannot reach Ollama on localhost:11434 (is `ollama serve` running?): %w", err)
		}

		if len(modelSlice) == 0 {
			return errors.New("Ollama has no models yet; pull one with `ollama pull <model>` and try again")
		}

		selectedModel, err := selectModel(modelSlice, reader)
		if err != nil {
			return err
		}

		modelMetaData, err := provider.OllamaGetModelMetaData(context.Background(), selectedModel)
		if err != nil {
			return fmt.Errorf("reading %s's capabilities from Ollama: %w", selectedModel, err)
		}

		hasTools := slices.Contains(modelMetaData.Capabilities, "tools")
		reportToolSupport(selectedModel, hasTools)

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

		launcherOut.ok("%s loaded (%d token context)", selectedModel, ctxLimit)
		return RunAgentSession("Ollama", selectedModel, hasTools, ctxLimit, "", false, true)

	case "vLLM":
		modelName, err := provider.VLLMGetModelData(context.Background())
		if err != nil {
			return fmt.Errorf("cannot reach vLLM on localhost:8000 (is the server running?): %w", err)
		}

		var nativeToolCall bool
		if err := withSpinner(fmt.Sprintf("Checking %s for tool calling", modelName), func() error {
			ok, err := provider.VLLMRunSmokeTest(context.Background(), modelName)
			nativeToolCall = ok
			return err
		}); err != nil {
			return fmt.Errorf("vLLM tool-calling check for %s: %w", modelName, err)
		}

		reportToolSupport(modelName, nativeToolCall)

		contextLimit, err := provider.VLLMGetModelContextLength(context.Background())
		if err != nil {
			launcherOut.warn("could not read the vLLM context window (%v); using the default", err)
		}

		return RunAgentSession("vLLM", modelName, nativeToolCall, contextLimit, "", false, true)
	}

	return nil
}

// reportToolSupport tells the user which tool protocol the session will use.
func reportToolSupport(model string, native bool) {
	if native {
		launcherOut.ok("%s supports native tool calling", model)
		return
	}

	launcherOut.warn("%s has no native tool calling; optine will drive tools through its JSON envelope protocol", model)
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
		return err
	}

	// /local sessions stay on local models, so they get a different /model
	// and /resume than /remote ones. A resumed session keeps the mode it was
	// saved with, whatever the caller passed.
	scope := slashCommands.RemoteSession
	if sess.LocalOnly {
		scope = slashCommands.LocalSession
	}

	var initialMessages []tui.Message
	if strings.TrimSpace(sessionUidfromCli) != "" {
		initialMessages = tui.TranscriptMessages(sess.Transcript())
	}

	launcherOut.info("session %s · %s · %s · %s", sess.UID, providerName, modelName, sess.Directory)

	configDir := filepath.Dir(harnessPermissions.AuthPath)

	// pendingRewind is the message number /rewind picked (0 if none). The view
	// is already cut there; the files and the saved history are rewound
	// before the next message runs.
	pendingRewind := 0

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

				tui.NotifyModelChanged(agentConf.Provider, agentConf.Model, sess.UID)
			}

			return tui.AgentResult{}, nil
		}
		if msg == "/new" {

			if sess.TotalUserMessages <= 0 {
				log.Println("Already in a new session.")
				return tui.AgentResult{}, nil
			}
			// Start from what config was being used for the very last model
			newSess, err := sessionForRun(harnessSession.Config{
				Provider:     agentConf.Provider,
				Model:        agentConf.Model,
				Directory:    CWD,
				Kind:         harnessSession.KindFor(agentConf.Provider, agentConf.NativeToolCalling),
				ContextLimit: sess.ContextLimit,
				MaxTurns:     harnessCore.MaxTurns,
				LocalOnly:    sess.LocalOnly,
				ToolCall:     agentConf.NativeToolCalling,
			}, "")
			if err != nil {
				log.Printf("error occurred while shifting to a new session: %v", err)
				return tui.AgentResult{}, nil
			}
			sess = newSess
			pendingRewind = 0
			notifyModelChanged(sess.Provider, sess.Model, sess.UID)

			return tui.AgentResult{
				Messages: tui.TranscriptMessages(sess.Transcript()),
			}, nil

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
			pendingRewind = 0

			agentConf.Provider = sess.Provider
			agentConf.Model = sess.Model
			agentConf.NativeToolCalling = sess.ToolCall

			// Replace the pane with the newly-loaded conversation.
			return tui.AgentResult{
				Messages: tui.TranscriptMessages(next.Transcript()),
			}, nil
		}

		// re-does the /revert'ed changes
		// Only the latest message's files are reverted or redone; multi-message
		// reversion and transcript undo are not handled here.
		if msg == "/revert" || msg == "/redo" {
			restoreLatestRun(sess, configDir, msg == "/redo")
			return tui.AgentResult{}, nil
		}

		// The picker cuts the view at once; the rewind itself waits for the
		// next message (see rewind.go).
		if msg == "/rewind" {
			var result tui.AgentResult
			pendingRewind, result = openRewind(sess, pendingRewind)
			return result, nil
		}

		if strings.HasPrefix(msg, "/") {
			RunCommand(msg, true, r)
			return tui.AgentResult{}, nil
		}

		if pendingRewind > 0 {
			if err := applyRewind(sess, configDir, pendingRewind); err != nil {
				return tui.AgentResult{}, fmt.Errorf("rewind failed, so the message was not sent; send it again to retry, or /resume to drop the rewind: %w", err)
			}
			pendingRewind = 0
		}

		answer, err := harnessCore.AgentLoop(ctx, sess, agentConf, msg, r)
		return tui.AgentResult{Response: answer}, err
	}

	if err := tui.Run(agentConf.Provider, agentConf.Model, CWD, sess.UID, initialMessages, slashCommands.For(scope), agent); err != nil {
		if errors.Is(err, tui.ErrQuit) {
			return err
		}
		return fmt.Errorf("agent interface: %w", err)
	}

	// A session is written on its first message; an empty one leaves nothing
	// to resume.
	if sess.RunCount > 0 {
		launcherOut.info("left session %s.", sess.UID)
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

	notifyModelChanged(next.Provider, next.Model, next.UID)

	return next, nil
}

func selectModel(modelSlice []string, reader *bufio.Reader) (string, error) {
	return chooseOption(reader, "Model", plainOptions(modelSlice))
}
