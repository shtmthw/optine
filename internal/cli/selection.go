package cli

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/mattthew/optine/internal/dataTypes"
	"github.com/mattthew/optine/internal/harnessCore"
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

func handleAPIkeyInput(reader *bufio.Reader, selectedConnection string) error {

	apiKey, err := reader.ReadString('\n')

	if err != nil {
		return err
	}

	apiKey = strings.TrimSpace(apiKey)

	errSavingConnection := saveConnection(selectedConnection, apiKey)

	if errSavingConnection != nil {
		return errSavingConnection
	}
	return nil
}

// shows the use the range of available services they can connect to
func selectConnection(reader *bufio.Reader) error {
	log.Println("<connector list>")

	for i, coonection := range connections {
		log.Printf("%d. %s\n", i+1, coonection)
	}

	selectedConnection, err := selectOption(reader, 5, connections)

	if err != nil {
		return handleError(err)
	}

	switch selectedConnection {
	case "Tavily":

		log.Printf("You have selected Tavily, please input the API key.")

		err := handleAPIkeyInput(reader, selectedConnection)
		if err != nil {
			return handleError(err)
		}
		log.Printf("Key set successfully.")

	case "OpenAI":
		log.Printf("You have selected OpenAI, please input the API key.")
		err := handleAPIkeyInput(reader, selectedConnection)
		if err != nil {
			return handleError(err)
		}
		log.Printf("Key set successfully.")

	case "Anthropic":
		log.Printf("You have selected Anthropic, please input the API key.")
		err := handleAPIkeyInput(reader, selectedConnection)
		if err != nil {
			return handleError(err)
		}
		log.Printf("Key set successfully.")

	}

	return nil
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
		//call Ollama probing
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
		return runAgentSession("Ollama", selectedModel, hasTools, modelMetaData.ContextLength(selectedModel), "")

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

		return runAgentSession("vLLM", modelName, nativeToolCall, contextLimit, "")
	}

	// wont ever reach
	return nil
}

// runAgentSession owns the session for the whole TUI lifetime. One session,
// one provider, one loop: history is never translated between formats.
func runAgentSession(providerName string, modelName string, nativeToolCalling bool, contextLimit int, sessionUidfromCli string) error {
	// The agent's tools run against the process working directory, so that is
	// the session's Directory: where reads, edits and commands will land.
	var sess *harnessSession.Session

	CWD, wdErr := os.Getwd()
	if wdErr != nil {
		log.Printf("working directory unavailable: %v", wdErr)
		CWD = "unknown"
	}
	sess = harnessSession.New(harnessSession.Config{
		Provider:     providerName,
		Model:        modelName,
		Directory:    CWD,
		Kind:         harnessSession.KindFor(providerName, nativeToolCalling),
		ContextLimit: contextLimit,
		MaxTurns:     harnessCore.MaxTurns,
	})

	agentConf := dataTypes.AgentConfig{
		Provider:          providerName,
		Model:             modelName,
		NativeToolCalling: nativeToolCalling,
	}

	if strings.TrimSpace(sessionUidfromCli) != "" {
		next, err := harnessSession.Load(sessionUidfromCli)
		if err != nil {
			return handleError(err)
		}
		sess = next

	}

	log.Printf("session %s running (%s, %s) in %s", sess.UID, providerName, modelName, sess.Directory)

	agent := func(ctx context.Context, provider string, modelName string, msg string, r *bufio.Reader) (string, error) {
		msg = strings.TrimSpace(msg)

		if msg == "/resume" || strings.HasPrefix(msg, "/resume ") {
			next, lines := resumeSession(sess, strings.TrimSpace(strings.TrimPrefix(msg, "/resume")))
			if next != nil {
				// if resumeable successfully the curr sess get overwritten with the fetched sess
				sess = next
			}

			for _, line := range lines {
				log.Println(line)
			}

			return "", nil
		}

		if strings.HasPrefix(msg, "/") {
			RunCommand(msg, true, r)
			return "", nil
		}

		return harnessCore.AgentLoop(ctx, sess, agentConf, msg, r)
	}

	if ifaceErr := tui.Run(agentConf.Provider, agentConf.Model, CWD, agent); ifaceErr != nil {
		return handleError(ifaceErr)
	}

	return nil
}

// resumeSession loads a saved session without blocking on input: the TUI can
// only answer approval prompts while the harness is blocked on one, so a
// selection prompt here would hang. Listing and picking are both plain text.
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
		return nil, []string{"no other saved sessions for this provider and tool mode"}
	}

	if arg == "" {
		lines := []string{"saved sessions (newest first):"}

		for i, meta := range eligible {
			line := fmt.Sprintf(
				"  %d. %s  %s  %d messages  saved %s",
				i+1, meta.UID, meta.Model, meta.Messages, meta.UpdatedAt.Format("2006-01-02 15:04"),
			)

			if meta.Directory != "" && meta.Directory != current.Directory {
				line += "  [" + meta.Directory + "]"
			}

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

		// Tools always run in the process working directory, so keep the
		// session record truthful when it was forcefully started started somewhere else.
		// as its already coded to not show sessions that does not match the CWD
		switch {
		case next.Directory == "":
			next.Directory = current.Directory
		case next.Directory != current.Directory:
			lines = append(lines, fmt.Sprintf(
				"session was started in %s; tools keep running in %s", next.Directory, current.Directory,
			))
			next.Directory = current.Directory
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
