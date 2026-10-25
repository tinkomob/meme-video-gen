package snippet

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"meme-video-gen/internal"
	"meme-video-gen/internal/audio"
	"meme-video-gen/internal/model"
	"meme-video-gen/internal/s3"
)

type selectionS3 struct {
	s3.Client
	index model.SongsIndex
}

func (s selectionS3) ReadJSON(_ context.Context, key string, out any) (bool, error) {
	if key != "songs.json" {
		return false, fmt.Errorf("wrong song index")
	}
	data, err := json.Marshal(s.index)
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(data, out)
}

func TestSnippetDefaultChoosesRandomSong(t *testing.T) {
	store := selectionS3{index: model.SongsIndex{Items: []model.Song{{ID: "AAAAAAAAAAA", AudioKey: "a", DurationS: 20}, {ID: "BBBBBBBBBBB", AudioKey: "b", DurationS: 40}, {ID: "CCCCCCCCCCC", AudioKey: "c", DurationS: 30}, {ID: "DDDDDDDDDDD", AudioKey: "d", DurationS: 15}}}}
	cfg := internal.Config{SongsJSONKey: "songs.json"}
	g := NewGenerator(cfg, store, audio.NewIndexer(cfg, store, nil), nil)
	seen := map[string]bool{}
	for i := 0; i < 128; i++ {
		song, err := g.snippetSong(context.Background(), "")
		if err != nil {
			t.Fatal(err)
		}
		if song.ID != "AAAAAAAAAAA" && song.ID != "BBBBBBBBBBB" && song.ID != "CCCCCCCCCCC" {
			t.Fatal("song is not from the library")
		}
		seen[song.ID] = true
	}
	if len(seen) < 2 {
		t.Fatal("default snippet always chooses the same song")
	}
	song, err := g.snippetSong(context.Background(), "BBBBBBBBBBB")
	if err != nil || song.ID != "BBBBBBBBBBB" {
		t.Fatal("specific song selection stopped working")
	}
}
