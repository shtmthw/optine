package harnessTools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

var searxHTTPClient = &http.Client{
	Timeout: 15 * time.Second,
}

type searxResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content"`
}

type searxResponse struct {
	Results []*searxResult `json:"results"`
}

func SearchWeb(ctx context.Context, query string) (string, error) {
	baseURL := getSearXNGBaseURL()
	endpoint, err := url.Parse(
		strings.TrimRight(baseURL, "/") + "/search",
	)
	if err != nil {
		return "", fmt.Errorf(
			"building searxng URL: %w",
			err,
		)
	}
	params := endpoint.Query()
	params.Set("q", query)
	params.Set("format", "json")

	endpoint.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		endpoint.String(),
		nil,
	)
	if err != nil {
		return "", fmt.Errorf(
			"building searxng request: %w",
			err,
		)
	}

	resp, err := searxHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf(
			"calling searxng: %w",
			err,
		)
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)

		return "", fmt.Errorf(
			"searxng returned HTTP %d: %s",
			resp.StatusCode,
			strings.TrimSpace(string(body)),
		)
	}

	var parsed searxResponse

	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf(
			"decoding searxng response: %w",
			err,
		)
	}

	if len(parsed.Results) == 0 {
		return "No search results were found.", nil
	}

	const maxResults = 5

	if len(parsed.Results) > maxResults {
		parsed.Results = parsed.Results[:maxResults]
	}

	var builderString strings.Builder

	for i, result := range parsed.Results {
		if result == nil {
			continue
		}

		fmt.Fprintf(
			&builderString,
			"%d. %s\nURL: %s\nContent: %s\n\n",
			i+1,
			result.Title,
			result.URL,
			result.Content,
		)
	}
	resultText := strings.TrimSpace(builderString.String())

	if resultText == "" {
		return "Search returned no usable results.", nil
	}

	return resultText, nil
}

func getSearXNGBaseURL() string {
	if value := strings.TrimSpace(
		os.Getenv("SEARXNG_BASE_URL"),
	); value != "" {
		return value
	}

	// Default for a Docker Compose service named "searxng".
	return "http://localhost:8080"
}
