package harnessSession

import (
	"log"
	"strings"
	"time"

	"github.com/mattthew/optine/internal/harnessMemory"
	"github.com/mattthew/optine/internal/harnessTools"
	"github.com/mattthew/optine/internal/systemPrompts"
)

// systemCache keeps the system message next to the snapshot it was built from.
// The prompt is rebuilt only when memory or crawler availability changes.
type systemCache struct {
	kind       Kind
	promptTime time.Time
	maxTurns   int

	base         string
	memoryPath   string
	memoryData   string
	crawlerStamp string

	injected string
}

// Indirection so tests can drive the cache without touching the real notebook.
var (
	casualMemoryPath     = harnessMemory.CasualMemoryPath
	readCasualMemoryFile = harnessMemory.ReadCasualMemoryFile
	availableCrawlers    = harnessTools.CrawlersAvailable
)

func newSystemCache(kind Kind, maxTurns int) systemCache {
	c := systemCache{
		kind:       kind,
		promptTime: time.Now(),
		maxTurns:   maxTurns,
	}

	c.refreshBase()
	c.refreshMemory()
	c.injected = c.compose()

	return c
}

func buildBasePrompt(kind Kind, now time.Time, maxTurns int, crawlers []string) string {
	if kind == Envelope {
		return systemPrompts.NonNativeToolSystemPrompt(now, maxTurns, crawlers)
	}

	return systemPrompts.NativeToolSystemPrompt(now, crawlers)
}

func crawlerStamp(crawlers []string) string {
	return strings.Join(crawlers, ",")
}

func (c *systemCache) refreshBase() {
	crawlers := availableCrawlers()

	c.base = buildBasePrompt(c.kind, c.promptTime, c.maxTurns, crawlers)
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
// message only when the contents changed.
func (s *Session) SyncSystem() bool {
	c := &s.SystemCache

	changed := c.refreshMemory()

	if crawlerStamp(availableCrawlers()) != c.crawlerStamp {
		c.refreshBase()
		changed = true
	}

	if !changed {
		return false
	}

	next := c.compose()

	if next == c.injected {
		return false
	}

	c.injected = next
	s.setSystemContent(next)

	return true
}
