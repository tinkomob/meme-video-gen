package video

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mowshon/moviego"
	"github.com/samber/lo"

	"meme-video-gen/internal"
	"meme-video-gen/internal/audio"
	"meme-video-gen/internal/logging"
	"meme-video-gen/internal/model"
	"meme-video-gen/internal/s3"
	"meme-video-gen/internal/sources"
)

type Generator struct {
	cfg        internal.Config
	s3         s3.Client
	log        *logging.Logger
	audioIdx   *audio.Indexer
	sourcesScr *sources.Scraper
}

func NewGenerator(cfg internal.Config, s3c s3.Client, log *logging.Logger, audioIdx *audio.Indexer, sourcesScr *sources.Scraper) *Generator {
	return &Generator{cfg: cfg, s3: s3c, log: log, audioIdx: audioIdx, sourcesScr: sourcesScr}
}

func (g *Generator) EnsureMemes(ctx context.Context) error {
	g.log.Infof("video: ensuring memes index")
	var memesIdx model.MemesIndex
	found, err := g.s3.ReadJSON(ctx, g.cfg.MemesJSONKey, &memesIdx)
	if err != nil {
		return fmt.Errorf("read memes.json: %w", err)
	}
	if !found {
		memesIdx = model.MemesIndex{Items: []model.Meme{}}
	}

	if len(memesIdx.Items) >= g.cfg.MaxMemes {
		g.log.Infof("video: already at max memes (%d)", len(memesIdx.Items))
		memesIdx.UpdatedAt = time.Now()
		_ = g.s3.WriteJSON(ctx, g.cfg.MemesJSONKey, &memesIdx)
		return nil
	}

	needed := g.cfg.MaxMemes - len(memesIdx.Items)
	for i := 0; i < needed; i++ {
		meme, err := g.generateOne(ctx, &memesIdx)
		if err != nil {
			g.log.Errorf("generate meme: %v", err)
			continue
		}
		memesIdx.Items = append(memesIdx.Items, *meme)
	}

	if len(memesIdx.Items) > g.cfg.MaxMemes {
		sorted := sortMemesByCreated(memesIdx.Items, false)
		toDelete := sorted[g.cfg.MaxMemes:]
		for _, m := range toDelete {
			_ = g.s3.Delete(ctx, m.VideoKey)
			_ = g.s3.Delete(ctx, m.ThumbKey)
		}
		memesIdx.Items = sorted[:g.cfg.MaxMemes]
		g.log.Infof("video: trimmed %d old memes", len(toDelete))
	}

	memesIdx.UpdatedAt = time.Now()
	if err := g.s3.WriteJSON(ctx, g.cfg.MemesJSONKey, &memesIdx); err != nil {
		return fmt.Errorf("write memes.json: %w", err)
	}
	g.log.Infof("video: memes updated (total=%d)", len(memesIdx.Items))
	return nil
}

func (g *Generator) generateOne(ctx context.Context, memesIdx *model.MemesIndex) (*model.Meme, error) {
	song, err := g.audioIdx.GetRandomSong(ctx)
	if err != nil {
		return nil, fmt.Errorf("get song: %w", err)
	}
	source, err := g.sourcesScr.GetRandomUnusedSource(ctx)
	if err != nil {
		return nil, fmt.Errorf("get source: %w", err)
	}

	audioPath, err := g.audioIdx.DownloadSongToTemp(ctx, song)
	if err != nil {
		return nil, fmt.Errorf("download song: %w", err)
	}
	defer os.Remove(audioPath)

	sourcePath, err := g.sourcesScr.DownloadSourceToTemp(ctx, source)
	if err != nil {
		return nil, fmt.Errorf("download source: %w", err)
	}
	defer os.Remove(sourcePath)

	videoPath := filepath.Join(os.TempDir(), fmt.Sprintf("meme-%d.mp4", time.Now().UnixNano()))
	defer os.Remove(videoPath)

	vid, err := moviego.Load(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("load source: %w", err)
	}

	// Apply random effects and output video (simplified – moviego API varies)
	if err := vid.ResizeByWidth(720).FadeIn(0, 1).FadeOut(7).Output(videoPath).Run(); err != nil {
		return nil, fmt.Errorf("generate video: %w", err)
	}

	videoData, err := os.ReadFile(videoPath)
	if err != nil {
		return nil, err
	}
	h := sha256.Sum256(videoData)
	hash := hex.EncodeToString(h[:])

	if g.memeExists(*memesIdx, hash) {
		return nil, fmt.Errorf("duplicate sha256")
	}

	memeID := fmt.Sprintf("meme-%d", time.Now().UnixNano())
	videoKey := g.cfg.MemesPrefix + memeID + ".mp4"
	thumbKey := g.cfg.MemesPrefix + memeID + "_thumb.jpg"

	if err := g.s3.PutBytes(ctx, videoKey, videoData, "video/mp4"); err != nil {
		return nil, fmt.Errorf("upload video: %w", err)
	}

	// Placeholder thumb (in real app, extract frame with ffmpeg)
	thumbData := []byte("placeholder")
	_ = g.s3.PutBytes(ctx, thumbKey, thumbData, "image/jpeg")

	if err := g.sourcesScr.MarkSourceUsed(ctx, source.ID); err != nil {
		g.log.Errorf("mark source used: %v", err)
	}

	title := fmt.Sprintf("%s — %s", song.Title, source.Kind)

	return &model.Meme{
		ID:        memeID,
		Title:     title,
		VideoKey:  videoKey,
		ThumbKey:  thumbKey,
		SongID:    song.ID,
		SourceID:  source.ID,
		CreatedAt: time.Now(),
		SHA256:    hash,
	}, nil
}

func (g *Generator) memeExists(idx model.MemesIndex, sha256 string) bool {
	return lo.ContainsBy(idx.Items, func(m model.Meme) bool { return m.SHA256 == sha256 })
}

func (g *Generator) GetRandomMeme(ctx context.Context) (*model.Meme, error) {
	var idx model.MemesIndex
	found, err := g.s3.ReadJSON(ctx, g.cfg.MemesJSONKey, &idx)
	if err != nil || !found || len(idx.Items) == 0 {
		return nil, fmt.Errorf("no memes available")
	}
	i := randomIndex(len(idx.Items))
	return &idx.Items[i], nil
}

func (g *Generator) DownloadMemeToTemp(ctx context.Context, meme *model.Meme) (string, error) {
	data, _, err := g.s3.GetBytes(ctx, meme.VideoKey)
	if err != nil {
		return "", err
	}
	tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("meme-%s.mp4", meme.ID))
	return tmpFile, os.WriteFile(tmpFile, data, 0o644)
}
