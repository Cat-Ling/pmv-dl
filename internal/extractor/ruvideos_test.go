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

func TestRuvideosMatching(t *testing.T) {
	urls := []string{
		"https://www.ruvideos.net/videos/42204/",
		"https://ruvideos.net/videos/42204/",
		"https://br.ruvideos.net/videos/42204/",
		"https://www.ruvideos.net/embed/42204",
		"https://www.ruvideos.net/tags/cartoon/",
		"https://www.ruvideos.net/categories/hairy-pussy/",
		"https://www.ruvideos.net/playlists/38/anal/",
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
		if ext.Name() != "ruvideos" {
			t.Errorf("Find(%q) = %q, want 'ruvideos'", uStr, ext.Name())
		}
	}
}

func TestRuvideosMockVideo(t *testing.T) {
	mockHTML := `<!doctype html>
<html>
<head>
  <title>Mock Anime Fantasy Adventure | Ruvideos.net</title>
</head>
<body>
  <h1>Mock Anime Fantasy Adventure</h1>
  <script>
    var flashvars = {
      video_id: '42204',
      video_title: 'Mock Anime Fantasy Adventure',
      license_code: '$531739217543007',
      video_url: 'https://www.ruvideos.net/get_file/6/stream_360p.mp4/',
      video_url_text: '360p',
      video_alt_url: 'https://www.ruvideos.net/get_file/6/stream_720p.mp4/?br=744',
      video_alt_url_text: '720p',
      video_alt_url_hd: '1'
    };
  </script>
</body>
</html>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(mockHTML))
	}))
	defer server.Close()

	ext := &RuvideosExtractor{}
	serverURL, _ := url.Parse(server.URL + "/videos/42204/")

	// Default quality should pick best (720p)
	res, err := ext.ExtractFromHTML(context.Background(), server.Client(), serverURL, []byte(mockHTML), common.DownloadOptions{})
	if err != nil {
		t.Fatalf("ExtractFromHTML failed: %v", err)
	}
	if res.Type != ResultTypeVideo || res.Video == nil {
		t.Fatalf("expected video result, got: %+v", res)
	}
	if res.Video.Title != "Mock Anime Fantasy Adventure" {
		t.Errorf("expected title 'Mock Anime Fantasy Adventure', got %q", res.Video.Title)
	}
	if res.Video.StreamURL != "https://www.ruvideos.net/get_file/6/stream_720p.mp4/?br=744" {
		t.Errorf("expected 720p stream, got %q", res.Video.StreamURL)
	}

	// 360p quality selection
	res360, err := ext.ExtractFromHTML(context.Background(), server.Client(), serverURL, []byte(mockHTML), common.DownloadOptions{Quality: "360p"})
	if err != nil {
		t.Fatalf("ExtractFromHTML 360p failed: %v", err)
	}
	if res360.Video.StreamURL != "https://www.ruvideos.net/get_file/6/stream_360p.mp4/" {
		t.Errorf("expected 360p stream, got %q", res360.Video.StreamURL)
	}
}

func TestRuvideosMockEmbed(t *testing.T) {
	mockEmbedHTML := `<!doctype html>
<html>
<head>
  <title>Mock Embed Feature / Embed плеер</title>
</head>
<body>
  <video id="my-video">
    <source src="https://www.ruvideos.net/get_file/6/embed_hq.mp4/" type="video/mp4" label="360P">
  </video>
</body>
</html>`

	ext := &RuvideosExtractor{}
	targetURL, _ := url.Parse("https://www.ruvideos.net/embed/99999")

	res, err := ext.extractVideo(context.Background(), http.DefaultClient, targetURL, mockEmbedHTML, common.DownloadOptions{})
	if err != nil {
		t.Fatalf("extractVideo embed failed: %v", err)
	}
	if res.Type != ResultTypeVideo || res.Video == nil {
		t.Fatalf("expected video result, got: %+v", res)
	}
	if res.Video.Title != "Mock Embed Feature" {
		t.Errorf("expected title 'Mock Embed Feature', got %q", res.Video.Title)
	}
	if res.Video.StreamURL != "https://www.ruvideos.net/get_file/6/embed_hq.mp4/" {
		t.Errorf("expected stream 'https://www.ruvideos.net/get_file/6/embed_hq.mp4/', got %q", res.Video.StreamURL)
	}
}

func TestRuvideosMockListing(t *testing.T) {
	mockListingHTML := `<!doctype html>
<html>
<head>
  <title>Videos Tagged with Cartoon | Ruvideos.net</title>
</head>
<body>
  <h1>Fresh Videos Tagged with Cartoon</h1>
  <div id="list_videos_common_videos_list_items">
    <div class="item">
      <a href="https://www.ruvideos.net/videos/42219/" title="First Cartoon Feature">
        <strong class="title">First Cartoon Feature</strong>
      </a>
    </div>
    <div class="item">
      <a href="/videos/42200/" title="Second Cartoon Feature">
        <strong class="title">Second Cartoon Feature</strong>
      </a>
    </div>
  </div>
</body>
</html>`

	ext := &RuvideosExtractor{}
	targetURL, _ := url.Parse("https://www.ruvideos.net/tags/cartoon/")

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
	if res.Playlist.Entries[0].URL != "https://www.ruvideos.net/videos/42219/" {
		t.Errorf("unexpected entry 0 URL: %s", res.Playlist.Entries[0].URL)
	}
	if res.Playlist.Entries[0].Title != "First Cartoon Feature" {
		t.Errorf("unexpected entry 0 Title: %s", res.Playlist.Entries[0].Title)
	}
	if res.Playlist.Entries[1].URL != "https://www.ruvideos.net/videos/42200/" {
		t.Errorf("unexpected entry 1 URL: %s", res.Playlist.Entries[1].URL)
	}
	if res.Playlist.Entries[1].Title != "Second Cartoon Feature" {
		t.Errorf("unexpected entry 1 Title: %s", res.Playlist.Entries[1].Title)
	}
}

func TestRuvideosLiveVideo(t *testing.T) {
	client, err := common.NewHTTPClient()
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	targetURL, _ := url.Parse("https://www.ruvideos.net/videos/42204/")
	ext := Find(targetURL)
	if ext == nil {
		t.Fatal("ruvideos extractor not found for target URL")
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

func TestRuvideosLiveEmbed(t *testing.T) {
	client, err := common.NewHTTPClient()
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	targetURL, _ := url.Parse("https://www.ruvideos.net/embed/42204")
	ext := Find(targetURL)
	if ext == nil {
		t.Fatal("ruvideos extractor not found for target URL")
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
	t.Logf("Extracted Embed Title: %s", res.Video.Title)
	t.Logf("Extracted Embed Stream: %s", res.Video.StreamURL)
}
