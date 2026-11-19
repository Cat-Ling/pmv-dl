package extractor

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"pmv-dl/internal/common"
)

func TestYandexLivePreviews(t *testing.T) {
	client := &http.Client{Timeout: 20 * time.Second}
	y := &YandexExtractor{}
	opts := common.DownloadOptions{Quality: "best"}

	tests := []struct {
		name        string
		rawURL      string
		wantTitle   string
		wantStream  string
	}{
		{
			name:       "Yandex Turkey Subdomain Preview (NoodleMagazine / nmcorp embed)",
			rawURL:     "https://twitter.yandex.com.tr/video/preview/5316966251679300048",
			wantTitle:  "2017 07 25 ariel and mike nude photo workshop by petter hegre",
			wantStream: ".mp4",
		},
		{
			name:       "Yandex Com Preview (VK Video BFF embed)",
			rawURL:     "https://yandex.com/video/preview/1199140968876831029",
			wantTitle:  "2023-01-17 Any Moloko and Goro - Masturbation Mania",
			wantStream: "okcdn.ru",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			u, err := url.Parse(tc.rawURL)
			if err != nil {
				t.Fatalf("url.Parse failed: %v", err)
			}
			if !y.Match(u) {
				t.Fatalf("expected YandexExtractor.Match(%q) to be true", tc.rawURL)
			}
			res, err := y.Extract(context.Background(), client, u, opts)
			if err != nil {
				t.Fatalf("Extract(%q) failed: %v", tc.rawURL, err)
			}
			if res == nil || res.Video == nil {
				t.Fatalf("expected VideoResult, got %+v", res)
			}
			if !strings.Contains(res.Video.Title, tc.wantTitle) {
				t.Errorf("Title = %q, want substring %q", res.Video.Title, tc.wantTitle)
			}
			if !strings.Contains(res.Video.StreamURL, tc.wantStream) {
				t.Errorf("StreamURL = %q, want substring %q", res.Video.StreamURL, tc.wantStream)
			}
			t.Logf("Extracted Title: %q -> StreamURL: %s", res.Video.Title, res.Video.StreamURL)
		})
	}
}
