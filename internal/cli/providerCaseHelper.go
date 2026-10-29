package cli

import (
	"fmt"

	"github.com/mattthew/optine/internal/provider"
)

// checkProviderAvailability confirms the selected provider is reachable before
// resuming a session.
func checkProviderAvailability(providerName, modelName string) error {

	switch providerName {

	case "Ollama":
		if err := withSpinner(fmt.Sprintf("Loading %s into Ollama", modelName), func() error {
			return provider.OllamaLoadModel(modelName)
		}); err != nil {
			return fmt.Errorf("error occured while trying to communicate with the local Ollama server, err: %v", err)
		}

	case "vLLM":
		_, err := provider.VLLMGetModelData()
		if err != nil {
			return fmt.Errorf("error occured while trying to communicate with the local vLLM server, err: %v", err)
		}
	default:
		return fmt.Errorf("Unrecognized provider, please select any supported providers or connect using /connect.")
	}
	return nil
}
