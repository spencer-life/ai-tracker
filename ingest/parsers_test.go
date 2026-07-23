package ingest

import (
	"testing"
)

func BenchmarkParse(b *testing.B) {
	data := []byte(`{"usage": {"prompt_tokens": 100, "completion_tokens": 50}, "model": "test", "content": "sk-ant-api1234567890"}`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		redacted := RedactSecrets(data)
		extractTokenUsage(map[string]interface{}{"usage": map[string]interface{}{"prompt_tokens": 100.0, "completion_tokens": 50.0}})
		_ = redacted
	}
}
