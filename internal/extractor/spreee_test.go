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

func TestSpreeeMatching(t *testing.T) {
	domains := []string{
		"https://spreee.pro/1128446/",
		"https://bbb.spreee.pro/1128446/",
		"https://spree.tube/1128446/",
		"https://xxx.spree.tube/1128446/",
		"https://m.itsmyfetish.com/1128446/",
		"https://embed-player.space/1128446/",
	}

	for _, d := range domains {
		u, err := url.Parse(d)
		if err != nil {
			t.Fatalf("failed to parse %s: %v", d, err)
		}
		ext := Find(u)
		if ext == nil {
			t.Errorf("Find(%q) returned nil", d)
			continue
		}
		if ext.Name() != "spreee" {
			t.Errorf("Find(%q) = %q, want 'spreee'", d, ext.Name())
		}
	}
}

func TestSpreeeMockExtraction(t *testing.T) {
	mockHTML := `<!doctype html>
<html>
<head>
  <title>Amazing Sunset Timelapse — spree</title>
  <meta property="og:title" content="Amazing Sunset Timelapse">
  <meta property="ya:ovs:content_url" content="https://cf-cdn.embed-player.space/12345.mp4">
</head>
<body>
  <video id="player">
    <source src="https://cf-cdn.embed-player.space/12345.mp4?d=spree" type="video/mp4">
  </video>
</body>
</html>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(mockHTML))
	}))
	defer server.Close()

	ext := &SpreeeExtractor{}
	serverURL, _ := url.Parse(server.URL + "/12345/")

	res, err := ext.ExtractFromHTML(context.Background(), server.Client(), serverURL, []byte(mockHTML), common.DownloadOptions{})
	if err != nil {
		t.Fatalf("ExtractFromHTML failed: %v", err)
	}
	if res.Type != ResultTypeVideo || res.Video == nil {
		t.Fatalf("expected video result, got: %+v", res)
	}
	if res.Video.Title != "Amazing Sunset Timelapse" {
		t.Errorf("expected title 'Amazing Sunset Timelapse', got %q", res.Video.Title)
	}
	if res.Video.StreamURL != "https://cf-cdn.embed-player.space/12345.mp4?d=spree" {
		t.Errorf("unexpected stream URL: %q", res.Video.StreamURL)
	}
}

func TestSpreeeLiveExtraction(t *testing.T) {
	client, err := common.NewHTTPClient()
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	// Test with bare spreee.pro to verify automatic rewriting to bbb.spreee.pro
	targetURL, _ := url.Parse("https://spreee.pro/1128446/")
	ext := Find(targetURL)
	if ext == nil {
		t.Fatal("spreee extractor not found for target URL")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	res, err := ext.Extract(ctx, client, targetURL, common.DownloadOptions{})
	if err != nil {
		t.Fatalf("extraction failed: %v", err)
	}
	if res.Type != ResultTypeVideo || res.Video == nil {
		t.Fatalf("expected video result, got %+v", res)
	}
	if res.Video.StreamURL == "" {
		t.Errorf("expected non-empty stream URL")
	}
	t.Logf("Extracted Title: %s", res.Video.Title)
	t.Logf("Extracted Stream: %s", res.Video.StreamURL)
}
