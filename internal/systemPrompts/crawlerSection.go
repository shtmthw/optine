package systemPrompts

import (
	"strings"
)

// crawlerSection renders the shared web_search crawler teaching. Keep it in
// sync with dataTypes/tools.go and CrawlersAvailable.
func crawlerSection(crawlers []string) string {
	if len(crawlers) == 0 {
		return "web_search has no usable crawlers right now. Do not call it."
	}

	var b strings.Builder

	b.WriteString("web_search crawlers\n")
	b.WriteString("web_search takes one required argument, crawler, which picks the backend.\n")
	b.WriteString("tavily is hosted, gives the best ranked quality and fits news and general public lookups. It sends the query to a third party and uses an API key stored in auth.json; if it fails with quota or key problems, retry once with searxng.\n")
	b.WriteString("searxng is the user's own instance: private, nothing leaves this machine, no quota; use it for anything private or internal.\n")
	b.WriteString("\nAvailable crawlers this session: ")
	b.WriteString(strings.Join(crawlers, ", "))
	b.WriteString(".\nNever pick a crawler that is not in that line.\nResults come back as a numbered list of titles, URLs and page snippets.")

	return b.String()
}
