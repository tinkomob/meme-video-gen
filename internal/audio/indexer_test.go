package audio

import "testing"

func TestCleanAuthorName(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"dee bill - Topic", "dee bill"},
		{"  dee bill - Topic  ", "dee bill"},
		{"Topic", "Topic"},
		{"Topic Artist", "Topic Artist"},
		{"Artist", "Artist"},
		{"", ""},
	} {
		if got := CleanAuthorName(tc.input); got != tc.want {
			t.Errorf("CleanAuthorName(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
