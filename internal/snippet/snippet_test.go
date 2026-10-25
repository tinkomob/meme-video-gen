package snippet

import (
	"bytes"
	"context"
	"fmt"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestSnippetRenderPipe(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	dir := t.TempDir()
	audio, cover := filepath.Join(dir, "audio.wav"), filepath.Join(dir, "cover.jpg")
	for _, args := range [][]string{
		{"-f", "lavfi", "-i", "sine=duration=25", audio},
		{"-f", "lavfi", "-i", "color=c=gray:s=600x600", "-frames:v", "1", cover},
	} {
		if out, err := exec.CommandContext(ctx, "ffmpeg", append([]string{"-v", "error", "-nostdin", "-y"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("fixture: %v: %s", err, out)
		}
	}
	for _, name := range []string{"title.txt", "artist.txt", "elapsed.txt", "duration.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	output := filepath.Join(dir, "snippet.mp4")
	cmd := snippetRenderCommand(ctx, audio, cover, output, snippetFilter("0xffffff", dir, 2, 25, 42), 2)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	pipe, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		pipe.Close()
		t.Fatal(err)
	}
	painter := newSnippetPainter("galaxy", 1, 1)
	frames, err := snippetAudio(ctx, audio, 2)
	if err != nil {
		pipe.Close()
		cmd.Wait()
		t.Fatal(err)
	}
	var writeErr error
	for f, audio := range frames {
		if _, writeErr = pipe.Write(painter.frame(f, audio)); writeErr != nil {
			break
		}
	}
	pipe.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("render: %v (context: %v): %s", err, ctx.Err(), stderr.String())
	}
	if writeErr != nil {
		t.Fatal(writeErr)
	}
	data, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=nw=1:nk=1", output).Output()
	if err != nil {
		t.Fatal(err)
	}
	duration, err := strconv.ParseFloat(string(bytes.TrimSpace(data)), 64)
	if err != nil || duration < 19.9 || duration > 20.1 {
		t.Fatalf("expected 20-second video, got %s: %v", data, err)
	}
}

// Exercise every filter graph with real audio and text containing filter syntax.
func TestSnippetFilters(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}
	dir := t.TempDir()
	for name, value := range map[string]string{"title.txt": "Song: 100% 'ночь'", "artist.txt": "Artist", "elapsed.txt": "%{eif:floor((t+50)/60):d:2}:%{eif:mod(t+50,60):d:2}", "duration.txt": "03:20"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, style := range snippetStyles {
		t.Run(style, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error",
				"-f", "lavfi", "-i", "color=c=black:s=540x960:r=30",
				"-f", "lavfi", "-i", "sine=frequency=440:duration=1",
				"-f", "lavfi", "-i", "color=c=gray:s=240x240:r=30",
				"-filter_complex_threads", "1", "-filter_complex", snippetFilter(newSnippetPainter(style, 1, 0).accent(), dir, 50, 200, 42),
				"-map", "[video]", "-map", "[audio]", "-t", "0.2", "-f", "null", "-")
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("style %s: %v\n%s", style, err, output)
			}
		})
	}
	// A 50-second excerpt offset in a 200-second song starts at 25%,
	// and advances relative to the full track through the final snippet second.
	for _, second := range []int{0, DurationSeconds - 1} {
		t.Run(fmt.Sprintf("progress-at-%d", second), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			graph := snippetFilter("0xffffff", dir, 50, 200, 42)
			graph += fmt.Sprintf(";[video]trim=start=%d,setpts=PTS-STARTPTS[frame]", second)
			cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error",
				"-f", "lavfi", "-i", "color=c=black:s=540x960:r=30",
				"-f", "lavfi", "-i", "sine=frequency=440:duration="+strconv.Itoa(DurationSeconds),
				"-f", "lavfi", "-i", "color=c=gray:s=240x240:r=30",
				"-filter_complex_threads", "1", "-filter_complex", graph,
				"-map", "[audio]", "-t", strconv.Itoa(DurationSeconds), "-f", "null", os.DevNull,
				"-map", "[frame]", "-frames:v", "1", "-c:v", "png", "-f", "image2pipe", "pipe:1")
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			data, err := cmd.Output()
			if err != nil {
				t.Fatalf("render progress: %v\n%s", err, stderr.String())
			}
			frame, err := png.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			position := 210 + 660*(50+second)/200
			left, _, _, _ := frame.At(position-15, 1286).RGBA()
			right, _, _, _ := frame.At(position+15, 1286).RGBA()
			if left < 170*257 || right > 110*257 {
				t.Fatalf("incorrect progress at %ds: expected boundary near x=%d, left=%d right=%d", second, position, left/257, right/257)
			}
		})
	}
}
