package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"slices"
	"strings"

	"github.com/mattthew/optine/internal/dataTypes"
	"github.com/mattthew/optine/internal/harnessPermissions"
	"github.com/mattthew/optine/internal/provider"
)

// un-authorized API key error
var invalidApiKey = fmt.Errorf("Invalid API key provided, please use a valid API key.")

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

// selectConnection prompts for an API provider and stores its key.
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

	case "Openrouter":
		log.Printf("You have selected Openrouter, please input the API key.")
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
		return fmt.Errorf("error occured while trying to read the auth.json file, err: %w ", err)
	}

	jsonErr := json.Unmarshal(data, &fileContent)
	if jsonErr != nil {
		return fmt.Errorf("error occured while trying to unmarshal the auth.json file, err: %w ", jsonErr)
	}

	supportedProviderList := make([]string, 0, len(fileContent.Connections))

	for provider := range fileContent.Connections {
		if _, ok := modelProviders[strings.ToLower(provider)]; !ok {
			continue
		}
		supportedProviderList = append(supportedProviderList, provider)
	}

	if len(supportedProviderList) == 0 {
		return fmt.Errorf("no model providers found, please use /connect and connect to the provider with your API key")
	}

	log.Println("<model providers>")
	for _, supportedProvider := range supportedProviderList {
		log.Println(supportedProvider)
	}
	selectedConnection, selectErr := selectOption(reader, 5, supportedProviderList)

	if selectErr != nil {
		return fmt.Errorf("err: %w ", selectErr)
	}

	connection := fileContent.Connections[strings.ToLower(selectedConnection)]

	apiKey := connection.APIKey

	log.Printf("Authorizing the key for %s", selectedConnection)

	authErr := checkApiAuthentication(selectedConnection, apiKey)

	if authErr != nil {
		if errors.Is(authErr, invalidApiKey) {
			return invalidApiKey
		}
		return fmt.Errorf("err: %w ", authErr)
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

		return RunAgentSession("Openrouter", modelData.ID, hasTools, modelData.ContextLength, "", false)

	case "openai":
		return fmt.Errorf("under development")

	case "anthropic":
		return fmt.Errorf("under development")
	default:
		return fmt.Errorf("unrecognized model provider, please select a valid and supported model provider")
	}
}
