package snippet

import (
	"bytes"
	"context"
	"crypto/sha256"
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
	seen := map[[32]byte]string{}
	for _, style := range snippetStyles {
		for variant := 0; variant < 3; variant++ {
			t.Run(fmt.Sprintf("%s-%d", style, variant), func(t *testing.T) {
				p := newSnippetPainter(style, 1, variant)
				silent := append([]byte(nil), p.frame(100, audioFrame{})...)
				playing := append([]byte(nil), p.frame(100, loud)...)
				hash := sha256.Sum256(playing)
				if previous, ok := seen[hash]; ok {
					t.Fatalf("visual duplicates %s", previous)
				}
				seen[hash] = t.Name()
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
				for i := 1; i < len(p.timeline); i++ {
					at := p.timeline[i].at
					p.timeline[i] = p.timeline[0]
					p.timeline[i].at = at
				}
				if bytes.Equal(playing, p.frame(100, loud)) {
					t.Fatal("visualizer ignores the evolving composition")
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

func TestVisualEvolution(t *testing.T) {
	p := newSnippetPainter("orbit", 42, 1)
	if p.evolution(-1) != p.timeline[0] || p.evolution(2) != p.timeline[4] {
		t.Fatal("evolution must clamp to the snippet endpoints")
	}
	for i, key := range p.timeline {
		if got := p.evolution(key.at); got != key {
			// Interpolation can differ by a floating-point rounding error at the boundary.
			if math.Abs(got.zoom-key.zoom)+math.Abs(got.unfold-key.unfold) > 1e-12 {
				t.Fatalf("keyframe %d was not reached: %+v", i, got)
			}
		}
		if i == 0 {
			continue
		}
		if key.at <= p.timeline[i-1].at || math.Abs(key.unfold-p.timeline[i-1].unfold) < 1e-4 {
			t.Fatal("keyframes must advance through distinct compositions")
		}
		before, after := p.evolution(key.at-1e-6), p.evolution(key.at+1e-6)
		if math.Abs(before.zoom-after.zoom)+math.Abs(before.turn-after.turn)+math.Abs(before.x-after.x)+math.Abs(before.y-after.y)+math.Abs(before.unfold-after.unfold)+math.Abs(before.density-after.density) > 1e-5 {
			t.Fatalf("evolution jumps at keyframe %d", i)
		}
	}
	palette, phase, spread := p.palette, p.phase, p.spread
	var audio audioFrame
	audio.level = .6
	for i := range audio.bands {
		audio.bands[i] = .5
	}
	for _, frame := range []int{0, 300, 599, 1, 300} {
		actual := append([]byte(nil), p.frame(frame, audio)...)
		if !bytes.Equal(actual, newSnippetPainter("orbit", 42, 1).frame(frame, audio)) {
			t.Fatalf("frame %d depends on previously rendered frames", frame)
		}
		if p.palette != palette || p.phase != phase || p.spread != spread {
			t.Fatal("layer rendering changed the base scene or player accent")
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
