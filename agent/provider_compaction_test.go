package agent

import "testing"

func TestCompactionConfigKeepsSmallerTailForLocalWindows(t *testing.T) {
	for _, tc := range []struct {
		window int
		want   int
	}{
		{32_000, 2},
		{64_000, 2},
		{128_000, 0}, // Norma's large-window default
	} {
		cfg := compactionConfig(tc.window)
		if cfg.ContextWindow != tc.window || cfg.KeepRecent != tc.want {
			t.Fatalf("window %d: got context=%d keep_recent=%d; want %d/%d",
				tc.window, cfg.ContextWindow, cfg.KeepRecent, tc.window, tc.want)
		}
	}
}
