package extractor

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"

	"pmv-dl/internal/common"
)

func TestProstoPornoMatching(t *testing.T) {
	urls := []string{
		"https://prostoporno.dev/videos/377927/",
		"https://prostoporno.dev/videos/377927",
		"https://prostoporno.dev/embed/377927/",
		"https://ppembed.com/embed/377927/",
		"https://ppembed.com/embed/377927",
		"https://www.prostoporno.dev/videos/123/",
		"https://prostoporno.dev/albums/dvoyurodnaya-sestra/",
		"https://prostoporno.dev/main-albums/",
		"https://prostoporno.dev/categories/брюнетки/",
		"https://prostoporno.dev/models/amira-mae/",
		"https://prostoporno.dev/tag/138/",
		"https://prostoporno.dev/best/",
		"https://prostoporno.dev/newest/",
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
		if ext.Name() != "prostoporno" {
			t.Errorf("Find(%q) = %q, want 'prostoporno'", uStr, ext.Name())
		}
	}
}

func TestProstoPornoVideoMock(t *testing.T) {
	mockHTML := `<!doctype html>
<html>
<head>
  <title>"Порно онлайн - озорная медсестра - Prostoporno"</title>
  <meta property="og:title" content="Порно онлайн - озорная медсестра - Prostoporno" />
</head>
<body>
  <h1 itemprop="name">озорная медсестра</h1>
  <script type="text/javascript">
    var flashvars = {
      video_id: '377927',
      video_url: 'https://prostoporno.dev/get_file/7/video_h.mp4/',
      video_url_text: 'HQ',
      video_url_hd: '1',
      video_alt_url: 'https://prostoporno.dev/get_file/7/video.mp4/',
      video_alt_url_text: 'LQ'
    };
  </script>
</body>
</html>`

	ext := &ProstoPornoExtractor{}
	targetURL, _ := url.Parse("https://prostoporno.dev/videos/377927/")

	// Default (HQ)
	res, err := ext.ExtractFromHTML(context.Background(), http.DefaultClient, targetURL, []byte(mockHTML), common.DownloadOptions{})
	if err != nil {
		t.Fatalf("ExtractFromHTML failed: %v", err)
	}
	if res.Type != ResultTypeVideo || res.Video == nil {
		t.Fatalf("expected video result, got %+v", res)
	}
	if res.Video.Title != "озорная медсестра" {
		t.Errorf("expected title 'озорная медсестра', got %q", res.Video.Title)
	}
	if res.Video.StreamURL != "https://prostoporno.dev/get_file/7/video_h.mp4/" {
		t.Errorf("expected HQ stream URL, got %q", res.Video.StreamURL)
	}

	// Low quality
	resLow, err := ext.ExtractFromHTML(context.Background(), http.DefaultClient, targetURL, []byte(mockHTML), common.DownloadOptions{Quality: "low"})
	if err != nil {
		t.Fatalf("ExtractFromHTML low failed: %v", err)
	}
	if resLow.Video.StreamURL != "https://prostoporno.dev/get_file/7/video.mp4/" {
		t.Errorf("expected LQ stream URL, got %q", resLow.Video.StreamURL)
	}
}

