package cli

import (
	"bufio"
	"fmt"
	"log"
	"slices"
	"strconv"
	"strings"

	"github.com/mattthew/optine/internals/dataTypes"
	"github.com/mattthew/optine/internals/provider"
)

var providers = []string{
	"Ollama",
	"vLLM",
	"your mom",
}

func handleError(err error) error {
	log.Println(err)
	return err
}

func selectOption(reader *bufio.Reader, maxRange int, dataSlice []string) (string, error) {
	for range maxRange {
		log.Print("> ")

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

	log.Println("Select a provider:")

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

		var agentConf = dataTypes.AgentConfig{
			Provider:          "Ollama",
			Model:             selectedModel,
			NativeToolCalling: hasTools,
		}

		// run agent interface
		_, interfaceErr := agentInterface(reader, agentConf)

		if interfaceErr != nil {

			return handleError(interfaceErr)

		}

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

		var agentConf = dataTypes.AgentConfig{
			Provider:          "vLLM",
			Model:             modelName,
			NativeToolCalling: nativeToolCall,
		}

		// run agent interface
		_, interfaceErr := agentInterface(reader, agentConf)

		if interfaceErr != nil {

			return handleError(interfaceErr)

		}

	case "your mom":
		log.Println("She got some nice milk jars yo")
	}

	// wont ever reach
	return nil
}

func selectModel(modelSlice []string, reader *bufio.Reader) (string, error) {

	for i, model := range modelSlice {
		log.Printf("%d. %s\n", i+1, model)
	}

	selectedModel, err := selectOption(reader, 5, modelSlice)

	if err != nil {
		return "", err

	}

	return selectedModel, nil

}
