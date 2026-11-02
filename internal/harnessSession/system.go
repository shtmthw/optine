package harnessSession

import (
	"log"
	"strings"
	"time"

	"github.com/mattthew/optine/internal/harnessMemory"
	"github.com/mattthew/optine/internal/harnessTools"
	"github.com/mattthew/optine/internal/systemPrompts"
)

// systemCache keeps the system prompt's inputs next to the snapshot they were
// built from. The composed prompt itself lives only in Session.SystemPrompt;
// the cache is rebuilt only when memory, crawler availability, or the tool
// protocol changes.
type systemCache struct {
	promptTime time.Time
	maxTurns   int
	toolCall   bool

	base         string
	memoryPath   string
	memoryData   string
	crawlerStamp string
}

// Indirection so tests can drive the cache without touching the real notebook.
var (
	casualMemoryPath     = harnessMemory.CasualMemoryPath
	readCasualMemoryFile = harnessMemory.ReadCasualMemoryFile
	availableCrawlers    = harnessTools.CrawlersAvailable
)

func newSystemCache(maxTurns int, toolCall bool) systemCache {
	c := systemCache{
		promptTime: time.Now(),
		maxTurns:   maxTurns,
	}

	c.refreshBase(toolCall, availableCrawlers())
	c.refreshMemory()

	return c
}

func buildBasePrompt(now time.Time, maxTurns int, crawlers []string, toolCall bool) string {
	if !toolCall {
		return systemPrompts.NonNativeToolSystemPrompt(now, maxTurns, crawlers)
	}

	return systemPrompts.NativeToolSystemPrompt(now, crawlers)
}

func crawlerStamp(crawlers []string) string {
	return strings.Join(crawlers, ",")
}

func (c *systemCache) refreshBase(toolCall bool, crawlers []string) {
	c.toolCall = toolCall
	c.base = buildBasePrompt(c.promptTime, c.maxTurns, crawlers, toolCall)
	c.crawlerStamp = crawlerStamp(crawlers)
}

// refreshMemory reads the notebook. A failure keeps the previous snapshot.
func (c *systemCache) refreshMemory() bool {
	path, err := casualMemoryPath()
	if err != nil {
		log.Printf("session system prompt: casual memory path unavailable: %v", err)
		return false
	}

	data, err := readCasualMemoryFile()
	if err != nil {
		log.Printf("session system prompt: casual memory unavailable: %v", err)
		return false
	}

	if path == c.memoryPath && data == c.memoryData {
		return false
	}

	c.memoryPath = path
	c.memoryData = data

	return true
}

func (c *systemCache) compose() string {
	if c.memoryPath == "" {
		return c.base
	}

	return systemPrompts.WithCasualMemory(c.base, c.memoryPath, c.memoryData)
}

// SyncSystem re-reads memory and crawler availability, rebuilding the system
// prompt only when the contents or the tool protocol changed.
func (s *Session) SyncSystem() bool {
	c := &s.SystemCache

	changed := c.refreshMemory()

	crawlers := availableCrawlers()
	if crawlerStamp(crawlers) != c.crawlerStamp || s.ToolCall != c.toolCall {
		c.refreshBase(s.ToolCall, crawlers)
		changed = true
	}

	if !changed {
		return false
	}

	next := c.compose()

	if next == s.SystemPrompt {
		return false
	}

	s.SystemPrompt = next

	return true
}
