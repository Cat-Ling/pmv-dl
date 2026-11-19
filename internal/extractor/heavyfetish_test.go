package extractor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"pmv-dl/internal/common"
)

func TestHeavyFetishMatching(t *testing.T) {
	urls := []string{
		"https://heavyfetish.com/videos/1234/sample-video.html",
		"https://www.heavyfetish.com/videos/5678/",
		"https://heavyfetish.com/embed/1234/",
		"https://heavyfetish.com/categories/test/",
		"https://heavyfetish.com/tags/leather/",
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
		if ext.Name() != "heavyfetish" {
			t.Errorf("Find(%q) = %q, want 'heavyfetish'", uStr, ext.Name())
		}
	}
}

func TestHeavyFetishMockVideo(t *testing.T) {
	mockHTML := `<!doctype html>
<html>
<head>
  <title>Sample Fetish Video - HeavyFetish</title>
  <meta property="og:title" content="Sample Fetish Video">
</head>
<body>
  <h1>Sample Fetish Video</h1>
  <script>
    var trand1234 = {
      video_id: '1234',
      video_url: 'https://cdn.example.com/stream_480p.mp4',
      video_url_text: '480p',
      video_alt_url: 'https://cdn.example.com/stream_720p.mp4',
      video_alt_url_text: '720p',
      video_alt_url_hd: '1'
    };
    var player_obj = kt_player('kt_player', '/player/kt_player.swf', '100%', '100%', trand1234);
  </script>
</body>
</html>`

	ext := &HeavyFetishExtractor{}
	targetURL, _ := url.Parse("https://heavyfetish.com/videos/1234/sample-fetish-video.html")

	// Default quality should pick 720p
	res, err := ext.ExtractFromHTML(context.Background(), http.DefaultClient, targetURL, []byte(mockHTML), common.DownloadOptions{})
	if err != nil {
		t.Fatalf("ExtractFromHTML failed: %v", err)
	}
	if res.Type != ResultTypeVideo || res.Video == nil {
		t.Fatalf("expected video result, got: %+v", res)
	}
	if res.Video.Title != "Sample Fetish Video" {
		t.Errorf("expected title 'Sample Fetish Video', got %q", res.Video.Title)
	}
	if res.Video.StreamURL != "https://cdn.example.com/stream_720p.mp4" {
		t.Errorf("expected 720p stream, got %q", res.Video.StreamURL)
	}

	// 480p quality selection
	res480, err := ext.ExtractFromHTML(context.Background(), http.DefaultClient, targetURL, []byte(mockHTML), common.DownloadOptions{Quality: "480p"})
	if err != nil {
		t.Fatalf("ExtractFromHTML 480p failed: %v", err)
	}
	if res480.Video.StreamURL != "https://cdn.example.com/stream_480p.mp4" {
		t.Errorf("expected 480p stream, got %q", res480.Video.StreamURL)
	}
}

func TestHeavyFetishMockListing(t *testing.T) {
	mockListingHTML := `<!doctype html>
<html>
<head>
  <title>Leather Category - HeavyFetish</title>
</head>
<body>
  <h1>Leather Videos</h1>
  <div class="item">
    <a href="https://heavyfetish.com/videos/101/first-fetish-clip.html" title="First Fetish Clip">First Fetish Clip</a>
  </div>
  <div class="item">
    <a href="/videos/102/second-fetish-clip.html" title="Second Fetish Clip">Second Fetish Clip</a>
  </div>
</body>
</html>`

	ext := &HeavyFetishExtractor{}
	targetURL, _ := url.Parse("https://heavyfetish.com/categories/leather/")

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
	if res.Playlist.Entries[0].URL != "https://heavyfetish.com/videos/101/first-fetish-clip.html" {
		t.Errorf("unexpected entry 0 URL: %s", res.Playlist.Entries[0].URL)
	}
	if res.Playlist.Entries[0].Title != "First Fetish Clip" {
		t.Errorf("unexpected entry 0 Title: %s", res.Playlist.Entries[0].Title)
	}
	if res.Playlist.Entries[1].URL != "https://heavyfetish.com/videos/102/second-fetish-clip.html" {
		t.Errorf("unexpected entry 1 URL: %s", res.Playlist.Entries[1].URL)
	}
	if res.Playlist.Entries[1].Title != "Second Fetish Clip" {
		t.Errorf("unexpected entry 1 Title: %s", res.Playlist.Entries[1].Title)
	}
}

func TestHeavyFetishBunnyShieldDetection(t *testing.T) {
	shieldHTML := `<!DOCTYPE html>
<html>
<head><title>Establishing a secure connection ...</title>
<link href="/.bunny-shield/assets/challenge-styles.css" rel="stylesheet" />
</head>
<body><div class="status">Checking browser...</div></body>
</html>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("CDN-Challenge", "true")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(shieldHTML))
	}))
	defer server.Close()

	ext := &HeavyFetishExtractor{}
	serverURL, _ := url.Parse(server.URL + "/videos/1234/test.html")

	_, err := ext.Extract(context.Background(), server.Client(), serverURL, common.DownloadOptions{})
	if err == nil {
		t.Fatal("expected error on BunnyCDN Shield 403 response, got nil")
	}
	if !strings.Contains(err.Error(), "BunnyCDN Shield") && !strings.Contains(err.Error(), "403") {
		t.Errorf("expected error mentioning BunnyCDN Shield or 403, got: %v", err)
	}
}

func TestHeavyFetishLiveWithBrowserCookies(t *testing.T) {
	client, err := common.NewHTTPClient()
	if err != nil {
		t.Fatal(err)
	}
	targetURL, _ := url.Parse("https://heavyfetish.com/videos/132311/all-natural-kasey-warner-restrained-and-used-hard/")
	opts := common.DownloadOptions{
		Quality:            "best",
		CookiesFromBrowser: "chrome",
	}
	if err := common.ApplyOptionsToClient(client, opts, targetURL); err != nil {
		t.Skipf("browser cookies unavailable or expired: %v", err)
	}
	if common.CookieHeaderForURL(client.Jar, targetURL) == "" {
		t.Skip("no heavyfetish cookies present in chrome jar")
	}

	ext := &HeavyFetishExtractor{}
	res, err := ext.Extract(context.Background(), client, targetURL, opts)
	if err != nil {
		t.Fatalf("Extract failed with browser cookies: %v", err)
	}
	if res == nil || res.Video == nil {
		t.Fatalf("expected VideoResult, got %+v", res)
	}
	t.Logf("Extracted Title: %s", res.Video.Title)
	t.Logf("Extracted StreamURL: %s", res.Video.StreamURL)
}
