package ingest

import (
	_ "embed"
	"encoding/json"
	"math"
	"regexp"
	"strings"
	"sync"
	"time"

	coredb "github.com/spencer-life/ai-tracker/internal/db"
)

// The snapshot is intentionally offline and deterministic. Rates and the
// codex-auto-review date mapping are derived from ccusage v20.0.18's pinned
// LiteLLM/models.dev data and built-in fallbacks.
const pricingVersion = "official-2026-07-25+ccusage-v20.0.18-standard"

type Pricing struct {
	Input                float64 `json:"input"`
	CacheRead            float64 `json:"cacheRead"`
	CacheWrite           float64 `json:"cacheWrite"`
	Output               float64 `json:"output"`
	LongContextThreshold int64   `json:"longContextThreshold,omitempty"`
	LongInput            float64 `json:"longInput,omitempty"`
	LongCacheRead        float64 `json:"longCacheRead,omitempty"`
	LongCacheWrite       float64 `json:"longCacheWrite,omitempty"`
	LongOutput           float64 `json:"longOutput,omitempty"`
}

//go:embed pricing.json
var embeddedPricing []byte

var (
	pricingOnce sync.Once
	pricingMap  map[string]Pricing
)

func loadPricing() {
	pricingOnce.Do(func() {
		pricingMap = map[string]Pricing{}
		_ = json.Unmarshal(embeddedPricing, &pricingMap)
	})
}

func CostFor(model string, tokens coredb.TokenCounts) (*int64, string) {
	return CostForAt(model, tokens, time.Time{})
}

// CostForAt returns a standard-tier API-equivalent estimate in microdollars.
// It is not a subscription invoice. Unknown models remain nil so callers can
// report pricing coverage instead of silently treating them as free.
func CostForAt(model string, tokens coredb.TokenCounts, occurredAt time.Time) (*int64, string) {
	return CostForAtWithCacheDuration(model, tokens, 0, occurredAt)
}

// CostForAtWithCacheDuration preserves Claude's more expensive one-hour cache
// writes when the source log reports their duration breakdown.
func CostForAtWithCacheDuration(model string, tokens coredb.TokenCounts, cacheWrite1h int64, occurredAt time.Time) (*int64, string) {
	loadPricing()
	resolved := resolvePricingModel(model, occurredAt)
	p, ok := pricingMap[resolved]
	if !ok {
		return nil, pricingVersion
	}
	// Gemini context-cache creation also incurs token-hour storage charges. The
	// local coding-agent logs do not retain duration, so fail closed when such a
	// bucket is present rather than emit an incomplete cost.
	if strings.HasPrefix(resolved, "gemini-") && tokens.CacheWrite > 0 {
		return nil, pricingVersion
	}
	// Anthropic published a scheduled Sonnet 5 standard-price change. Historical
	// events retain the rate in force on their occurrence date.
	if resolved == "claude-sonnet-5" && !occurredAt.IsZero() && !occurredAt.Before(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)) {
		p.Input, p.CacheRead, p.CacheWrite, p.Output = 3, 0.3, 3.75, 15
	}
	inputTokens := tokens.InputUncached + tokens.CacheRead + tokens.CacheWrite
	if p.LongContextThreshold > 0 && inputTokens > p.LongContextThreshold {
		p.Input, p.CacheRead, p.CacheWrite, p.Output = p.LongInput, p.LongCacheRead, p.LongCacheWrite, p.LongOutput
	}
	// Reasoning is retained as a useful source counter, but Codex can report it
	// as a subset of output_tokens. Billing it separately would double charge.
	if cacheWrite1h < 0 || cacheWrite1h > tokens.CacheWrite {
		cacheWrite1h = 0
	}
	cacheWrite5m := tokens.CacheWrite - cacheWrite1h
	value := float64(tokens.InputUncached)*p.Input + float64(tokens.CacheRead)*p.CacheRead + float64(cacheWrite5m)*p.CacheWrite + float64(cacheWrite1h)*(2*p.Input) + float64(tokens.Output)*p.Output
	micros := int64(math.Round(value)) // token * USD-per-million equals microdollars.
	return &micros, pricingVersion
}

var pricingAliases = map[string]string{
	"chatgpt-5.4":                        "gpt-5.4",
	"terra-5.6":                          "gpt-5.6-terra",
	"moonshotai/kimi-k2.6":               "moonshot/kimi-k2.6",
	"claude-haiku-4-5-latest":            "claude-haiku-4-5",
	"opus-5":                             "claude-opus-5",
	"fable-5":                            "claude-fable-5",
	"mythos-5":                           "claude-mythos-5",
	"gemini-3.6-flash-high":              "gemini-3.6-flash",
	"gemini-3.1-pro":                     "gemini-3.1-pro-preview",
	"gemini-3.1-pro-preview-customtools": "gemini-3.1-pro-preview",
}

var releasePinSuffix = regexp.MustCompile(`^(?:-\d{8}|-\d{4}-\d{2}-\d{2}|-thinking|@default)$`)

type datedModel struct {
	released time.Time
	model    string
}

var autoReviewModels = []datedModel{
	{time.Date(2026, 4, 23, 0, 0, 0, 0, time.UTC), "gpt-5.5"},
	{time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC), "gpt-5.4"},
	{time.Date(2026, 2, 5, 0, 0, 0, 0, time.UTC), "gpt-5.3-codex"},
	{time.Date(2025, 12, 11, 0, 0, 0, 0, time.UTC), "gpt-5.2-codex"},
	{time.Date(2025, 11, 13, 0, 0, 0, 0, time.UTC), "gpt-5.1-codex"},
	{time.Date(2025, 9, 15, 0, 0, 0, 0, time.UTC), "gpt-5-codex"},
	{time.Date(2025, 8, 7, 0, 0, 0, 0, time.UTC), "gpt-5"},
}

func resolvePricingModel(model string, occurredAt time.Time) string {
	name := strings.ToLower(strings.TrimSpace(model))
	if name == "" {
		return ""
	}
	if name == "codex-auto-review" {
		if occurredAt.IsZero() {
			return autoReviewModels[0].model
		}
		for _, candidate := range autoReviewModels {
			if !occurredAt.Before(candidate.released) {
				return candidate.model
			}
		}
		return ""
	}
	if alias, ok := pricingAliases[name]; ok {
		name = alias
	}
	if _, ok := pricingMap[name]; ok {
		return name
	}
	// Provider-qualified names are common in proxy logs. Only drop a provider
	// prefix when the remaining exact name is present in the pinned snapshot.
	if slash := strings.LastIndexByte(name, '/'); slash >= 0 {
		if tail := name[slash+1:]; pricingMap[tail].Input > 0 {
			return tail
		}
	}
	// Resolve only verified release-pin/variant patterns at a key boundary. Longest
	// keys win, which prevents e.g. gpt-5.4-mini from collapsing to gpt-5.4.
	best := ""
	for candidate := range pricingMap {
		if len(candidate) <= len(best) || !strings.HasPrefix(name, candidate) {
			continue
		}
		suffix := strings.TrimPrefix(name, candidate)
		if suffix == "" || releasePinSuffix.MatchString(suffix) {
			best = candidate
		}
	}
	return best
}

// CalculateCost is retained for source compatibility; unknown models return zero
// rather than receiving a fabricated generic price.
func CalculateCost(model string, inTokens, outTokens float64) float64 {
	cost, _ := CostFor(model, coredb.TokenCounts{InputUncached: int64(inTokens), Output: int64(outTokens), Total: int64(inTokens + outTokens)})
	if cost == nil {
		return 0
	}
	return float64(*cost) / 1_000_000
}
