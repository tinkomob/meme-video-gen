package snippet

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestAudioBands(t *testing.T) {
	samples := make([]float64, 16000*DurationSeconds)
	for _, frame := range analyzeSnippet(samples) {
		if frame.level != 0 {
			t.Fatal("silence must have zero level")
		}
		for _, band := range frame.bands {
			if band != 0 {
				t.Fatal("silence must have zero spectrum")
			}
		}
	}
	for i := range samples {
		samples[i] = .6 * math.Sin(2*math.Pi*440*float64(i)/16000)
	}
	frames := analyzeSnippet(samples)
	peak := 0
	for i, value := range frames[100].bands {
		if value > frames[100].bands[peak] {
			peak = i
		}
	}
	frequency := 40 * math.Pow(7000.0/40, float64(peak)/(visualBands-1))
	if math.Abs(frequency-440) > 40 || frames[100].level < .9 {
		t.Fatalf("expected 440Hz and strong energy, got %.1fHz level=%.2f", frequency, frames[100].level)
	}
}

func TestVisualizersReactToAudio(t *testing.T) {
	var loud audioFrame
	loud.level = 1
	for b := range loud.bands {
		loud.bands[b] = .8
	}
	for _, style := range snippetStyles {
		for variant := 0; variant < 3; variant++ {
			t.Run(fmt.Sprintf("%s-%d", style, variant), func(t *testing.T) {
				p := newSnippetPainter(style, 1, variant)
				silent := append([]byte(nil), p.frame(100, audioFrame{})...)
				playing := append([]byte(nil), p.frame(100, loud)...)
				if bytes.Equal(silent, playing) {
					t.Fatal("background does not react to audio")
				}
				if !bytes.Equal(playing, newSnippetPainter(style, 1, variant).frame(100, loud)) {
					t.Fatal("same seed must reproduce the same frame")
				}
				if bytes.Equal(playing, newSnippetPainter(style, 2, variant).frame(100, loud)) {
					t.Fatal("different seeds must vary the visualizer")
				}
				if bytes.Equal(playing, p.frame(120, loud)) {
					t.Fatal("visualizer must move over time")
				}
				var bass, treble audioFrame
				bass.level, treble.level = 1, 1
				for b := 0; b < visualBands/2; b++ {
					bass.bands[b] = 1
					treble.bands[b+visualBands/2] = 1
				}
				low := append([]byte(nil), p.frame(100, bass)...)
				if bytes.Equal(low, p.frame(100, treble)) {
					t.Fatal("visualizer must react to frequencies, not only volume")
				}
			})
		}
	}
}

func TestSnippetStyleSelection(t *testing.T) {
	if len(snippetPalettes) != len(snippetStyles) {
		t.Fatal("style registry and palettes disagree")
	}
	for _, style := range snippetStyles {
		seen := map[int]bool{}
		for seed := int64(0); seed < 20; seed++ {
			seen[newSnippetPainter(style, seed, -1).variant] = true
		}
		if len(seen) != 3 {
			t.Fatalf("%s: random must reach all three variants", style)
		}
	}
	var g Generator
	for _, tc := range []struct {
		style   string
		variant int
	}{{"unknown", -1}, {"silver", -2}, {"galaxy", 3}} {
		if _, err := g.GenerateSongSnippet(context.Background(), "unused.mp4", tc.style, "", tc.variant); err == nil {
			t.Fatalf("accepted invalid style/variant: %+v", tc)
		}
	}
}

func TestSnippetAudioUsesSelectedExcerpt(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "song.wav")
	output, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=20", "-f", "lavfi", "-i", "sine=frequency=1760:duration=20", "-filter_complex", "[0:a][1:a]concat=n=2:v=0:a=1", path).CombinedOutput()
	if err != nil {
		t.Fatalf("create test song: %v: %s", err, output)
	}
	for _, tc := range []struct{ start, frequency float64 }{{0, 440}, {20, 1760}} {
		frames, err := snippetAudio(ctx, path, tc.start)
		if err != nil {
			t.Fatal(err)
		}
		if len(frames) != DurationSeconds*visualFPS {
			t.Fatalf("unexpected frame count: %d", len(frames))
		}
		peak := 0
		for b, value := range frames[100].bands {
			if value > frames[100].bands[peak] {
				peak = b
			}
		}
		frequency := 40 * math.Pow(7000.0/40, float64(peak)/(visualBands-1))
		if math.Abs(frequency-tc.frequency) > tc.frequency*.1 {
			t.Fatalf("offset %.0f: expected %.0fHz, got %.0fHz", tc.start, tc.frequency, frequency)
		}
	}
}
