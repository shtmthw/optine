package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/mattthew/optine/internal/dataTypes"
	"github.com/mattthew/optine/internal/harnessPermissions"
	"github.com/mattthew/optine/internal/provider"
)

// un-authorized API key error
var invalidApiKey = fmt.Errorf("Invalid API key provided, please use a valid API key.")

// selectConnection runs /connect: pick a service and store its API key.
func selectConnection(reader *bufio.Reader) error {
	selectedConnection, err := chooseOption(reader, "Connect a service", connections)
	if err != nil {
		return err
	}

	apiKey, err := readAPIKey(reader, connectionLabel(selectedConnection))
	if err != nil {
		return err
	}

	if err := saveConnection(selectedConnection, apiKey); err != nil {
		return fmt.Errorf("saving the %s key: %w", connectionLabel(selectedConnection), err)
	}

	launcherOut.ok("saved the %s key to %s", connectionLabel(selectedConnection), harnessPermissions.AuthPath)

	switch strings.ToLower(selectedConnection) {
	case "openrouter":
		launcherOut.info("run /remote to start a session on an OpenRouter model")
	case "tavily":
		launcherOut.info("web_search can now use the tavily crawler in new and resumed sessions")
	}

	return nil
}

// connectionLabel is the display name for a connection key.
func connectionLabel(name string) string {
	switch strings.ToLower(name) {
	case "openrouter":
		return "OpenRouter"
	case "openai":
		return "OpenAI"
	case "anthropic":
		return "Anthropic"
	case "tavily":
		return "Tavily"
	}

	return name
}

var modelProviders = map[string]struct{}{
	"openrouter": {},
	"openai":     {},
	"anthropic":  {},
}

func filterModelProviderAvailability(reader *bufio.Reader) error {
	authPath := harnessPermissions.AuthPath
	var fileContent dataTypes.AuthConfig

	data, err := os.ReadFile(authPath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", authPath, err)
	}

	jsonErr := json.Unmarshal(data, &fileContent)
	if jsonErr != nil {
		return fmt.Errorf("%s is not valid JSON: %w", authPath, jsonErr)
	}

	supportedProviderList := make([]string, 0, len(fileContent.Connections))

	for provider := range fileContent.Connections {
		if _, ok := modelProviders[strings.ToLower(provider)]; !ok {
			continue
		}
		supportedProviderList = append(supportedProviderList, provider)
	}

	if len(supportedProviderList) == 0 {
		return fmt.Errorf("no model provider keys saved yet; run /connect and add an OpenRouter key")
	}

	sort.Strings(supportedProviderList)

	options := make([]option, len(supportedProviderList))
	for i, name := range supportedProviderList {
		options[i] = option{value: name, label: connectionLabel(name)}
	}

	selectedConnection, err := chooseOption(reader, "Remote provider", options)
	if err != nil {
		return err
	}

	apiKey := fileContent.Connections[strings.ToLower(selectedConnection)].APIKey

	authErr := checkApiAuthentication(strings.ToLower(selectedConnection), apiKey)
	if authErr != nil {
		if errors.Is(authErr, invalidApiKey) {
			return invalidApiKey
		}
		return authErr
	}

	return nil
}

func checkApiAuthentication(providerName, apiKey string) error {
	switch providerName {
	case "openrouter":
		var modelData dataTypes.OpenRouterModel

		err := withSpinner("Authorizing OpenRouter key", func() error {
			model, validateErr := provider.ValidateOpenRouterKey(apiKey)
			if validateErr != nil {
				return validateErr
			}
			modelData = model
			return nil
		})
		if err != nil {
			return err
		}

		hasTools := slices.Contains(modelData.SupportedParameters, "tools")
		launcherOut.ok("OpenRouter key authorized; starting on %s (/model switches)", modelData.ID)
		reportToolSupport(modelData.ID, hasTools)

		return RunAgentSession("Openrouter", modelData.ID, hasTools, modelData.ContextLength, "", false, false)

	case "openai", "anthropic":
		return fmt.Errorf("the %s provider is still in development; its key is stored for when it lands", connectionLabel(providerName))
	default:
		return fmt.Errorf("unrecognized model provider, please select a valid and supported model provider")
	}
}
