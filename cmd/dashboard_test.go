package cmd

import "testing"

func TestBrowserOpenCommand(t *testing.T) {
	target := "http://127.0.0.1:8080"
	tests := []struct {
		goos, want string
		ok         bool
	}{
		{goos: "linux", want: "xdg-open", ok: true},
		{goos: "darwin", want: "open", ok: true},
		{goos: "windows", want: "rundll32", ok: true},
		{goos: "plan9", want: "", ok: false},
	}
	for _, test := range tests {
		name, args, ok := browserOpenCommand(test.goos, target)
		if name != test.want || ok != test.ok {
			t.Fatalf("%s command=%q ok=%v", test.goos, name, ok)
		}
		if ok && len(args) == 0 {
			t.Fatalf("%s missing arguments", test.goos)
		}
	}
}