func TestProstoPornoAlbumMock(t *testing.T) {
	mockHTML := `<!doctype html>
<html>
<head>
  <title>"Порно фото - Двоюродная сестра - альбом от пользователя Оксана83"</title>
  <meta property="og:title" content="Порно фото - Двоюродная сестра - альбом от пользователя Оксана83" />
</head>
<body>
  <div class="full">
    <a href="https://i.prostoporno.dev/contents/albums/main/1024x768/65000/65605/879546.jpg" class="fullimg"></a>
    <a href="https://i.prostoporno.dev/contents/albums/main/1024x768/65000/65605/879547.jpg" class="fullimg"></a>
    <a href="https://i.prostoporno.dev/contents/albums/main/1024x768/65000/65605/879548.jpg" class="fullimg"></a>
  </div>
</body>
</html>`

	ext := &ProstoPornoExtractor{}
	targetURL, _ := url.Parse("https://prostoporno.dev/albums/dvoyurodnaya-sestra/")

	res, err := ext.ExtractFromHTML(context.Background(), http.DefaultClient, targetURL, []byte(mockHTML), common.DownloadOptions{})
	if err != nil {
		t.Fatalf("ExtractFromHTML failed: %v", err)
	}
	if res.Type != ResultTypeImage || res.Image == nil {
		t.Fatalf("expected image result, got %+v", res)
	}
	if res.Image.Title != "Двоюродная сестра" {
		t.Errorf("expected title 'Двоюродная сестра', got %q", res.Image.Title)
	}
	if len(res.Image.ImageURLs) != 3 {
		t.Fatalf("expected 3 image URLs, got %d", len(res.Image.ImageURLs))
	}
	if res.Image.ImageURLs[0] != "https://i.prostoporno.dev/contents/albums/main/1024x768/65000/65605/879546.jpg" {
		t.Errorf("unexpected first image URL: %q", res.Image.ImageURLs[0])
	}
}

func TestProstoPornoVideoListingMock(t *testing.T) {
	mockHTML := `<!doctype html>
<html>
<head>
  <title>"Порно Брюнетки смотреть онлайн бесплатно в хорошем качестве на Prostoporno"</title>
</head>
<body>
  <h1>Порно с брюнетками</h1>
  <div class="thumb">
    <a href="https://prostoporno.dev/videos/377927/" title="озорная медсестра">озорная медсестра</a>
  </div>
  <div class="thumb">
    <a href="https://prostoporno.dev/videos/376455/" title="Их разный секс">Их разный секс</a>
  </div>
</body>
</html>`

	ext := &ProstoPornoExtractor{}
	targetURL, _ := url.Parse("https://prostoporno.dev/categories/брюнетки/")

	res, err := ext.ExtractFromHTML(context.Background(), http.DefaultClient, targetURL, []byte(mockHTML), common.DownloadOptions{})
	if err != nil {
		t.Fatalf("extract failed: %v", err)
	}
	if res.Type != ResultTypePlaylist || res.Playlist == nil {
		t.Fatalf("expected playlist result, got %+v", res)
	}
	if res.Playlist.Title != "Порно с брюнетками" {
		t.Errorf("expected playlist title 'Порно с брюнетками', got %q", res.Playlist.Title)
	}
	if len(res.Playlist.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(res.Playlist.Entries))
	}
	if res.Playlist.Entries[0].URL != "https://prostoporno.dev/videos/377927/" {
		t.Errorf("unexpected entry 0 URL: %q", res.Playlist.Entries[0].URL)
	}
	if res.Playlist.Entries[0].Title != "озорная медсестра" {
		t.Errorf("unexpected entry 0 title: %q", res.Playlist.Entries[0].Title)
	}
}

func TestProstoPornoLiveVideo(t *testing.T) {
	client, err := common.NewHTTPClient()
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	targetURL, _ := url.Parse("https://prostoporno.dev/videos/377927/")
	ext := Find(targetURL)
	if ext == nil {
		t.Fatal("prostoporno extractor not found")
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
		t.Error("video title is empty")
	}
}

func TestProstoPornoLiveAlbum(t *testing.T) {
	client, err := common.NewHTTPClient()
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	targetURL, _ := url.Parse("https://prostoporno.dev/albums/dvoyurodnaya-sestra/")
	ext := Find(targetURL)
	if ext == nil {
		t.Fatal("prostoporno extractor not found")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	res, err := ext.Extract(ctx, client, targetURL, common.DownloadOptions{})
	if err != nil {
		t.Fatalf("live album extraction failed: %v", err)
	}
	if res.Type != ResultTypeImage || res.Image == nil {
		t.Fatalf("expected image result, got %+v", res)
	}
	if len(res.Image.ImageURLs) == 0 {
		t.Error("image URLs list is empty")
	}
	if res.Image.Title == "" {
		t.Error("album title is empty")
	}
}
