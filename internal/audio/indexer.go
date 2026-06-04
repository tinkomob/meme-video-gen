package audio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/exec"
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

	cookiesFile, err := idx.fetchCookiesFile(ctx)
	if err != nil {
		idx.log.Warnf("audio: could not load youtube_cookies.txt from S3 (%v), proceeding without cookies", err)
	} else if cookiesFile != "" {
		defer os.Remove(cookiesFile)
		idx.log.Infof("audio: loaded youtube cookies from S3")
	}

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
			if err := idx.downloadAndStoreSong(ctx, entry, &songsIdx, cookiesFile); err != nil {
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

func (idx *Indexer) downloadAndStoreSong(ctx context.Context, entry *youtube.PlaylistEntry, songsIdx *model.SongsIndex, cookiesFile string) error {
	maxRetries := 3
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt))*time.Second + time.Duration(rand.Intn(1000))*time.Millisecond
			idx.log.Warnf("audio: retry %d/%d for %s after %v (last error: %v)", attempt, maxRetries, entry.ID, backoff, lastErr)
			time.Sleep(backoff)
		}

		lastErr = idx.downloadSongAttempt(ctx, entry, songsIdx, cookiesFile)
		if lastErr == nil {
			return nil
		}

		if !isRetryableError(lastErr) {
			idx.log.Errorf("audio: non-retryable error for %s: %v", entry.ID, lastErr)
			return lastErr
		}
	}

	idx.log.Errorf("audio: failed to download song %s after %d attempts: %v", entry.ID, maxRetries, lastErr)
	return lastErr
}

func (idx *Indexer) downloadSongAttempt(ctx context.Context, entry *youtube.PlaylistEntry, songsIdx *model.SongsIndex, cookiesFile string) error {
	tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("audio-%s.m4a", entry.ID))
	defer os.Remove(tmpFile)

	args := []string{
		"-x",
		"--audio-format", "m4a",
		"--no-playlist",
		"--quiet",
		"-o", tmpFile,
	}
	if cookiesFile != "" {
		args = append(args, "--cookies", cookiesFile)
	}
	args = append(args, "--", entry.ID)

	idx.log.Infof("audio: downloading stream via yt-dlp for %s", entry.ID)
	var stderr strings.Builder
	cmd := exec.CommandContext(ctx, "yt-dlp", args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("yt-dlp: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}
	idx.log.Infof("audio: stream downloaded successfully: %s", entry.ID)

	var probeStderr strings.Builder
	probeCmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-select_streams", "a:0",
		"-show_entries", "stream=duration",
		"-of", "default=noprint_wrappers=1",
		tmpFile,
	)
	probeCmd.Stderr = &probeStderr
	if err := probeCmd.Run(); err != nil {
		return fmt.Errorf("ffprobe validation failed (corrupt m4a): %s", strings.TrimSpace(probeStderr.String()))
	}

	data, err := os.ReadFile(tmpFile)
	if err != nil {
		return fmt.Errorf("read temp file: %w", err)
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
		Author:     entry.Author,
		SourceURL:  "https://www.youtube.com/watch?v=" + entry.ID,
		AudioKey:   key,
		DurationS:  float64(entry.Duration.Seconds()),
		AddedAt:    time.Now(),
		LastSeenAt: time.Now(),
		SHA256:     hash,
	})
	return nil
}

// fetchCookiesFile downloads youtube_cookies.txt from S3 (payload/youtube_cookies.txt)
// to a temp file and returns its path. Returns ("", nil) if the file doesn't exist in S3.
func (idx *Indexer) fetchCookiesFile(ctx context.Context) (string, error) {
	key := idx.cfg.PayloadPrefix + "youtube_cookies.txt"
	data, _, err := idx.s3.GetBytes(ctx, key)
	if err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "", nil
	}
	tmp, err := os.CreateTemp("", "yt-cookies-*.txt")
	if err != nil {
		return "", fmt.Errorf("create cookies temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", fmt.Errorf("write cookies temp file: %w", err)
	}
	tmp.Close()
	return tmp.Name(), nil
}

func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "403") ||
		strings.Contains(errStr, "429") ||
		strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "i/o timeout") ||
		strings.Contains(errStr, "TLS handshake") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "no such host") ||
		strings.Contains(errStr, "context deadline") ||
		strings.Contains(errStr, "EOF")
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

	// Stream directly from S3 to temp file — avoids holding entire audio in heap (5-15 MB)
	reader, err := idx.s3.GetReader(ctx, song.AudioKey)
	if err != nil {
		return "", fmt.Errorf("s3.GetReader failed for key '%s': %w", song.AudioKey, err)
	}
	defer reader.Reader.Close()

	tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("song-%s.m4a", song.ID))
	f, err := os.Create(tmpFile)
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	defer f.Close()

	written, err := io.Copy(f, reader.Reader)
	if err != nil {
		os.Remove(tmpFile)
		return "", fmt.Errorf("copy from S3 stream: %w", err)
	}
	if reader.Size > 0 && written != reader.Size {
		os.Remove(tmpFile)
		return "", fmt.Errorf("S3 stream truncated: got %d bytes, expected %d", written, reader.Size)
	}
	return tmpFile, nil
}
