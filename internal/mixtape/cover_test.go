package mixtape

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchSongCover(t *testing.T) {
	var good, small bytes.Buffer
	if err := jpeg.Encode(&good, image.NewRGBA(image.Rect(0, 0, 640, 360)), nil); err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(&small, image.NewRGBA(image.Rect(0, 0, 120, 90)), nil); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		status int
		data   []byte
		valid  bool
	}{
		{"cover", 200, good.Bytes(), true},
		{"placeholder", 200, small.Bytes(), false},
		{"html", 200, []byte("<html>blocked</html>"), false},
		{"missing", 404, good.Bytes(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); w.Write(tc.data) }))
			defer server.Close()
			data, err := fetchSongCover(context.Background(), server.Client(), server.URL)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
			if tc.valid && !bytes.Equal(data, tc.data) {
				t.Fatal("cover changed")
			}
		})
	}
}
