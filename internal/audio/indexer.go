package audio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kkdai/youtube/v2"
	"github.com/samber/lo"
	"github.com/tidwall/gjson"

	"meme-video-gen/internal"
	"meme-video-gen/internal/logging"
	"meme-video-gen/internal/model"
	"meme-video-gen/internal/s3"
)

type Indexer struct {
	cfg internal.Config
	s3  s3.Client
	log *logging.Logger
}

func NewIndexer(cfg internal.Config, s3c s3.Client, log *logging.Logger) *Indexer {
	return &Indexer{cfg: cfg, s3: s3c, log: log}
}

func (idx *Indexer) EnsureSongs(ctx context.Context) error {
	idx.log.Infof("audio: ensuring songs index - START")
	var songsIdx model.SongsIndex
	found, err := idx.s3.ReadJSON(ctx, idx.cfg.SongsJSONKey, &songsIdx)
	if err != nil {
		idx.log.Errorf("audio: read songs.json failed: %v", err)
		return fmt.Errorf("read songs.json: %w", err)
	}
	if !found {
		songsIdx = model.SongsIndex{Items: []model.Song{}}
		idx.log.Infof("audio: creating new songs index (not found in S3)")
	} else {
		idx.log.Infof("audio: loaded existing songs index with %d items", len(songsIdx.Items))
	}

	idx.log.Infof("audio: loading playlists configuration...")
	playlists, err := idx.loadPlaylistsJSON(ctx)
	if err != nil {
		idx.log.Errorf("audio: load music_playlists.json failed: %v", err)
		idx.log.Infof("audio: no playlists configured, skipping song download")
		return nil
	}

	if len(playlists) == 0 {
		idx.log.Infof("audio: no playlists found in music_playlists.json")
		return nil
	}

	idx.log.Infof("audio: found %d playlists", len(playlists))

	client := youtube.Client{}
	newSongsCount := 0
	for playlistIdx, plURL := range playlists {
		idx.log.Infof("audio: fetching playlist %d/%d: %s", playlistIdx+1, len(playlists), plURL)
		pl, err := client.GetPlaylist(plURL)
		if err != nil {
			idx.log.Errorf("audio: fetch playlist %s failed: %v", plURL, err)
			continue
		}
		idx.log.Infof("audio: playlist %s has %d videos", plURL, len(pl.Videos))

		for _, entry := range pl.Videos {
			if idx.songExists(songsIdx, entry.ID) {
				continue
			}
			idx.log.Infof("audio: downloading new song: %s (%s)", entry.Title, entry.ID)
			if err := idx.downloadAndStoreSong(ctx, &client, entry, &songsIdx); err != nil {
				idx.log.Errorf("download song %s: %v", entry.ID, err)
			} else {
				newSongsCount++
				idx.log.Infof("audio: song downloaded successfully: %s", entry.ID)

				// Update songs.json in S3 after each successful download
				songsIdx.UpdatedAt = time.Now()
				if err := idx.s3.WriteJSON(ctx, idx.cfg.SongsJSONKey, &songsIdx); err != nil {
					idx.log.Errorf("audio: failed to update songs.json after song %s: %v", entry.ID, err)
				} else {
					idx.log.Infof("audio: songs.json updated in S3 (%d total songs)", len(songsIdx.Items))
				}
			}
		}
	}

	idx.log.Infof("audio: all playlists processed - total %d songs, %d new", len(songsIdx.Items), newSongsCount)
	return nil
}

