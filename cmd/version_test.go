package cmd

import "testing"

func TestDisplayVersion(t *testing.T) {
	original := Version
	t.Cleanup(func() { Version = original })

	tests := []struct {
		name    string
		version string
		want    string
	}{
		{name: "development", version: "dev", want: "dev"},
		{name: "empty", version: "", want: "dev"},
		{name: "semantic", version: "1.1.0", want: "v1.1.0"},
		{name: "tagged", version: "v1.1.0", want: "v1.1.0"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			Version = test.version
			if got := displayVersion(); got != test.want {
				t.Fatalf("displayVersion() = %q, want %q", got, test.want)
			}
		})
	}
}
