package snippet

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"meme-video-gen/internal/audio"
	"meme-video-gen/internal/model"
)

const DurationSeconds = 20

func snippetAudioFade() string {
	return fmt.Sprintf("afade=t=in:d=0.15,afade=t=out:st=%.1f:d=0.5", float64(DurationSeconds)-0.5)
}

// GenerateSongSnippet writes a local preview; it does not publish or schedule it.
func (g *Generator) GenerateSongSnippet(ctx context.Context, output, style, songID string, variant int) (*model.Song, error) {
	if style == "random" {
		style = snippetStyles[rand.Intn(len(snippetStyles))]
	}
	if !slices.Contains(snippetStyles, style) {
		return nil, fmt.Errorf("unknown snippet style %q: use random or %s", style, strings.Join(snippetStyles, ", "))
	}
	if variant < -1 || variant > 2 {
		return nil, fmt.Errorf("variant must be -1 (random), 0, 1 or 2")
	}
	painter := newSnippetPainter(style, rand.Int63(), variant)
	song, err := g.snippetSong(ctx, songID)
	if err != nil {
		return nil, err
	}
	song.Author = audio.CleanAuthorName(song.Author)
	dir, err := os.MkdirTemp("", "song-snippet-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	audioPath, err := g.audio.DownloadSongToTemp(ctx, song)
	if err != nil {
		return nil, err
	}
	defer os.Remove(audioPath)
	cover, coverErr := g.downloadThumbnail(ctx, song.ID, dir)
	probe, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=nw=1:nk=1", audioPath).Output()
	if err != nil {
		return nil, fmt.Errorf("probe snippet audio: %w", err)
	}
	duration, err := strconv.ParseFloat(strings.TrimSpace(string(probe)), 64)
	if err != nil || math.IsNaN(duration) || math.IsInf(duration, 0) || duration < DurationSeconds {
		return nil, fmt.Errorf("snippet requires at least %d seconds of audio, got %q", DurationSeconds, strings.TrimSpace(string(probe)))
	}
	title := strings.Join(strings.Fields(song.Title), " ")
	titleSize := 48
	if n := len([]rune(title)); n > 30 {
		titleSize = max(18, 1440/n)
	}
	start := rand.Float64() * math.Max(0, duration*.75-DurationSeconds)
	// Text files keep drawtext expressions separate from FFmpeg filter escaping.
	elapsed := fmt.Sprintf("%%{eif:floor((t+%.6f)/60):d:2}:%%{eif:mod(t+%.6f,60):d:2}", start, start)
	total := fmt.Sprintf("%02d:%02d", int(duration)/60, int(duration)%60)
	for name, value := range map[string]string{"title.txt": title, "artist.txt": strings.Join(strings.Fields(song.Author), " "), "elapsed.txt": elapsed, "duration.txt": total} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0600); err != nil {
			return nil, err
		}
	}
	g.log.Infof("song snippet: %s — %s; offset=%.2fs duration=%.2fs style=%s variant=%d", song.Author, song.Title, start, duration, style, painter.variant)
	g.log.Infof("song snippet: waiting for renderer")
	select {
	case ffmpegSem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-ffmpegSem }()
	g.log.Infof("song snippet: analyzing audio")
	frames, err := snippetAudio(ctx, audioPath, start)
	if err != nil {
		return nil, err
	}
	if coverErr != nil {
		g.log.Warnf("snippet cover unavailable; using song waveform: %v", coverErr)
		cover = filepath.Join(dir, "wave-cover.png")
		result, err := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-i", audioPath, "-filter_complex", "aformat=channel_layouts=mono,showwavespic=s=600x600:colors=white,format=rgb24", "-frames:v", "1", cover).CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("render waveform cover: %w: %s", err, result)
		}
	}
	videoPath := filepath.Join(dir, "snippet.mp4")
	cmd := snippetRenderCommand(ctx, audioPath, cover, videoPath, snippetFilter(painter.accent(), dir, start, duration, titleSize), start)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	pipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		pipe.Close()
		return nil, err
	}
	renderStarted := time.Now()
	g.log.Infof("song snippet: rendering %d frames", len(frames))
	var writeErr error
	for f, audio := range frames {
		if writeErr = ctx.Err(); writeErr != nil {
			break
		}
		if _, writeErr = pipe.Write(painter.frame(f, audio)); writeErr != nil {
			break
		}
		if (f+1)%visualFPS == 0 {
			g.log.Infof("song snippet: rendered %d/%d frames (%s)", f+1, len(frames), time.Since(renderStarted).Round(time.Second))
		}
	}
	pipe.Close()
	g.log.Infof("song snippet: finishing encoder")
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("render snippet: %w: %s", err, stderr.String())
	}
	if writeErr != nil {
		return nil, fmt.Errorf("stream visualizer frames: %w", writeErr)
	}
	g.log.Infof("song snippet: encoded in %s", time.Since(renderStarted).Round(time.Second))
	src, err := os.Open(videoPath)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	dst, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return nil, err
	}
	_, copyErr := io.Copy(dst, src)
	closeErr := dst.Close()
	if copyErr != nil || closeErr != nil {
		os.Remove(output)
		return nil, fmt.Errorf("save snippet: copy=%v close=%v", copyErr, closeErr)
	}
	return song, nil
}

