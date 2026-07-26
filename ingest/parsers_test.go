package ingest

import (
	"testing"
	"time"

	coredb "github.com/spencer-life/ai-tracker/internal/db"
)

func TestUnknownModelHasNoFabricatedCost(t *testing.T) {
	if cost, _ := CostFor("unknown-future-model", coredb.TokenCounts{InputUncached: 100, Output: 50, Total: 150}); cost != nil {
		t.Fatalf("unknown model cost = %v, want nil", *cost)
	}
}

func TestCurrentPricingResolvesAliasesAndAutoReviewByDate(t *testing.T) {
	tokens := coredb.TokenCounts{InputUncached: 1_000, Total: 1_000}
	for _, tc := range []struct {
		model string
		at    time.Time
		want  int64
	}{
		{"gpt-5.6-sol", time.Time{}, 5_000},
		{"terra-5.6", time.Time{}, 2_500},
		{"codex-auto-review", time.Date(2026, 4, 22, 0, 0, 0, 0, time.UTC), 2_500},
		{"codex-auto-review", time.Date(2026, 4, 23, 0, 0, 0, 0, time.UTC), 5_000},
		{"claude-haiku-4-5-20251001-thinking", time.Time{}, 1_000},
		{"claude-opus-5", time.Time{}, 5_000},
		{"opus-5", time.Time{}, 5_000},
		{"claude-fable-5", time.Time{}, 10_000},
		{"fable-5", time.Time{}, 10_000},
		{"gemini-3.6-flash-high", time.Time{}, 1_500},
		{"gemini-3.1-pro", time.Time{}, 2_000},
	} {
		got, _ := CostForAt(tc.model, tokens, tc.at)
		if got == nil || *got != tc.want {
			if got == nil {
				t.Fatalf("CostForAt(%q)=nil, want %d", tc.model, tc.want)
			}
			t.Fatalf("CostForAt(%q)=%d, want %d", tc.model, *got, tc.want)
		}
	}
}

func TestSonnetFivePricingUsesOccurrenceDate(t *testing.T) {
	tokens := coredb.TokenCounts{InputUncached: 1_000, Total: 1_000}
	before, _ := CostForAt("claude-sonnet-5", tokens, time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC))
	after, _ := CostForAt("claude-sonnet-5", tokens, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	if before == nil || after == nil || *before != 2_000 || *after != 3_000 {
		t.Fatalf("Sonnet 5 dated costs before=%v after=%v", before, after)
	}
}

func TestPricingDoesNotGuessUnknownSuffixes(t *testing.T) {
	for _, model := range []string{"gpt-5.4-completely-unknown", "claude-opus-5.1", "gemini-3.6-flash-ultra"} {
		if cost, _ := CostFor(model, coredb.TokenCounts{InputUncached: 1_000, Total: 1_000}); cost != nil {
			t.Fatalf("CostFor(%q)=%d, want unknown", model, *cost)
		}
	}
}

func TestGeminiCacheStorageWithoutDurationIsUnpriced(t *testing.T) {
	if cost, _ := CostFor("gemini-3.6-flash", coredb.TokenCounts{CacheWrite: 100, Total: 100}); cost != nil {
		t.Fatalf("Gemini cache creation cost=%d, want unknown without storage duration", *cost)
	}
}

func TestCurrentPricingUsesWholeRequestLongContextRates(t *testing.T) {
	tokens := coredb.TokenCounts{InputUncached: 200_000, CacheRead: 72_001, Output: 1_000, Total: 273_001}
	got, _ := CostFor("gpt-5.6-sol", tokens)
	// 200k*10 + 72,001*1 + 1k*45 USD-per-million rates.
	const want = int64(2_117_001)
	if got == nil || *got != want {
		t.Fatalf("long-context cost=%v, want %d", got, want)
	}
}

func TestCurrentPricingRoundsMicrodollars(t *testing.T) {
	got, _ := CostFor("gpt-5.6-terra", coredb.TokenCounts{InputUncached: 1, Total: 1})
	if got == nil || *got != 3 {
		t.Fatalf("rounded cost=%v, want 3 microdollars", got)
	}
}

func TestClaudeOneHourCacheWriteUsesDoubleInputRate(t *testing.T) {
	tokens := coredb.TokenCounts{CacheWrite: 30, Total: 30}
	got, _ := CostForAtWithCacheDuration("claude-opus-5", tokens, 10, time.Time{})
	// 20 five-minute writes at 6.25 + 10 one-hour writes at 10.
	if got == nil || *got != 225 {
		t.Fatalf("duration-aware cache cost=%v, want 225", got)
	}
}

func TestCostDoesNotDoubleBillOverlappingReasoningTokens(t *testing.T) {
	cost, _ := CostFor("claude-3-5-sonnet-20241022", coredb.TokenCounts{InputUncached: 10, Output: 20, Reasoning: 5, Total: 30})
	if cost == nil || *cost != 330 {
		t.Fatalf("cost=%v, want 330 microdollars without separately billing reasoning", cost)
	}
}

func TestStableIDSeparatesParts(t *testing.T) {
	if stableID("ab", "c") == stableID("a", "bc") {
		t.Fatal("stable IDs must preserve part boundaries")
	}
}
