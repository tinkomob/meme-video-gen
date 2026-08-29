package web

import "testing"

func TestParseRange(t *testing.T) {
	tests := []struct {
		name, header string
		start, end   int64
		partial      bool
		wantErr      bool
	}{
		{name: "full", start: 0, end: 99},
		{name: "bounded", header: "bytes=10-24", start: 10, end: 24, partial: true},
		{name: "open ended", header: "bytes=90-", start: 90, end: 99, partial: true},
		{name: "suffix", header: "bytes=-10", start: 90, end: 99, partial: true},
		{name: "past end", header: "bytes=95-200", start: 95, end: 99, partial: true},
		{name: "invalid", header: "bytes=100-101", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			start, end, partial, err := parseRange(test.header, 100)
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, want error: %v", err, test.wantErr)
			}
			if test.wantErr {
				return
			}
			if start != test.start || end != test.end || partial != test.partial {
				t.Fatalf("got (%d, %d, %v), want (%d, %d, %v)", start, end, partial, test.start, test.end, test.partial)
			}
		})
	}
}