func snippetRenderCommand(ctx context.Context, audioPath, cover, output, filter string, start float64) *exec.Cmd {
	return exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		// Raw frames have no metadata to probe; do not wait for more pipe data during input discovery.
		"-probesize", "32", "-analyzeduration", "0", "-thread_queue_size", "8",
		"-f", "rawvideo", "-pixel_format", "rgb24", "-video_size", "540x960", "-framerate", "30", "-i", "pipe:0",
		"-thread_queue_size", "8", "-ss", fmt.Sprintf("%.6f", start), "-t", strconv.Itoa(DurationSeconds), "-i", audioPath,
		"-i", cover,
		"-filter_complex_threads", "1", "-filter_complex", filter,
		"-map", "[video]", "-map", "[audio]", "-t", strconv.Itoa(DurationSeconds), "-r", "30",
		"-c:v", "libx264", "-preset", "fast", "-crf", "20", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-b:a", "192k", "-ar", "48000", "-movflags", "+faststart", output)
}

func (g *Generator) snippetSong(ctx context.Context, id string) (*model.Song, error) {
	var index model.SongsIndex
	found, err := g.s3.ReadJSON(ctx, g.cfg.SongsJSONKey, &index)
	if err != nil {
		return nil, err
	}
	if id == "" {
		var pool []*model.Song
		for i := range index.Items {
			song := &index.Items[i]
			if song.AudioKey != "" && (song.DurationS == 0 || song.DurationS >= DurationSeconds) {
				pool = append(pool, song)
			}
		}
		if len(pool) == 0 {
			return nil, fmt.Errorf("no songs long enough for a %d-second snippet", DurationSeconds)
		}
		return pool[rand.Intn(len(pool))], nil
	}
	if found {
		for i := range index.Items {
			if index.Items[i].ID == id {
				return &index.Items[i], nil
			}
		}
	}
	return nil, fmt.Errorf("song %q not found in S3 index", id)
}

func snippetFont(serif bool) string {
	fonts := []string{"C:/Windows/Fonts/arial.ttf", "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf", "/usr/share/fonts/ttf-dejavu/DejaVuSans.ttf"}
	if serif {
		fonts = []string{"C:/Windows/Fonts/georgia.ttf", "/usr/share/fonts/truetype/dejavu/DejaVuSerif.ttf", "/usr/share/fonts/ttf-dejavu/DejaVuSerif.ttf"}
	}
	for _, font := range fonts {
		if _, err := os.Stat(font); err == nil {
			return "fontfile='" + escapeFfmpegPath(font) + "':"
		}
	}
	return ""
}

func (p *snippetPainter) accent() string {
	c := p.palette[0]
	return fmt.Sprintf("0x%02x%02x%02x", int(c.r), int(c.g), int(c.b))
}

func snippetFilter(accent, dir string, start, duration float64, titleSize int) string {
	sans, serif := snippetFont(false), snippetFont(true)
	return fmt.Sprintf(
		"[0:v]scale=1080:1920:flags=lanczos,setsar=1[bg];"+
			"[1:a]aformat=sample_rates=48000:channel_layouts=stereo,asetpts=PTS-STARTPTS,%s[audio];"+
			"[2:v]trim=end_frame=1,setpts=PTS-STARTPTS,scale=520:520:force_original_aspect_ratio=increase,crop=520:520,setsar=1,format=rgba,"+
			"geq=r='r(X,Y)':g='g(X,Y)':b='b(X,Y)':a='if(lte(hypot(max(abs(X-W/2)-(W/2-14),0),max(abs(Y-H/2)-(H/2-14),0)),14),255,0)'[cover];"+
			"[bg][cover]overlay=x=280:y=560:eof_action=repeat,"+
			"drawtext=%stextfile='%s':expansion=none:fontsize=%d:fontcolor=white:shadowcolor=black@0.7:shadowx=1:shadowy=2:x=(w-tw)/2:y=1130,"+
			"drawtext=%stextfile='%s':expansion=none:fontsize=28:fontcolor=0xb7b7c5:x=(w-tw)/2:y=1210,"+
			"drawtext=%stextfile='%s':fontsize=22:fontcolor=0xd0d0d8:x=210:y=1320,"+
			"drawtext=%stextfile='%s':expansion=none:fontsize=22:fontcolor=0xd0d0d8:x=870-tw:y=1320[metadata];"+
			"color=c=0x454550:s=660x4:r=30:d=%d[track];color=c=%s:s=660x4:r=30:d=%d[fill];"+
			"[track][fill]overlay=x='-660+660*min((%.6f+t)/%.6f,1)':y=0:eval=frame[progress];"+
			"[metadata][progress]overlay=x=210:y=1285:shortest=1,"+
			"drawtext=%stext='●':fontsize=25:fontcolor=white:x='198+660*min((%.6f+t)/%.6f,1)':y=1279,format=yuv420p[video]",
		snippetAudioFade(), serif, escapeFfmpegPath(filepath.Join(dir, "title.txt")), titleSize,
		sans, escapeFfmpegPath(filepath.Join(dir, "artist.txt")),
		sans, escapeFfmpegPath(filepath.Join(dir, "elapsed.txt")),
		sans, escapeFfmpegPath(filepath.Join(dir, "duration.txt")), DurationSeconds, accent, DurationSeconds, start, duration, sans, start, duration)
}
