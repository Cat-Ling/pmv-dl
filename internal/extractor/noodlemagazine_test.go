package extractor

import (
	"context"
	"net/url"
	"testing"
	"time"

	"pmv-dl/internal/common"
)

func TestNoodleNetworkMatching(t *testing.T) {
	domains := []string{
		"https://noodlemagazine.com/watch/-152022671_456240558",
		"https://adult.noodlemagazine.com/watch/-152022671_456240558",
		"https://mat6tube.com/watch/-152022671_456240558",
		"https://exporntoons.net/watch/-152022671_456240558",
		"https://ukdevilz.com/watch/-152022671_456240558",
		"https://tyler-brown.com/watch/-152022671_456240558",
		"https://pornjav.tv/watch/-152022671_456240558",
		"https://noodle.yemoja.xyz/watch/-152022671_456240558",
	}

	for _, d := range domains {
		u, err := url.Parse(d)
		if err != nil {
			t.Fatalf("failed to parse url %s: %v", d, err)
		}
		ext := Find(u)
		if ext == nil {
			t.Errorf("Find(%q) returned nil", d)
			continue
		}
		if ext.Name() != "noodlemagazine" {
			t.Errorf("Find(%q) = %q, want 'noodlemagazine'", d, ext.Name())
		}
	}
}

func TestNoodleTitleSanitization(t *testing.T) {
	ext := &NoodleMagazineExtractor{}
	targetURL, _ := url.Parse("https://mat6tube.com/watch/-152022671_456240558")

	tests := []struct {
		name     string
		html     string
		expected string
	}{
		{
			name:     "Strip BEST XXX TUBE suffix",
			html:     `<title>Epic Dance Performance - BEST XXX TUBE</title>`,
			expected: "Epic Dance Performance",
		},
		{
			name:     "Strip NoodleMagazine suffix and unescape entities",
			html:     `<meta property="og:title" content="Rock &amp; Roll Concert | Live - Noodle Magazine">`,
			expected: "Rock & Roll Concert - Live",
		},
		{
			name:     "Strip BrownPorn site branding suffix without delimiter",
			html:     `<title data-video-title-base="Acoustic Session Live porn video on BrownPorn">Acoustic Session Live</title>`,
			expected: "Acoustic Session Live",
		},
		{
			name:     "Data-video-title-base takes precedence",
			html:     `<title data-video-title-base="Original Studio Title - BEST XXX TUBE">Translated - BEST XXX TUBE</title>`,
			expected: "Original Studio Title",
		},
		{
			name:     "URL in title stripped to last segment",
			html:     `<title>https://cdn.example.com/videos/concert_highlight.mp4</title>`,
			expected: "concert_highlight.mp4",
		},
		{
			name:     "Path slashes in title stripped",
			html:     `<title>/var/data/videos/featured_track.mp4</title>`,
			expected: "featured_track.mp4",
		},
		{
			name:     "Fallback to ID when title empty",
			html:     `<title>   </title>`,
			expected: "mat6tube_-152022671_456240558",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ext.extractVideoTitle(tc.html, targetURL)
			if got != tc.expected {
				t.Errorf("extractVideoTitle() = %q, want %q", got, tc.expected)
			}
		})
	}
}

func TestNoodleQualitySelection(t *testing.T) {
	sources := []NoodleSource{
		{File: "https://cdn.example.com/240p.mp4", Label: "240p", Type: "mp4"},
		{File: "https://cdn.example.com/720p.mp4", Label: "720p", Type: "mp4"},
		{File: "https://cdn.example.com/480p.mp4", Label: "480p", Type: "mp4"},
	}

	// 1. Best -> 720p
	best := selectNoodleSource(sources, "best")
	if best.Label != "720p" {
		t.Errorf("best: got %s, want 720p", best.Label)
	}

	// 2. Low -> 240p
	low := selectNoodleSource(sources, "low")
	if low.Label != "240p" {
		t.Errorf("low: got %s, want 240p", low.Label)
	}

	// 3. Exact 480 -> 480p
	q480 := selectNoodleSource(sources, "480")
	if q480.Label != "480p" {
		t.Errorf("480: got %s, want 480p", q480.Label)
	}

	// 4. Request 1080 -> closest <= 1080 -> 720p
	q1080 := selectNoodleSource(sources, "1080")
	if q1080.Label != "720p" {
		t.Errorf("1080: got %s, want 720p", q1080.Label)
	}
}

func TestDynamicProxyDetection(t *testing.T) {
	htmlSample := `<!DOCTYPE html><html><head><script>
window.playlist = {"sources": [{"file": "https://cdn.pvvstream.pro/videos/sample.mp4", "label": "720p", "type": "mp4"}]};
</script></head><body><h1>Unknown Mirror</h1></body></html>`

	if !IsNoodleNetworkContent(htmlSample) {
		t.Errorf("expected IsNoodleNetworkContent to return true for unknown mirror with pvvstream.pro")
	}

	unrelatedHTML := `<!DOCTYPE html><html><head><title>Unrelated</title></head><body></body></html>`
	if IsNoodleNetworkContent(unrelatedHTML) {
		t.Errorf("expected IsNoodleNetworkContent to return false for unrelated HTML")
	}
}

func TestMat6tubeLiveExtraction(t *testing.T) {
	client, err := common.NewHTTPClient()
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	targetURL, _ := url.Parse("https://mat6tube.com/watch/-152022671_456240558")
	ext := Find(targetURL)
	if ext == nil {
		t.Skip("noodlemagazine extractor not yet registered")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	res, err := ext.Extract(ctx, client, targetURL, common.DownloadOptions{Quality: "best"})
	if err != nil {
		t.Fatalf("extract failed: %v", err)
	}
	if res.Type != ResultTypeVideo || res.Video == nil {
		t.Fatalf("expected video result, got: %+v", res)
	}
	if res.Video.StreamURL == "" {
		t.Errorf("expected stream URL, got empty")
	}
	t.Logf("Extracted Title: %s", res.Video.Title)
	t.Logf("Extracted Stream: %s", res.Video.StreamURL)
}
