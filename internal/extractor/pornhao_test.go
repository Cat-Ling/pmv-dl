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

func TestPornHaoMatching(t *testing.T) {
	urls := []string{
		"https://pornhao.com/video/sample-slug-45270/",
		"https://www.pornhao.com/video/sample-slug-45270/",
		"https://pornhao.com/sportsmenki/",
		"https://pornhao.com/categories/",
		"https://pornozak.top/video/sample-45270/",
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
		if ext.Name() != "pornhao" {
			t.Errorf("Find(%q) = %q, want 'pornhao'", uStr, ext.Name())
		}
	}
}

func TestPornHaoMockVideo(t *testing.T) {
	mockHTML := `<!doctype html>
<html>
<head>
  <title>Sample Feature Video Title - PornHao</title>
  <meta property="og:title" content="Sample Feature Video Title">
</head>
<body>
  <h1>Sample Feature Video Title...</h1>
  <script>
    var player = new Playerjs({
      id: "player-box",
      file: '[720p]https://video31.pornocdn.info/stream_720p.mp4?token=123,[1080p]https://video4.pornocdn.info/stream_1080p.mp4?token=123,[480p]https://video4.pornocdn.info/stream_480p.mp4?token=123,[360p]https://video3.pornocdn.info/stream_360p.mp4?token=123',
      poster: '/images/45270.jpg',
      duration: 1200
    });
  </script>
</body>
</html>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(mockHTML))
	}))
	defer server.Close()

	ext := &PornHaoExtractor{}
	serverURL, _ := url.Parse(server.URL + "/video/sample-feature-video-title-45270/")

	// Default quality should pick 1080p
	res, err := ext.ExtractFromHTML(context.Background(), server.Client(), serverURL, []byte(mockHTML), common.DownloadOptions{})
	if err != nil {
		t.Fatalf("ExtractFromHTML failed: %v", err)
	}
	if res.Type != ResultTypeVideo || res.Video == nil {
		t.Fatalf("expected video result, got: %+v", res)
	}
	if res.Video.Title != "Sample Feature Video Title" {
		t.Errorf("expected title 'Sample Feature Video Title', got %q", res.Video.Title)
	}
	if res.Video.StreamURL != "https://video4.pornocdn.info/stream_1080p.mp4?token=123" {
		t.Errorf("expected 1080p stream, got %q", res.Video.StreamURL)
	}

	// 480p quality selection
	res480, err := ext.ExtractFromHTML(context.Background(), server.Client(), serverURL, []byte(mockHTML), common.DownloadOptions{Quality: "480p"})
	if err != nil {
		t.Fatalf("ExtractFromHTML 480p failed: %v", err)
	}
	if res480.Video.StreamURL != "https://video4.pornocdn.info/stream_480p.mp4?token=123" {
		t.Errorf("expected 480p stream, got %q", res480.Video.StreamURL)
	}
}

func TestPornHaoMockListing(t *testing.T) {
	mockListingHTML := `<!doctype html>
<html>
<head>
  <title>Sports Category - PornHao</title>
</head>
<body>
  <h1>Sports Videos</h1>
  <div class="video trailer">
    <a href="https://pornhao.com/video/first-clip-45257/" title="First Clip Title...">
      <p>First Clip Title...</p>
    </a>
  </div>
  <div class="video trailer">
    <a href="/video/second-clip-45247/" title="Second Clip Title...">
      <p>Second Clip Title...</p>
    </a>
  </div>
</body>
</html>`

	ext := &PornHaoExtractor{}
	targetURL, _ := url.Parse("https://pornhao.com/sportsmenki/")

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
	if res.Playlist.Entries[0].URL != "https://pornhao.com/video/first-clip-45257/" {
		t.Errorf("unexpected entry 0 URL: %s", res.Playlist.Entries[0].URL)
	}
	if res.Playlist.Entries[0].Title != "First Clip Title" {
		t.Errorf("unexpected entry 0 Title: %s", res.Playlist.Entries[0].Title)
	}
	if res.Playlist.Entries[1].URL != "https://pornhao.com/video/second-clip-45247/" {
		t.Errorf("unexpected entry 1 URL: %s", res.Playlist.Entries[1].URL)
	}
	if res.Playlist.Entries[1].Title != "Second Clip Title" {
		t.Errorf("unexpected entry 1 Title: %s", res.Playlist.Entries[1].Title)
	}
}

func TestPornHaoLiveVideo(t *testing.T) {
	client, err := common.NewHTTPClient()
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	targetURL, _ := url.Parse("https://pornhao.com/video/lola-grant-daet-druzhku-svoyu-uzkuyu-dyrochku-45270/")
	ext := Find(targetURL)
	if ext == nil {
		t.Fatal("pornhao extractor not found for target URL")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	res, err := ext.Extract(ctx, client, targetURL, common.DownloadOptions{})
	if err != nil {
		t.Skipf("skipping live test due to network error: %v", err)
		return
	}
	if res.Type != ResultTypeVideo || res.Video == nil {
		t.Fatalf("expected video result, got %+v", res)
	}
	if res.Video.StreamURL == "" {
		t.Errorf("expected non-empty stream URL")
	}
	if res.Video.Title == "" {
		t.Errorf("expected non-empty video title")
	}
	t.Logf("Extracted Title: %s", res.Video.Title)
	t.Logf("Extracted Stream: %s", res.Video.StreamURL)
}
