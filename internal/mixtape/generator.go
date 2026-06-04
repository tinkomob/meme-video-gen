package mixtape

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"meme-video-gen/internal"
	"meme-video-gen/internal/audio"
	"meme-video-gen/internal/logging"
	"meme-video-gen/internal/model"
	"meme-video-gen/internal/s3"
)

const (
	mixtapesJSONKey = "mixtapes.json"
	mixtapesPrefix  = "mixtapes/"
	segmentCount    = 4  // segments per mixtape (3-4, we default to 4)
	segmentDuration = 14 // seconds per segment
	maxMixtapes     = 5

	// TopLabelText is the question overlaid on every segment and reused in YouTube descriptions.
	TopLabelText = "What track do you like the most?"
)

// ffmpeg semaphore — one encoding process at a time
var ffmpegSem = make(chan struct{}, 1)

type Mixtape struct {
	ID        string    `json:"id"`
	VideoKey  string    `json:"video_key"`
	ThumbKey  string    `json:"thumb_key"`
	SongIDs   []string  `json:"song_ids"`
	Titles    []string  `json:"titles"`
	CreatedAt time.Time `json:"created_at"`
}

type MixtapeIndex struct {
	UpdatedAt time.Time `json:"updated_at"`
	Items     []Mixtape `json:"items"`
}

type Generator struct {
	cfg   internal.Config
	s3    s3.Client
	audio *audio.Indexer
	log   *logging.Logger
}

func NewGenerator(cfg internal.Config, s3c s3.Client, audioIdx *audio.Indexer, log *logging.Logger) *Generator {
	return &Generator{cfg: cfg, s3: s3c, audio: audioIdx, log: log}
}

// EnsureMixtapes generates mixtapes until the pool reaches maxMixtapes.
func (g *Generator) EnsureMixtapes(ctx context.Context) error {
	g.log.Infof("mixtape: EnsureMixtapes START")
	idx, err := g.loadIndex(ctx)
	if err != nil {
		return err
	}

	// Trim if over limit
	if len(idx.Items) > g.maxMixtapes() {
		idx, err = g.trimOldest(ctx, idx, g.maxMixtapes())
		if err != nil {
			return err
		}
	}

	for len(idx.Items) < g.maxMixtapes() {
		g.log.Infof("mixtape: generating new mixtape (%d/%d)", len(idx.Items)+1, g.maxMixtapes())
		m, err := g.generate(ctx)
		if err != nil {
			g.log.Errorf("mixtape: generate failed: %v", err)
			return err
		}
		idx.Items = append(idx.Items, *m)
		idx.UpdatedAt = time.Now()
		if err := g.saveIndex(ctx, idx); err != nil {
			return err
		}
	}
	g.log.Infof("mixtape: EnsureMixtapes DONE — %d mixtapes", len(idx.Items))
	return nil
}

// GetRandom returns a random mixtape from the index.
func (g *Generator) GetRandom(ctx context.Context) (*Mixtape, error) {
	idx, err := g.loadIndex(ctx)
	if err != nil {
		return nil, err
	}
	if len(idx.Items) == 0 {
		return nil, fmt.Errorf("no mixtapes available")
	}
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	return &idx.Items[r.Intn(len(idx.Items))], nil
}

