package ingest

import (
	"testing"

	coredb "github.com/spencer-life/ai-tracker/internal/db"
)

func TestUnknownModelHasNoFabricatedCost(t *testing.T) {
	if cost, _ := CostFor("unknown-future-model", coredb.TokenCounts{InputUncached: 100, Output: 50, Total: 150}); cost != nil {
		t.Fatalf("unknown model cost = %v, want nil", *cost)
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
