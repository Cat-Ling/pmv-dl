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

func TestEbalovoMatching(t *testing.T) {
	urls := []string{
		"https://www.ebalovo.porn/video/analnaya-gruppovuha-s-russkoy/",
		"https://ebalovo.porn/video/analnaya-gruppovuha-s-russkoy/",
		"https://www.ebalovo.porn/embed/121464/",
		"https://www.ebalovo.porn/porno/anal-videos/",
		"https://mod.ebalovo.click/video/test/",
		"https://ebalovo.click/porno/best/",
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
		if ext.Name() != "ebalovo" {
			t.Errorf("Find(%q) = %q, want 'ebalovo'", uStr, ext.Name())
		}
	}
}

func TestEbalovoMockVideo(t *testing.T) {
	mockHTML := `<!doctype html>
<html>
<head>
  <title>😈 Sample Action Title, русское порно.</title>
  <meta property="og:title" content="Sample Action Title">
</head>
<body>
  <h1>Sample Action Title.</h1>
  <script>
    /* <![CDATA[ */
    var t12345 = {
      video_id: '121464',
      video_url: 'https://www.ebalovo.porn/video_file/sample_480p.mp4/?token=abc',
      video_url_text: '480p',
      postfix: '_480p.mp4',
      video_alt_url: 'https://www.ebalovo.porn/video_file/sample_720p.mp4/?token=def',
      video_alt_url_text: '720p',
      video_alt_url_hd: '1'
    };
    var player_obj = kt_player('kt_player', '/player/kt_player.swf', '100%', '100%', t12345);
    /* ]]> */
  </script>
</body>
</html>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(mockHTML))
	}))
	defer server.Close()

	ext := &EbalovoExtractor{}
	serverURL, _ := url.Parse(server.URL + "/video/sample-action-title/")

	// Test default quality (best/720p)
	res, err := ext.ExtractFromHTML(context.Background(), server.Client(), serverURL, []byte(mockHTML), common.DownloadOptions{})
	if err != nil {
		t.Fatalf("ExtractFromHTML failed: %v", err)
	}
	if res.Type != ResultTypeVideo || res.Video == nil {
		t.Fatalf("expected video result, got: %+v", res)
	}
	if res.Video.Title != "Sample Action Title" {
		t.Errorf("expected title 'Sample Action Title', got %q", res.Video.Title)
	}
	if res.Video.StreamURL != "https://www.ebalovo.porn/video_file/sample_720p.mp4/?token=def" {
		t.Errorf("expected 720p stream, got %q", res.Video.StreamURL)
	}

	// Test low/480p quality selection
	resLow, err := ext.ExtractFromHTML(context.Background(), server.Client(), serverURL, []byte(mockHTML), common.DownloadOptions{Quality: "480p"})
	if err != nil {
		t.Fatalf("ExtractFromHTML with 480p quality failed: %v", err)
	}
	if resLow.Video.StreamURL != "https://www.ebalovo.porn/video_file/sample_480p.mp4/?token=abc" {
		t.Errorf("expected 480p stream, got %q", resLow.Video.StreamURL)
	}
}

func TestEbalovoMockListing(t *testing.T) {
	mockListingHTML := `<!doctype html>
<html>
<head>
  <title>😈 Категория Анал - Ебалово</title>
</head>
<body>
  <h1>Анал видео онлайн</h1>
  <div id="list_videos_common_videos_list_items">
    <div class="item">
      <a href="https://www.ebalovo.porn/video/video-one/" data-id="101">
        <div class="img"><img alt="First Feature Video" src="https://img.ebacdn.com/1.jpg"></div>
      </a>
      <div class="item-info">
        <div class="item-title"> First Feature Video </div>
      </div>
    </div>
    <div class="item">
      <a href="/video/video-two/" data-id="102">
        <div class="img"><img alt="Second Feature Video" src="https://img.ebacdn.com/2.jpg"></div>
      </a>
      <div class="item-info">
        <div class="item-title"> Second Feature Video </div>
      </div>
    </div>
  </div>
</body>
</html>`

	ext := &EbalovoExtractor{}
	targetURL, _ := url.Parse("https://www.ebalovo.porn/porno/anal-videos/")

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
	if res.Playlist.Entries[0].URL != "https://www.ebalovo.porn/video/video-one/" {
		t.Errorf("entry 0 unexpected URL: %s", res.Playlist.Entries[0].URL)
	}
	if res.Playlist.Entries[0].Title != "First Feature Video" {
		t.Errorf("entry 0 unexpected Title: %s", res.Playlist.Entries[0].Title)
	}
	if res.Playlist.Entries[1].URL != "https://www.ebalovo.porn/video/video-two/" {
		t.Errorf("entry 1 unexpected URL: %s", res.Playlist.Entries[1].URL)
	}
	if res.Playlist.Entries[1].Title != "Second Feature Video" {
		t.Errorf("entry 1 unexpected Title: %s", res.Playlist.Entries[1].Title)
	}
}

func TestEbalovoLiveVideo(t *testing.T) {
	client, err := common.NewHTTPClient()
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	targetURL, _ := url.Parse("https://www.ebalovo.porn/video/analnaya-gruppovuha-s-russkoy/")
	ext := Find(targetURL)
	if ext == nil {
		t.Fatal("ebalovo extractor not found for target URL")
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

func TestEbalovoLiveEmbed(t *testing.T) {
	client, err := common.NewHTTPClient()
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	targetURL, _ := url.Parse("https://www.ebalovo.porn/embed/121464/")
	ext := Find(targetURL)
	if ext == nil {
		t.Fatal("ebalovo extractor not found for target URL")
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