// GetByID returns a mixtape by ID.
func (g *Generator) GetByID(ctx context.Context, id string) (*Mixtape, error) {
	idx, err := g.loadIndex(ctx)
	if err != nil {
		return nil, err
	}
	for _, m := range idx.Items {
		if m.ID == id {
			cp := m
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("mixtape not found: %s", id)
}

// Delete removes a mixtape from S3 and the index.
func (g *Generator) Delete(ctx context.Context, id string) error {
	idx, err := g.loadIndex(ctx)
	if err != nil {
		return err
	}
	var kept []Mixtape
	for _, m := range idx.Items {
		if m.ID == id {
			_ = g.s3.Delete(ctx, m.VideoKey)
			_ = g.s3.Delete(ctx, m.ThumbKey)
		} else {
			kept = append(kept, m)
		}
	}
	idx.Items = kept
	idx.UpdatedAt = time.Now()
	return g.saveIndex(ctx, idx)
}

// ClearAll deletes all mixtapes from S3 and resets the index.
func (g *Generator) ClearAll(ctx context.Context) error {
	idx, err := g.loadIndex(ctx)
	if err != nil {
		return err
	}
	for _, m := range idx.Items {
		_ = g.s3.Delete(ctx, m.VideoKey)
		_ = g.s3.Delete(ctx, m.ThumbKey)
	}
	idx.Items = []Mixtape{}
	idx.UpdatedAt = time.Now()
	return g.saveIndex(ctx, idx)
}

// DownloadVideoToTemp streams the mixtape video to a temp file.
func (g *Generator) DownloadVideoToTemp(ctx context.Context, m *Mixtape) (string, error) {
	reader, err := g.s3.GetReader(ctx, m.VideoKey)
	if err != nil {
		return "", fmt.Errorf("s3 get reader: %w", err)
	}
	defer reader.Reader.Close()

	f, err := os.CreateTemp(os.TempDir(), "mixtape-*.mp4")
	if err != nil {
		return "", err
	}
	defer f.Close()

	if _, err := io.Copy(f, reader.Reader); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// generate builds a single mixtape video.
func (g *Generator) generate(ctx context.Context) (*Mixtape, error) {
	songs, err := g.pickSongs(ctx, segmentCount)
	if err != nil {
		return nil, fmt.Errorf("pick songs: %w", err)
	}

	tmpDir, err := os.MkdirTemp(os.TempDir(), "mixtape-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	// Shuffle color palette so each segment in this mixtape gets a unique color.
	palette := []string{"yellow", "0x00FFFF", "0xFF6600", "0xFF44FF", "0x00FF88", "0xFF2255", "0xAAFF00", "0xFF9900"}
	rMain := rand.New(rand.NewSource(time.Now().UnixNano()))
	rMain.Shuffle(len(palette), func(i, j int) { palette[i], palette[j] = palette[j], palette[i] })

	var segmentPaths []string
	var songIDs, titles []string

	for i, song := range songs {
		g.log.Infof("mixtape: building segment %d — %s", i+1, song.Title)

		thumbPath, err := g.downloadThumbnail(ctx, song.ID, tmpDir, i)
		if err != nil {
			return nil, fmt.Errorf("download thumbnail for %s: %w", song.ID, err)
		}

		audioPath, err := g.audio.DownloadSongToTemp(ctx, song)
		if err != nil {
			return nil, fmt.Errorf("download audio for %s: %w", song.ID, err)
		}
		defer os.Remove(audioPath)

		// Determine valid start offset, staying out of the final 25% to avoid fade-outs/silence.
		duration := song.DurationS
		if duration <= float64(segmentDuration)+1 {
			duration = float64(segmentDuration) + 2
		}
		r := rand.New(rand.NewSource(time.Now().UnixNano() + int64(i)))
		safeEnd := duration * 0.75
		maxStart := safeEnd - float64(segmentDuration)
		if maxStart < 0 {
			maxStart = 0
		}
		startOffset := r.Float64() * maxStart

		segPath := filepath.Join(tmpDir, fmt.Sprintf("seg%d.mp4", i))
		if err := g.buildSegment(ctx, thumbPath, audioPath, segPath, startOffset, segmentDuration, r, i+1, song.Author, song.Title, palette[i%len(palette)]); err != nil {
			return nil, fmt.Errorf("build segment %d: %w", i, err)
		}
		segmentPaths = append(segmentPaths, segPath)
		songIDs = append(songIDs, song.ID)
		titles = append(titles, song.Title)
	}

	// Concatenate segments
	outPath := filepath.Join(tmpDir, "mixtape.mp4")
	if err := g.concatenate(ctx, segmentPaths, outPath); err != nil {
		return nil, fmt.Errorf("concatenate: %w", err)
	}

	// Read output video
	videoBytes, err := os.ReadFile(outPath)
	if err != nil {
		return nil, err
	}

	// Use first segment thumbnail as mixtape thumbnail
	thumbBytes, err := os.ReadFile(filepath.Join(tmpDir, "thumb0.jpg"))
	if err != nil {
		return nil, err
	}

	id := fmt.Sprintf("mixtape-%d", time.Now().Unix())
	videoKey := mixtapesPrefix + id + ".mp4"
	thumbKey := mixtapesPrefix + id + "_thumb.jpg"

	if err := g.s3.PutBytes(ctx, videoKey, videoBytes, "video/mp4"); err != nil {
		return nil, fmt.Errorf("upload video: %w", err)
	}
	if err := g.s3.PutBytes(ctx, thumbKey, thumbBytes, "image/jpeg"); err != nil {
		return nil, fmt.Errorf("upload thumb: %w", err)
	}

	return &Mixtape{
		ID:        id,
		VideoKey:  videoKey,
		ThumbKey:  thumbKey,
		SongIDs:   songIDs,
		Titles:    titles,
		CreatedAt: time.Now(),
	}, nil
}

// pickSongs returns n songs filtered to eenfinit / dee bill artists.
func (g *Generator) pickSongs(ctx context.Context, n int) ([]*model.Song, error) {
	var songsIdx model.SongsIndex
	found, err := g.s3.ReadJSON(ctx, g.cfg.SongsJSONKey, &songsIdx)
	if err != nil || !found {
		return nil, fmt.Errorf("read songs.json: %w", err)
	}

	var pool []*model.Song
	for i := range songsIdx.Items {
		s := &songsIdx.Items[i]
		author := strings.ToLower(s.Author)
		if strings.Contains(author, "eenfinit") || strings.Contains(author, "dee bill") {
			pool = append(pool, s)
		}
	}
	if len(pool) == 0 {
		return nil, fmt.Errorf("no songs by eenfinit or dee bill found in index")
	}

	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	r.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })

	result := make([]*model.Song, n)
	for i := range result {
		result[i] = pool[i%len(pool)]
	}
	return result, nil
}

// downloadThumbnail fetches the YouTube maxresdefault thumbnail for a video ID.
func (g *Generator) downloadThumbnail(ctx context.Context, videoID, dir string, idx int) (string, error) {
	urls := []string{
		fmt.Sprintf("https://img.youtube.com/vi/%s/maxresdefault.jpg", videoID),
		fmt.Sprintf("https://img.youtube.com/vi/%s/hqdefault.jpg", videoID),
	}

	destPath := filepath.Join(dir, fmt.Sprintf("thumb%d.jpg", idx))

	for _, u := range urls {
		req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
		if err != nil {
			continue
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			if resp != nil {
				resp.Body.Close()
			}
			continue
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			continue
		}
		if err := os.WriteFile(destPath, data, 0644); err != nil {
			return "", err
		}
		return destPath, nil
	}
	return "", fmt.Errorf("could not download thumbnail for video %s", videoID)
}

// wrapText wraps s to at most maxChars per line on word boundaries.
func wrapText(s string, maxChars int) string {
	words := strings.Fields(s)
	var lines []string
	current := ""
	for _, w := range words {
		if current == "" {
			current = w
		} else if len(current)+1+len(w) <= maxChars {
			current += " " + w
		} else {
			lines = append(lines, current)
			current = w
		}
	}
	if current != "" {
		lines = append(lines, current)
	}
	return strings.Join(lines, `\n`)
}

// escapeFfmpegText escapes special characters for use inside an ffmpeg drawtext filter value.
// In filter_complex context, [ ] , ; are structural characters and must also be escaped.
func escapeFfmpegText(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	s = strings.ReplaceAll(s, `:`, `\:`)
	s = strings.ReplaceAll(s, `[`, `\[`)
	s = strings.ReplaceAll(s, `]`, `\]`)
	s = strings.ReplaceAll(s, `,`, `\,`)
	s = strings.ReplaceAll(s, `;`, `\;`)
	return s
}

// panStyle describes one distinct animation pattern for a segment.
type panStyle struct {
	xExpr string
	yExpr string
	zExpr string
}

// pickPanStyle returns a deterministically unique pan style for the given segment index.
// Each style is visually distinct so adjacent segments look noticeably different.
func pickPanStyle(segIdx int, r *rand.Rand) panStyle {
	// Available pan range: iw*zoom-ow (≈2160) horizontal, ih*zoom-oh (≈3840) vertical.
	// `on` = output frame number (30fps). All expressions must be valid ffmpeg math.
	styles := []panStyle{
		// 0: DVD-bounce — triangular wave on both axes (different speeds)
		{
			xExpr: fmt.Sprintf("abs(mod(on*%d+%d,2*(iw*zoom-ow))-(iw*zoom-ow))", 5+r.Intn(3), r.Intn(4320)),
			yExpr: fmt.Sprintf("abs(mod(on*%d+%d,2*(ih*zoom-oh))-(ih*zoom-oh))", 7+r.Intn(4), r.Intn(7680)),
			zExpr: fmt.Sprintf("1+0.02*(1+sin(%.4f+2*PI*on/(30*%d)))", r.Float64()*2*math.Pi, 20+r.Intn(10)),
		},
		// 1: Slow diagonal sweep left-to-right, top-to-bottom, then hold
		{
			xExpr: fmt.Sprintf("min(on*%d+%d,(iw*zoom-ow))", 3+r.Intn(3), r.Intn(500)),
			yExpr: fmt.Sprintf("min(on*%d+%d,(ih*zoom-oh))", 4+r.Intn(3), r.Intn(800)),
			zExpr: "1.05",
		},
		// 2: Circular motion (sin/cos orbit around center)
		{
			xExpr: fmt.Sprintf("(iw*zoom-ow)/2+((iw*zoom-ow)/2)*sin(%.4f+2*PI*on/(30*%d))", r.Float64()*2*math.Pi, 18+r.Intn(10)),
			yExpr: fmt.Sprintf("(ih*zoom-oh)/2+((ih*zoom-oh)/2)*cos(%.4f+2*PI*on/(30*%d))", r.Float64()*2*math.Pi, 18+r.Intn(10)),
			zExpr: "1.0",
		},
		// 3: Slow vertical drift only (horizontal centered)
		{
			xExpr: "(iw*zoom-ow)/2",
			yExpr: fmt.Sprintf("abs(mod(on*%d+%d,2*(ih*zoom-oh))-(ih*zoom-oh))", 6+r.Intn(5), r.Intn(7680)),
			zExpr: fmt.Sprintf("1+0.03*(1+sin(%.4f+2*PI*on/(30*%d)))", r.Float64()*2*math.Pi, 15+r.Intn(8)),
		},
		// 4: Slow horizontal drift only (vertical centered)
		{
			xExpr: fmt.Sprintf("abs(mod(on*%d+%d,2*(iw*zoom-ow))-(iw*zoom-ow))", 6+r.Intn(5), r.Intn(4320)),
			yExpr: "(ih*zoom-oh)/2",
			zExpr: fmt.Sprintf("1+0.03*(1+sin(%.4f+2*PI*on/(30*%d)))", r.Float64()*2*math.Pi, 15+r.Intn(8)),
		},
		// 5: Zoom pulse — stays centered, pulses in/out
		{
			xExpr: "(iw*zoom-ow)/2",
			yExpr: "(ih*zoom-oh)/2",
			zExpr: fmt.Sprintf("1+0.08*(1+sin(%.4f+2*PI*on/(30*%d)))", r.Float64()*2*math.Pi, 8+r.Intn(6)),
		},
	}
	// Assign style by segment index so sequential segments always differ.
	return styles[segIdx%len(styles)]
}

// buildSegment creates a video segment: thumbnail image + trimmed audio slice.
// Each segment gets a distinct pan/zoom animation style.
func (g *Generator) buildSegment(ctx context.Context, thumbPath, audioPath, outPath string, startOffset float64, dur int, r *rand.Rand, segNum int, author, songTitle, bottomColor string) error {
	pan := pickPanStyle(segNum-1, r)

	topText := escapeFfmpegText(TopLabelText)
	labelText := fmt.Sprintf("#%d %s - %s", segNum, author, songTitle)
	if len(labelText) > 49 {
		labelText = wrapText(labelText, 49)
	}
	bottomText := escapeFfmpegText(labelText)
	textStyle := "fontsize=48:fontcolor=white:borderw=4:bordercolor=black:box=1:boxcolor=black@0.6:boxborderw=12"
	filterComplex := fmt.Sprintf(
		"[0:v]scale=3240:5760:force_original_aspect_ratio=increase,crop=3240:5760,"+
			"zoompan=z='%s':x='%s':y='%s':fps=30:d=1:s=1080x1920,setsar=1,"+
			"drawtext=text='%s':%s:x=(w-tw)/2:y=100,"+
			"drawtext=text='%s':fontsize=64:fontcolor=%s:borderw=4:bordercolor=black:box=1:boxcolor=black@0.6:boxborderw=12:x=(w-tw)/2:y=h-th-60"+
			"[out]",
		pan.zExpr, pan.xExpr, pan.yExpr,
		topText, textStyle,
		bottomText, bottomColor,
	)

	ffmpegSem <- struct{}{}
	defer func() { <-ffmpegSem }()

	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner",
		"-loglevel", "error",
		"-threads", "1",
		"-filter_threads", "1",
		"-filter_complex_threads", "1",
		"-loop", "1",
		"-i", thumbPath,
		"-ss", fmt.Sprintf("%.2f", startOffset),
		"-i", audioPath,
		"-t", fmt.Sprintf("%d", dur),
		"-filter_complex", filterComplex,
		"-map", "[out]",
		"-map", "1:a",
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-x264-params", "threads=1",
		"-c:a", "aac",
		"-b:a", "192k",
		"-pix_fmt", "yuv420p",
		"-r", "30",
		"-shortest",
		"-y",
		"-strict", "-2",
		outPath,
	)
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := stderr.String()
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("ffmpeg segment: %s", msg)
	}
	return nil
}

