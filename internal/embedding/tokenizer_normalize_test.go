package embedding

import "testing"

func TestStripDiacritics(t *testing.T) {
	cases := []struct {
		text string
		want string
	}{
		{"café", "cafe"},
		{"naïve", "naive"},
		{"日本語", "日本語"},
		{"a\u0301", "a"}, // combining acute accent
		{"\u0301", ""},   // combining mark alone
	}
	for _, tc := range cases {
		if got := stripDiacritics(tc.text); got != tc.want {
			t.Fatalf("stripDiacritics(%q) = %q, want %q", tc.text, got, tc.want)
		}
	}
}