func (idx *Indexer) downloadAndStoreSong(ctx context.Context, client *youtube.Client, entry *youtube.PlaylistEntry, songsIdx *model.SongsIndex) error {
	idx.log.Infof("audio: getting video details for %s", entry.ID)
	video, err := client.GetVideo(entry.ID)
	if err != nil {
		idx.log.Errorf("audio: get video %s: %v", entry.ID, err)
		return err
	}
	formats := video.Formats.WithAudioChannels()
	if len(formats) == 0 {
		idx.log.Errorf("audio: no audio formats for %s", entry.ID)
		return fmt.Errorf("no audio formats")
	}
	format := formats[0]

	tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("audio-%s.m4a", entry.ID))
	defer os.Remove(tmpFile)

	f, err := os.Create(tmpFile)
	if err != nil {
		idx.log.Errorf("audio: create temp file: %v", err)
		return err
	}

	idx.log.Infof("audio: downloading stream for %s", entry.ID)
	stream, _, err := client.GetStream(video, &format)
	if err != nil {
		f.Close()
		idx.log.Errorf("audio: get stream %s: %v", entry.ID, err)
		return err
	}

	if _, err := io.Copy(f, stream); err != nil {
		f.Close()
		stream.Close()
		idx.log.Errorf("audio: copy stream %s: %v", entry.ID, err)
		return err
	}
	f.Close()
	stream.Close()
	idx.log.Infof("audio: stream downloaded successfully: %s", entry.ID)

	data, err := os.ReadFile(tmpFile)
	if err != nil {
		idx.log.Errorf("audio: read temp file: %v", err)
		return err
	}

	idx.log.Infof("audio: uploading to S3: %s", entry.ID)
	h := sha256.Sum256(data)
	hash := hex.EncodeToString(h[:])

	key := idx.cfg.SongsPrefix + entry.ID + ".m4a"
	if err := idx.s3.PutBytes(ctx, key, data, "audio/mp4"); err != nil {
		idx.log.Errorf("audio: upload to S3 %s: %v", entry.ID, err)
		return err
	}
	idx.log.Infof("audio: S3 upload completed: %s -> %s", entry.ID, key)

	songsIdx.Items = append(songsIdx.Items, model.Song{
		ID:         entry.ID,
		Title:      entry.Title,
		Author:     cleanAuthorName(video.Author),
		SourceURL:  "https://www.youtube.com/watch?v=" + entry.ID,
		AudioKey:   key,
		DurationS:  float64(entry.Duration.Seconds()),
		AddedAt:    time.Now(),
		LastSeenAt: time.Now(),
		SHA256:     hash,
	})
	return nil
}

func cleanAuthorName(author string) string {
	// Remove " - Topic" suffix that YouTube adds to official audio channels
	return strings.TrimSuffix(author, " - Topic")
}

func (idx *Indexer) songExists(songsIdx model.SongsIndex, id string) bool {
	return lo.ContainsBy(songsIdx.Items, func(s model.Song) bool { return s.ID == id })
}

func (idx *Indexer) loadPlaylistsJSON(ctx context.Context) ([]string, error) {
	// Try to load from S3 first, then fallback to local files
	key := idx.cfg.PayloadPrefix + "music_playlists.json"
	data, _, err := idx.s3.GetBytes(ctx, key)
	if err == nil && data != nil {
		idx.log.Infof("audio: loaded music_playlists.json from S3: %s", key)
		res := gjson.GetBytes(data, "@this")
		if !res.IsArray() {
			return nil, fmt.Errorf("music_playlists.json must be array")
		}
		var out []string
		for _, item := range res.Array() {
			s := strings.TrimSpace(item.String())
			if s != "" {
				out = append(out, s)
			}
		}
		return out, nil
	}

	// Fallback to local files
	idx.log.Infof("audio: S3 load failed (%v), trying local paths", err)
	paths := []string{
		"music_playlists.json",
		"cmd/music_playlists.json",
		"internal/audio/music_playlists.json",
		"./internal/audio/music_playlists.json",
	}

	var localData []byte
	var lastErr error

	for _, path := range paths {
		if d, readErr := os.ReadFile(path); readErr == nil {
			localData = d
			break
		} else {
			lastErr = readErr
		}
	}

	if localData == nil {
		return nil, fmt.Errorf("music_playlists.json not found in any path: %v", lastErr)
	}

	res := gjson.GetBytes(localData, "@this")
	if !res.IsArray() {
		return nil, fmt.Errorf("music_playlists.json must be array")
	}
	var out []string
	for _, item := range res.Array() {
		s := strings.TrimSpace(item.String())
		if s != "" {
			out = append(out, s)
		}
	}
	return out, nil
}

func (idx *Indexer) GetRandomSong(ctx context.Context) (*model.Song, error) {
	var songsIdx model.SongsIndex
	found, err := idx.s3.ReadJSON(ctx, idx.cfg.SongsJSONKey, &songsIdx)
	if err != nil || !found || len(songsIdx.Items) == 0 {
		return nil, fmt.Errorf("no songs available")
	}
	i := randomIndex(len(songsIdx.Items))
	return &songsIdx.Items[i], nil
}

func (idx *Indexer) DownloadSongToTemp(ctx context.Context, song *model.Song) (string, error) {
	if song == nil {
		return "", fmt.Errorf("song is nil")
	}
	if song.AudioKey == "" {
		return "", fmt.Errorf("song.AudioKey is empty (song ID: %s)", song.ID)
	}

	data, _, err := idx.s3.GetBytes(ctx, song.AudioKey)
	if err != nil {
		return "", fmt.Errorf("s3.GetBytes failed for key '%s': %w", song.AudioKey, err)
	}
	tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("song-%s.m4a", song.ID))
	return tmpFile, os.WriteFile(tmpFile, data, 0o644)
}
