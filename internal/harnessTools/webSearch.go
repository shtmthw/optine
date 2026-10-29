package harnessTools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/mattthew/optine/internal/harnessPermissions"
)

var crawlerHTTPClient = &http.Client{
	Timeout: 15 * time.Second,
}

// Shared by both crawlers: Tavily's results use the same title/url/content fields.
type crawlerResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content"`
}

type crawlerResponse struct {
	Results []*crawlerResult `json:"results"`
}

// SearchWeb runs a query through the named crawler.
func SearchWeb(ctx context.Context, crawlerName string, query string) (string, error) {
	const maxResults = 5

	var req *http.Request

	switch crawlerName {
	case "tavily":
		apiKey, err := harnessPermissions.GetAPIKey(crawlerName)
		if err != nil {
			return "", fmt.Errorf("error occured while getting the API key, err: %w", err)
		}

		payload, err := json.Marshal(map[string]any{
			"query":        query,
			"search_depth": "basic",
			"max_results":  maxResults,
		})
		if err != nil {
			return "", fmt.Errorf(
				"building tavily payload: %w",
				err,
			)
		}

		req, err = http.NewRequestWithContext(
			ctx,
			http.MethodPost,
			"https://api.tavily.com/search",
			bytes.NewReader(payload),
		)
		if err != nil {
			return "", fmt.Errorf(
				"building tavily request: %w",
				err,
			)
		}

		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("Content-Type", "application/json")

	case "searxng":
		endpoint, err := url.Parse(
			strings.TrimRight(getSearXNGBaseURL(), "/") + "/search",
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

		req, err = http.NewRequestWithContext(
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

	default:
		return "", fmt.Errorf(
			"unknown crawler %q (use \"tavily\" or \"searxng\")",
			crawlerName,
		)
	}

	resp, err := crawlerHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf(
			"calling %s: %w",
			crawlerName,
			err,
		)
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)

		return "", fmt.Errorf(
			"%s returned HTTP %d: %s",
			crawlerName,
			resp.StatusCode,
			strings.TrimSpace(string(body)),
		)
	}

	var parsed crawlerResponse

	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf(
			"decoding %s response: %w",
			crawlerName,
			err,
		)
	}

	if len(parsed.Results) == 0 {
		return "No search results were found.", nil
	}

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

// CrawlersAvailable lists the crawlers configured for this session.
func CrawlersAvailable() []string {
	crawlers := []string{"searxng"}

	if _, err := harnessPermissions.GetAPIKey("tavily"); err == nil {
		crawlers = append(crawlers, "tavily")
	}

	return crawlers
}
