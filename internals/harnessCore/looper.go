package harnessCore

import (
	"bufio"
	"context"
	"fmt"
	"log"

	"github.com/mattthew/optine/internals/dataTypes"
)

func AgentLoop(userMessage string, agentConfig dataTypes.AgentConfig, reader *bufio.Reader) (string, error) {

	ctx := context.Background()

	switch agentConfig.NativeToolCalling {
	case true:

		// native tool path, pass the agentConfig.Provider
		result, err := nativeToolAgentCall(ctx, reader, agentConfig.Provider, agentConfig.Model, userMessage)

		if err != nil {
			log.Println("error occured in nonNativeToolCall looper, err: ", err)
			return "", err
		}

		return result, nil

	case false:
		// envelope path pass the agentConfig.Provider

	default:
		return "", fmt.Errorf("unsupported provider: %s", agentConfig.Provider)
	}
	return "", nil
}
