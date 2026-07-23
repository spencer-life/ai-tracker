package ingest

import (
	"testing"
)

func BenchmarkParse(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		extractTokenUsage(map[string]interface{}{"usage": map[string]interface{}{"prompt_tokens": 100.0, "completion_tokens": 50.0}})
	}
}
