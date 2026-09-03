package logfmt

import "testing"

func TestParseLevel(t *testing.T) {
	cases := []struct {
		in   string
		want Level
	}{
		{"TRACE", LevelTrace},
		{"DEBUG", LevelDebug},
		{"INFO", LevelInfo},
		{"WARN", LevelWarn},
		{"ERROR", LevelError},
		{"", LevelUnknown},
		{"NOPE", LevelUnknown},
	}
	for _, c := range cases {
		if got := ParseLevel(c.in); got != c.want {
			t.Errorf("ParseLevel(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestLevelString(t *testing.T) {
	if got := LevelTrace.String(); got != "TRACE" {
		t.Errorf("LevelTrace.String() = %q, want %q", got, "TRACE")
	}
	if got := LevelUnknown.String(); got != "UNKNOWN" {
		t.Errorf("LevelUnknown.String() = %q, want %q", got, "UNKNOWN")
	}
}
