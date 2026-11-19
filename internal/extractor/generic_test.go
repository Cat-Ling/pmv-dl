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

func TestGenericExtractorLive(t *testing.T) {
	client := &http.Client{Timeout: 15 * time.Second}
	gen := &GenericExtractor{}
	opts := common.DownloadOptions{}

	tests := []struct {
		name          string
		urlStr        string
		expectedTitle string
		streamSubstr  string
	}{
		{
			name:          "PMVHero via Generic",
			urlStr:        "https://pmvhero.com/video/02680150-0991-41c7-9de8-9d7aba085e79",
			expectedTitle: "HENTAI OLYMPICS_HMV",
			streamSubstr:  ".mp4",
		},
		{
			name:          "FapTap via Generic",
			urlStr:        "https://faptap.net/v/2117497152488869889",
			expectedTitle: "Jane's Dough [Elasid]",
			streamSubstr:  ".b-cdn.net/79ea1379-30f6-4a0a-bd25-5cbc8c254ad4/playlist.m3u8",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			u, err := url.Parse(tc.urlStr)
			if err != nil {
				t.Fatalf("failed to parse url: %v", err)
			}
			res, err := gen.Extract(context.Background(), client, u, opts)
			if err != nil {
				t.Fatalf("generic extract failed for %s: %v", tc.urlStr, err)
			}
			if res.Type != ResultTypeVideo || res.Video == nil {
				t.Fatalf("expected video result, got %+v", res)
			}
			if !strings.Contains(res.Video.Title, tc.expectedTitle) {
				t.Errorf("expected title to contain %q, got %q", tc.expectedTitle, res.Video.Title)
			}
			if !strings.Contains(res.Video.StreamURL, tc.streamSubstr) {
				t.Errorf("expected stream URL to contain %q, got %q", tc.streamSubstr, res.Video.StreamURL)
			}
		})
	}
}

func TestGenericPlayerjs(t *testing.T) {
	html := `<!doctype html>
<html>
<head><title>Playerjs Clip</title></head>
<body>
  <script>
    var player = new Playerjs({
      id: "player",
      file: "[720p]https://cdn.example.com/video_720p.mp4,[1080p]https://cdn.example.com/video_1080p.mp4,[360p]https://cdn.example.com/video_360p.mp4"
    });
  </script>
</body>
</html>`

	got := extractPlayerjs(html)
	if got != "https://cdn.example.com/video_1080p.mp4" {
		t.Errorf("extractPlayerjs = %q, want 1080p stream", got)
	}
}

func TestGenericModernKVS(t *testing.T) {
	html := `<!doctype html>
<html>
<head><title>KVS Clip</title></head>
<body>
  <script>
    var t04d08ed058 = {
      video_id: '109058',
      license_code: '$566510018690157',
      video_url: 'https://cdn.example.com/video_480p.mp4',
      video_url_text: '480p',
      postfix: '_480p.mp4',
      video_alt_url: 'https://cdn.example.com/video_720p.mp4',
      video_alt_url_text: '720p',
      timeline_screens_url: 'https://cdn.example.com/timelines/{time}.jpg'
    };
    var player_obj = kt_player('kt_player', '/player/kt_player.swf', '100%', '100%', t04d08ed058);
  </script>
</body>
</html>`

	got := extractKVSPlayer(html)
	if got != "https://cdn.example.com/video_720p.mp4" {
		t.Errorf("extractKVSPlayer = %q, want 720p alt stream", got)
	}
}

func TestGenericTitlePreservesApostrophesInMetaTags(t *testing.T) {
	html := `<!doctype html>
<html>
<head>
  <meta property="og:title" content="Exxxtra Small 4'8 Haley Spades takes on massive 20 inch BBC - xHamster.com">
</head>
<body></body>
</html>`
	u, _ := url.Parse("https://xhamster.com/videos/exxxtra-small-48-haley-spades-takes-on-massive-20-inch-bbc-xh4eBOZ")
	got := (&GenericExtractor{}).extractTitle(html, u)
	want := "Exxxtra Small 4'8 Haley Spades takes on massive 20 inch BBC"
	if got != want {
		t.Errorf("extractTitle = %q, want %q", got, want)
	}
}

func TestGenericPXPFarmMultiQualityLive(t *testing.T) {
	client := &http.Client{Timeout: 15 * time.Second}
	gen := &GenericExtractor{}
	u, _ := url.Parse("https://pxp.farm/videos/15908416856")

	for _, tc := range []struct {
		quality    string
		wantSubstr string
	}{
		{"best", "/1080.mp4"},
		{"720", "/720.mp4"},
		{"360", "/360.mp4"},
		{"worst", "/360.mp4"},
	} {
		res, err := gen.Extract(context.Background(), client, u, common.DownloadOptions{Quality: tc.quality})
		if err != nil {
			t.Fatalf("Extract(%q) failed: %v", tc.quality, err)
		}
		if res.Video.Title != "Nude Photo Workshop" {
			t.Errorf("Title = %q, want %q", res.Video.Title, "Nude Photo Workshop")
		}
		if !strings.Contains(res.Video.StreamURL, tc.wantSubstr) {
			t.Errorf("Quality %q -> StreamURL = %q, want substring %q", tc.quality, res.Video.StreamURL, tc.wantSubstr)
		}
	}
}
