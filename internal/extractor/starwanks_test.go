package extractor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"pmv-dl/internal/common"
)

func TestStarWanksMatching(t *testing.T) {
	urls := []string{
		"https://starwanks.com/video?id=3225",
		"https://starwanks.com/video/?id=3225",
		"https://starwanks.com/video/3225",
		"https://starwanks.com/v/3225",
		"https://www.starwanks.com/video?id=3225",
		"https://starwanks.com/categories/stepsister/",
		"https://starwanks.com/pornstars/niks-indian/",
		"https://starwanks.com/search/?q=test",
		"https://starwanks.com/new/",
		"https://starwanks.com/best/",
		"https://starwanks.com/",
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
		if ext.Name() != "starwanks" {
			t.Errorf("Find(%q) = %q, want 'starwanks'", uStr, ext.Name())
		}
	}
}

func TestStarWanksMockVideo(t *testing.T) {
	tsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/stream/test" {
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			w.Write([]byte("#EXTM3U\n#EXT-X-VERSION:3\n"))
			return
		}
		http.NotFound(w, r)
	}))
	defer tsServer.Close()

	mockHTML := `<!doctype html>
<html>
<head>
  <title>Video: Sample Presentation Clip (15 min) - StarWanks</title>
</head>
<body>
  <h1 class="video-title">Sample Presentation Clip</h1>
  <video data-video-player>
    <source src="` + tsServer.URL + `/stream/test" type="application/x-mpegURL">
  </video>
</body>
</html>`

	ext := &StarWanksExtractor{}
	targetURL, _ := url.Parse(tsServer.URL + "/video?id=9999")

	res, err := ext.ExtractFromHTML(context.Background(), tsServer.Client(), targetURL, []byte(mockHTML), common.DownloadOptions{})
	if err != nil {
		t.Fatalf("ExtractFromHTML failed: %v", err)
	}
	if res.Type != ResultTypeVideo || res.Video == nil {
		t.Fatalf("expected video result, got %+v", res)
	}
	if res.Video.Title != "Sample Presentation Clip" {
		t.Errorf("expected title 'Sample Presentation Clip', got %q", res.Video.Title)
	}
	if res.Video.StreamURL != tsServer.URL+"/stream/test" {
		t.Errorf("expected stream URL %q, got %q", tsServer.URL+"/stream/test", res.Video.StreamURL)
	}
}

func TestStarWanksMockListing(t *testing.T) {
	mockHTML := `<!doctype html>
<html>
<head>
  <title>Featured Collection - StarWanks</title>
</head>
<body>
  <h1 aria-current="page">Featured Highlights</h1>
  <article class="video-card">
    <a class="video-thumb" href="/video?id=1001">
      <img src="thumb1.jpg" alt="Clip One">
    </a>
    <h3><a href="/video?id=1001">Clip One</a></h3>
  </article>
  <article class="video-card">
    <a class="video-thumb" href="/video?id=1002">
      <img src="thumb2.jpg" alt="Clip Two">
    </a>
    <h3><a href="/video?id=1002">Clip Two</a></h3>
  </article>
</body>
</html>`

	ext := &StarWanksExtractor{}
	targetURL, _ := url.Parse("https://starwanks.com/categories/sample/")

	res, err := ext.ExtractFromHTML(context.Background(), http.DefaultClient, targetURL, []byte(mockHTML), common.DownloadOptions{})
	if err != nil {
		t.Fatalf("ExtractFromHTML failed: %v", err)
	}
	if res.Type != ResultTypePlaylist || res.Playlist == nil {
		t.Fatalf("expected playlist result, got %+v", res)
	}
	if res.Playlist.Title != "Featured Highlights" {
		t.Errorf("expected playlist title 'Featured Highlights', got %q", res.Playlist.Title)
	}
	if len(res.Playlist.Entries) != 2 {
		t.Fatalf("expected 2 playlist entries, got %d", len(res.Playlist.Entries))
	}
	if res.Playlist.Entries[0].URL != "https://starwanks.com/video?id=1001" {
		t.Errorf("unexpected entry 0 URL: %q", res.Playlist.Entries[0].URL)
	}
	if res.Playlist.Entries[0].Title != "Clip One" {
		t.Errorf("unexpected entry 0 title: %q", res.Playlist.Entries[0].Title)
	}
}

func TestStarWanksLiveVideo(t *testing.T) {
	client, err := common.NewHTTPClient()
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	targetURL, _ := url.Parse("https://starwanks.com/video?id=3225")
	ext := Find(targetURL)
	if ext == nil {
		t.Fatal("starwanks extractor not found")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	res, err := ext.Extract(ctx, client, targetURL, common.DownloadOptions{})
	if err != nil {
		t.Fatalf("live video extraction failed: %v", err)
	}
	if res.Type != ResultTypeVideo || res.Video == nil {
		t.Fatalf("expected video result, got %+v", res)
	}
	if res.Video.StreamURL == "" {
		t.Error("stream URL is empty")
	}
	if res.Video.Title == "" {
		t.Error("title is empty")
	}
}

func TestStarWanksLiveCategory(t *testing.T) {
	client, err := common.NewHTTPClient()
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	targetURL, _ := url.Parse("https://starwanks.com/categories/stepsister/")
	ext := Find(targetURL)
	if ext == nil {
		t.Fatal("starwanks extractor not found")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	res, err := ext.Extract(ctx, client, targetURL, common.DownloadOptions{})
	if err != nil {
		t.Fatalf("live category extraction failed: %v", err)
	}
	if res.Type != ResultTypePlaylist || res.Playlist == nil {
		t.Fatalf("expected playlist result, got %+v", res)
	}
	if len(res.Playlist.Entries) == 0 {
		t.Error("expected non-empty entries in category playlist")
	}
}
