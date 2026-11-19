package extractor

import (
	"net/url"
	"testing"
)

func TestExtractorMatching(t *testing.T) {
	tests := []struct {
		urlStr       string
		expectedName string
	}{
		{"https://pmvhaven.com/video/three-qos_6a690d5bc7a08eac7becb863", "pmvhaven"},
		{"https://www.pmvhaven.com/playlists/6aeaea5695401eeb81775573", "pmvhaven"},
		{"https://pmvclub.com/gallery/all-teenyboppers-rbc1to3w", "pmvclub"},
		{"https://goonchan.org/video/sample-video", "pmvclub"},
		{"https://goonchan.org/image/flat-tits-trigger-you-5teyvjea", "pmvclub"},
		{"https://pmvhero.com/video/sample-id", "pmvhero"},
		{"https://pmvhero.com/playlists/sample-id", "pmvhero"},
		{"https://faptap.net/v/sample-id", "faptap"},
		{"https://faptap.net/playlist/sample-id", "faptap"},
		{"https://pmvtube.com/14846-seductive-teen-whore-music-video/", "pmvtube"},
		{"https://pmvtube.com/tag/teen/", "pmvtube"},
		{"https://www.gig.sex/video/sample-slug-12345/", "gigsex"},
		{"https://gig.sex/playlists/10/my-playlist/", "gigsex"},
		{"https://noodlemagazine.com/watch/-152022671_456240558", "noodlemagazine"},
		{"https://adult.noodlemagazine.com/watch/-152022671_456240558", "noodlemagazine"},
		{"https://mat6tube.com/watch/-152022671_456240558", "noodlemagazine"},
		{"https://exporntoons.net/watch/-152022671_456240558", "noodlemagazine"},
		{"https://ukdevilz.com/watch/-152022671_456240558", "noodlemagazine"},
		{"https://tyler-brown.com/watch/-152022671_456240558", "noodlemagazine"},
		{"https://pornjav.tv/watch/-152022671_456240558", "noodlemagazine"},
		{"https://noodle.yemoja.xyz/watch/-152022671_456240558", "noodlemagazine"},
		{"https://spreee.pro/1128446/", "spreee"},
		{"https://bbb.spreee.pro/1128446/", "spreee"},
		{"https://xxx.spree.tube/1128446/", "spreee"},
		{"https://example.com/stream/index.m3u8", "generic"},
		{"https://unknown-site.org/some/page", "generic"},
	}

	for _, tc := range tests {
		u, err := url.Parse(tc.urlStr)
		if err != nil {
			t.Fatalf("failed to parse test url %q: %v", tc.urlStr, err)
		}
		ext := Find(u)
		if ext == nil {
			t.Fatalf("no extractor matched %q", tc.urlStr)
		}
		if ext.Name() != tc.expectedName {
			t.Errorf("Find(%q) = %q, want %q", tc.urlStr, ext.Name(), tc.expectedName)
		}
	}
}
