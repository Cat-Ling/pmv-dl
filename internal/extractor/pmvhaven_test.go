package extractor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"pmv-dl/internal/common"
)

func TestPMVHavenNuxtExtraction(t *testing.T) {
	// Simulated Nuxt 3 data payload
	mockHTML := `<!DOCTYPE html>
<html>
<head><title>Mock Haven - Playlist</title></head>
<body>
<script type="application/json" id="__NUXT_DATA__">[
  {"name": 1, "videoDetails": 2},
  "Test Playlist",
  [3, 4],
  {"_id": 5, "title": 6, "hlsMasterPlaylistUrl": 7},
  {"_id": 8, "title": 9, "hlsMasterPlaylistUrl": 10},
  "vid001",
  "First Video",
  "https://cdn.example.com/videos/v1/master.m3u8",
  "vid002",
  "Second Video",
  "https://cdn.example.com/videos/v2/master.m3u8"
]</script>
</body>
</html>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(mockHTML))
	}))
	defer server.Close()

	ext := &PMVHavenExtractor{}
	serverURL, _ := url.Parse(server.URL + "/playlists/test-id")

	// 1. Full playlist extraction
	res, err := ext.extractPlaylist(context.Background(), server.Client(), serverURL, common.DownloadOptions{})
	if err != nil {
		t.Fatalf("extractPlaylist failed: %v", err)
	}
	if res.Type != ResultTypePlaylist || res.Playlist == nil {
		t.Fatalf("expected playlist result, got: %+v", res)
	}
	if res.Playlist.Title != "Test Playlist" {
		t.Errorf("expected title 'Test Playlist', got %q", res.Playlist.Title)
	}
	if len(res.Playlist.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(res.Playlist.Entries))
	}
	if res.Playlist.Entries[0].Title != "First Video" || res.Playlist.Entries[0].StreamURL != "https://cdn.example.com/videos/v1/master.m3u8" {
		t.Errorf("entry 0 mismatch: %+v", res.Playlist.Entries[0])
	}
	if res.Playlist.Entries[1].Title != "Second Video" || res.Playlist.Entries[1].StreamURL != "https://cdn.example.com/videos/v2/master.m3u8" {
		t.Errorf("entry 1 mismatch: %+v", res.Playlist.Entries[1])
	}

	// 2. Extract with NoPlaylist option on playlist URL (should return 1st video)
	noPlRes, err := ext.Extract(context.Background(), server.Client(), serverURL, common.DownloadOptions{NoPlaylist: true})
	if err != nil {
		t.Fatalf("Extract with NoPlaylist failed: %v", err)
	}
	if noPlRes.Type != ResultTypeVideo || noPlRes.Video == nil {
		t.Fatalf("expected video result, got: %+v", noPlRes)
	}
	if noPlRes.Video.Title != "First Video" {
		t.Errorf("expected first video title, got %q", noPlRes.Video.Title)
	}
	if noPlRes.Video.StreamURL != "https://cdn.example.com/videos/v1/master.m3u8" {
		t.Errorf("expected first video stream URL, got %q", noPlRes.Video.StreamURL)
	}
}
