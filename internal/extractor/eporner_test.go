package extractor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"pmv-dl/internal/common"
)

func TestEpornerMatching(t *testing.T) {
	urls := []string{
		"http://www.eporner.com/hd-porn/95008/Infamous-Tiffany-Teen-Strip-Tease-Video/",
		"https://www.eporner.com/hd-porn/3YRUtzMcWn0/Star-Wars-XXX-Parody/",
		"https://www.eporner.com/video-FJsA19J3Y3H/one-of-the-greats/",
		"https://eporner.com/embed/3YRUtzMcWn0/",
		"https://www.eporner.com/category/amateur/",
	}

	for _, uStr := range urls {
		u, err := url.Parse(uStr)
		if err != nil {
			t.Fatalf("failed to parse %s: %v", uStr, err)
		}
		ext := Find(u)
		if ext == nil {
			t.Errorf("Find(%q) returned nil", uStr)
			continue
		}
		if ext.Name() != "eporner" {
			t.Errorf("Find(%q) = %q, want 'eporner'", uStr, ext.Name())
		}
	}
}

func TestEpornerHashCalculation(t *testing.T) {
	// Sample hash from yt-dlp test suite
	rawHash := "68b329da9893e34099c7d8ad5cb9c940"
	expected := "t1tfq216c20jk16o2ld9pq7m0w"

	got, err := calculateEpornerHash(rawHash)
	if err != nil {
		t.Fatalf("calculateEpornerHash failed: %v", err)
	}
	if got != expected {
		t.Errorf("got %q, want %q", got, expected)
	}
}

func TestEpornerMockVideo(t *testing.T) {
	mockHTML := `<!doctype html>
<html>
<head>
  <title>Epic Feature Showcase - EPORNER</title>
  <meta property="og:title" content="Epic Feature Showcase">
</head>
<body>
  <h1>Epic Feature Showcase</h1>
  <script>
    EP.video.player.hash = '68b329da9893e34099c7d8ad5cb9c940';
  </script>
</body>
</html>`

	mockXHR := `{
  "available": true,
  "title": "Epic Feature Showcase",
  "sources": {
    "mp4": {
      "720p": {"src": "https://cdn.example.com/stream_720p.mp4"},
      "1080p": {"src": "https://cdn.example.com/stream_1080p.mp4"},
      "480p": {"src": "https://cdn.example.com/stream_480p.mp4"}
    }
  }
}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/xhr/video/3YRUtzMcWn0" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(mockXHR))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(mockHTML))
	}))
	defer server.Close()

	ext := &EpornerExtractor{}
	serverURL, _ := url.Parse(server.URL + "/hd-porn/3YRUtzMcWn0/epic-feature-showcase/")

	// Default quality should pick 1080p
	res, err := ext.Extract(context.Background(), server.Client(), serverURL, common.DownloadOptions{})
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}
	if res.Type != ResultTypeVideo || res.Video == nil {
		t.Fatalf("expected video result, got: %+v", res)
	}
	if res.Video.Title != "Epic Feature Showcase" {
		t.Errorf("expected title 'Epic Feature Showcase', got %q", res.Video.Title)
	}
	if res.Video.StreamURL != "https://cdn.example.com/stream_1080p.mp4" {
		t.Errorf("expected 1080p stream, got %q", res.Video.StreamURL)
	}

	// 720p quality selection
	res720, err := ext.Extract(context.Background(), server.Client(), serverURL, common.DownloadOptions{Quality: "720p"})
	if err != nil {
		t.Fatalf("Extract 720p failed: %v", err)
	}
	if res720.Video.StreamURL != "https://cdn.example.com/stream_720p.mp4" {
		t.Errorf("expected 720p stream, got %q", res720.Video.StreamURL)
	}
}

func TestEpornerMockListing(t *testing.T) {
	mockListingHTML := `<!doctype html>
<html>
<head>
  <title>Amateur Videos - EPORNER</title>
</head>
<body>
  <h1>Amateur Videos</h1>
  <div class="mbinfo">
    <a href="https://www.eporner.com/video-ABC12345/first-sample-clip/" title="First Sample Clip">First Sample Clip</a>
  </div>
  <div class="mbinfo">
    <a href="/video-XYZ67890/second-sample-clip/" title="Second Sample Clip">Second Sample Clip</a>
  </div>
</body>
</html>`

	ext := &EpornerExtractor{}
	targetURL, _ := url.Parse("https://www.eporner.com/category/amateur/")

	res, err := ext.ExtractFromHTML(context.Background(), http.DefaultClient, targetURL, []byte(mockListingHTML), common.DownloadOptions{})
	if err != nil {
		t.Fatalf("ExtractFromHTML listing failed: %v", err)
	}
	if res.Type != ResultTypePlaylist || res.Playlist == nil {
		t.Fatalf("expected playlist result, got: %+v", res)
	}
	if len(res.Playlist.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(res.Playlist.Entries))
	}
	if res.Playlist.Entries[0].URL != "https://www.eporner.com/video-ABC12345/first-sample-clip/" {
		t.Errorf("unexpected entry 0 URL: %s", res.Playlist.Entries[0].URL)
	}
	if res.Playlist.Entries[0].Title != "First Sample Clip" {
		t.Errorf("unexpected entry 0 Title: %s", res.Playlist.Entries[0].Title)
	}
	if res.Playlist.Entries[1].URL != "https://www.eporner.com/video-XYZ67890/second-sample-clip/" {
		t.Errorf("unexpected entry 1 URL: %s", res.Playlist.Entries[1].URL)
	}
	if res.Playlist.Entries[1].Title != "Second Sample Clip" {
		t.Errorf("unexpected entry 1 Title: %s", res.Playlist.Entries[1].Title)
	}
}