// concatenate joins segment files into a single output MP4.
// Uses the concat filter (not demuxer) to ensure A/V sync at segment boundaries —
// the concat demuxer with -c copy can cause audio to lag by one AAC frame delay.
func (g *Generator) concatenate(ctx context.Context, segments []string, outPath string) error {
	ffmpegSem <- struct{}{}
	defer func() { <-ffmpegSem }()

	// Build -i args and filter_complex concat expression.
	args := []string{"-hide_banner", "-loglevel", "error"}
	for _, p := range segments {
		args = append(args, "-i", p)
	}

	n := len(segments)
	var fc strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&fc, "[%d:v][%d:a]", i, i)
	}
	fmt.Fprintf(&fc, "concat=n=%d:v=1:a=1[outv][outa]", n)

	args = append(args,
		"-filter_complex", fc.String(),
		"-map", "[outv]",
		"-map", "[outa]",
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-crf", "30",
		"-x264-params", "threads=1",
		"-c:a", "aac",
		"-b:a", "128k",
		"-pix_fmt", "yuv420p",
		"-r", "30",
		"-y",
		outPath,
	)

	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := stderr.String()
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("ffmpeg concat: %s", msg)
	}
	return nil
}

func (g *Generator) maxMixtapes() int {
	if g.cfg.MaxMixtapes > 0 {
		return g.cfg.MaxMixtapes
	}
	return maxMixtapes
}

