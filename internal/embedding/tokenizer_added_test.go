package embedding

import "testing"

func TestSpaceRightmostAtStart(t *testing.T) {
	cases := []struct {
		text string
		want int
	}{
		{"", 0},
		{"abc", 0},
		{"abc ", 0},
		{" abc", 1},
		{"  \tabc", 3},
		{"\u3000abc", 3}, // U+3000 ideographic space is 3 UTF-8 bytes
		{"  ", 2},
	}
	for _, tc := range cases {
		if got := spaceRightmostAtStart(tc.text); got != tc.want {
			t.Fatalf("spaceRightmostAtStart(%q) = %d, want %d", tc.text, got, tc.want)
		}
	}
}
