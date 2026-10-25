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
//
// Every run re-reads casual memory and crawler availability; the prompt is
// only rebuilt when one of those inputs actually changed, so an unchanged
// memory file costs one read and zero prompt churn. This is what makes user
// edits to the notebook visible on the very next message.
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

	c.refreshBase()          //refresh and builds the system prompt and the web crawlers
	c.refreshMemory()        //refreshes the casual memory file and sets the data and the path to it in c
	c.injected = c.compose() //compiles the base with the memory, crawlers etc and returns a ready to use final sysprompt

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

// refreshMemory reads the notebook. A failure keeps the previous snapshot so
// the model keeps its last known memory instead of losing it mid-session.
// Best-effort by design: memory problems never abort a run.
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

// SyncSystem re-reads the notebook and crawler availability and rebuilds the
// system message in place only when the contents actually changed. It reports
// whether the injected system message changed.
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

	// Defensive action; the changed being true should already dictate the c.Injected ( fully built sys prompt ) is not the same anymore.
	if next == c.injected {
		return false
	}

	c.injected = next
	s.setSystemContent(next)

	return true
}