func (g *Generator) loadIndex(ctx context.Context) (MixtapeIndex, error) {
	var idx MixtapeIndex
	found, err := g.s3.ReadJSON(ctx, mixtapesJSONKey, &idx)
	if err != nil {
		return idx, fmt.Errorf("read mixtapes.json: %w", err)
	}
	if !found {
		idx.Items = []Mixtape{}
	}
	return idx, nil
}

func (g *Generator) saveIndex(ctx context.Context, idx MixtapeIndex) error {
	return g.s3.WriteJSON(ctx, mixtapesJSONKey, &idx)
}

func (g *Generator) trimOldest(ctx context.Context, idx MixtapeIndex, limit int) (MixtapeIndex, error) {
	sort.Slice(idx.Items, func(i, j int) bool {
		return idx.Items[i].CreatedAt.Before(idx.Items[j].CreatedAt)
	})
	for len(idx.Items) > limit {
		oldest := idx.Items[0]
		_ = g.s3.Delete(ctx, oldest.VideoKey)
		_ = g.s3.Delete(ctx, oldest.ThumbKey)
		idx.Items = idx.Items[1:]
	}
	idx.UpdatedAt = time.Now()
	if err := g.saveIndex(ctx, idx); err != nil {
		return idx, err
	}
	return idx, nil
}
