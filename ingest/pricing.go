package ingest

import (
	_ "embed"
	"encoding/json"
	"strings"
	"sync"

	coredb "github.com/spencer-life/ai-tracker/internal/db"
)

const pricingVersion = "embedded-2026-07-23"

type Pricing struct {
	Input      float64 `json:"input"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
	Output     float64 `json:"output"`
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
	loadPricing()
	p, ok := pricingMap[strings.ToLower(model)]
	if !ok || model == "" {
		return nil, pricingVersion
	}
	// Reasoning is retained as a useful source counter, but Codex can report it
	// as a subset of output_tokens. Billing it separately would double charge.
	value := float64(tokens.InputUncached)*p.Input + float64(tokens.CacheRead)*p.CacheRead + float64(tokens.CacheWrite)*p.CacheWrite + float64(tokens.Output)*p.Output
	micros := int64(value) // rates are USD per million tokens, so token*rate equals microdollars.
	return &micros, pricingVersion
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
