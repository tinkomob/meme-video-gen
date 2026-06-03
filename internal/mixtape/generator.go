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
	segmentDuration = 7  // seconds per segment
	maxMixtapes     = 5
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

		// Determine valid start offset
		duration := song.DurationS
		if duration <= float64(segmentDuration)+1 {
			duration = float64(segmentDuration) + 2
		}
		r := rand.New(rand.NewSource(time.Now().UnixNano() + int64(i)))
		maxStart := duration - float64(segmentDuration) - 0.5
		if maxStart < 0 {
			maxStart = 0
		}
		startOffset := r.Float64() * maxStart

		segPath := filepath.Join(tmpDir, fmt.Sprintf("seg%d.mp4", i))
		if err := g.buildSegment(ctx, thumbPath, audioPath, segPath, startOffset, segmentDuration, r); err != nil {
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

// buildSegment creates a video segment: thumbnail image + trimmed audio slice.
// The thumbnail is upscaled to 3x output size and animated with a bouncing pan
// (DVD screensaver style) and smooth zoom oscillation between 3x and 4x.
func (g *Generator) buildSegment(ctx context.Context, thumbPath, audioPath, outPath string, startOffset float64, dur int, r *rand.Rand) error {
	// Random movement params for each segment
	xSpeed := 12 + r.Intn(8)                       // 12–19 px/frame horizontal drift
	ySpeed := 15 + r.Intn(10)                      // 15–24 px/frame vertical drift
	xPhase := r.Intn(4320)                         // random start position in x
	yPhase := r.Intn(7680)                         // random start position in y
	zoomPeriod := 6 + r.Intn(8)                   // zoom cycle 6–13 seconds
	zoomPhase := r.Float64() * 2 * math.Pi        // random zoom phase

	// Scale thumbnail to 3x output (3240×5760) so there's room to pan.
	// zoompan z oscillates 1.0→1.33, giving effective zoom 3x→4x total.
	// x/y bounce within the 2160×3840 extra space (3240-1080, 5760-1920).
	filterComplex := fmt.Sprintf(
		"[0:v]scale=3240:5760:force_original_aspect_ratio=increase,crop=3240:5760,"+
			"zoompan=z='1+0.165*(1+sin(%.4f+2*PI*on/(30*%d)))':"+
			"x='abs(mod(on*%d+%d,2*(iw*zoom-ow))-(iw*zoom-ow))':"+
			"y='abs(mod(on*%d+%d,2*(ih*zoom-oh))-(ih*zoom-oh))':"+
			"fps=30:d=1:s=1080x1920,setsar=1[out]",
		zoomPhase, zoomPeriod,
		xSpeed, xPhase,
		ySpeed, yPhase,
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
func (g *Generator) concatenate(ctx context.Context, segments []string, outPath string) error {
	// Write concat list
	listPath := outPath + ".txt"
	var sb strings.Builder
	for _, p := range segments {
		sb.WriteString(fmt.Sprintf("file '%s'\n", p))
	}
	if err := os.WriteFile(listPath, []byte(sb.String()), 0644); err != nil {
		return err
	}
	defer os.Remove(listPath)

	ffmpegSem <- struct{}{}
	defer func() { <-ffmpegSem }()

	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner",
		"-loglevel", "error",
		"-f", "concat",
		"-safe", "0",
		"-i", listPath,
		"-c", "copy",
		"-y",
		outPath,
	)
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
