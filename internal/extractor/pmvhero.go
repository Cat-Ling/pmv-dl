package extractor

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"pmv-dl/internal/common"
)

var (
	rePMVHeroMasterM3u8  = regexp.MustCompile(`https?://[^"'\s\\<>]*master\.m3u8(?:\?[^"'\s\\<>]*)?`)
	rePMVHeroJSONLD      = regexp.MustCompile(`(?is)<script[^>]*type=["']application/ld\+json["'][^>]*>(.*?)</script>`)
	rePMVHeroTitle       = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	rePMVHeroH1          = regexp.MustCompile(`(?is)<h1[^>]*>(.*?)</h1>`)
	rePMVHeroSiteSuffix  = regexp.MustCompile(`(?i)\s*[-|]\s*PMV\s*Hero\s*$`)
	rePMVHeroHTMLTags    = regexp.MustCompile(`<[^>]*>`)
	rePMVHeroVideoLink   = regexp.MustCompile(`/video/([0-9a-fA-F-]+)`)
	rePMVHeroContentURL  = regexp.MustCompile(`"contentUrl"\s*:\s*"([^"]+)"`)
	rePMVHeroName        = regexp.MustCompile(`"name"\s*:\s*"([^"]+)"`)
)

type PMVHeroExtractor struct{}

func (p *PMVHeroExtractor) Name() string {
	return "pmvhero"
}

func (p *PMVHeroExtractor) Description() string {
	return "PMV Hero video and playlist extractor"
}

func (p *PMVHeroExtractor) Capabilities() Capability {
	return CapVideo | CapPlaylist
}

func (p *PMVHeroExtractor) Match(u *url.URL) bool {
	host := strings.ToLower(u.Host)
	return host == "pmvhero.com" || host == "www.pmvhero.com"
}

func (p *PMVHeroExtractor) Extract(ctx context.Context, client *http.Client, targetURL *url.URL, opts common.DownloadOptions) (*Result, error) {
	path := strings.ToLower(targetURL.Path)
	urlStr := targetURL.String()

	isPlaylistPath := strings.HasPrefix(path, "/playlists/") || strings.HasPrefix(path, "/playlist/")
	if isPlaylistPath && !opts.NoPlaylist {
		return p.extractPlaylist(ctx, client, targetURL, opts)
	}

	return p.extractVideo(ctx, client, targetURL, urlStr)
}

func (p *PMVHeroExtractor) extractVideo(ctx context.Context, client *http.Client, targetURL *url.URL, pageURL string) (*Result, error) {
	bodyBytes, finalURL, err := common.FetchBytesWithRetry(ctx, client, targetURL.String(), "", common.MaxHTMLBytes, common.DefaultTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch pmvhero video page: %w", err)
	}
	if finalURL != nil {
		targetURL = finalURL
	}

	bodyStr := string(bodyBytes)
	normalized := common.NormalizeString(bodyStr)

	videoTitle := ""
	streamURL := ""

	// 1. Check for JSON-LD VideoObject
	for _, m := range rePMVHeroJSONLD.FindAllStringSubmatch(bodyStr, -1) {
		jsonText := strings.TrimSpace(m[1])
		if !strings.Contains(jsonText, "VideoObject") {
			continue
		}
		var parsed map[string]interface{}
		if err := json.Unmarshal([]byte(jsonText), &parsed); err == nil {
			if name, ok := parsed["name"].(string); ok && strings.TrimSpace(name) != "" {
				videoTitle = strings.TrimSpace(name)
			}
			if cURL, ok := parsed["contentUrl"].(string); ok && strings.TrimSpace(cURL) != "" {
				streamURL = strings.TrimSpace(cURL)
			} else if eURL, ok := parsed["embedUrl"].(string); ok && strings.TrimSpace(eURL) != "" {
				streamURL = strings.TrimSpace(eURL)
			}
		}
		if videoTitle == "" {
			if nm := rePMVHeroName.FindStringSubmatch(jsonText); len(nm) > 1 {
				videoTitle = nm[1]
			}
		}
		if streamURL == "" {
			if cm := rePMVHeroContentURL.FindStringSubmatch(jsonText); len(cm) > 1 {
				streamURL = cm[1]
			}
		}
		if streamURL != "" {
			break
		}
	}

	// 2. Check if page contains an HLS master playlist URL (preferred for multi-resolution)
	if m := rePMVHeroMasterM3u8.FindString(normalized); m != "" {
		streamURL = m
	}

	if streamURL == "" {
		return nil, fmt.Errorf("no stream URL found on %s", targetURL.String())
	}

	// Resolve relative stream URLs if needed
	resolvedStreamURL, err := common.ResolveURL(targetURL, streamURL)
	if err == nil {
		streamURL = resolvedStreamURL.String()
	}

	// Extract title fallback
	if videoTitle == "" {
		if m := rePMVHeroTitle.FindStringSubmatch(bodyStr); len(m) > 1 {
			t := html.UnescapeString(m[1])
			t = strings.TrimSpace(rePMVHeroSiteSuffix.ReplaceAllString(t, ""))
			if t != "" {
				videoTitle = t
			}
		}
	}
	if videoTitle == "" {
		for _, m := range rePMVHeroH1.FindAllStringSubmatch(bodyStr, -1) {
			t := html.UnescapeString(strings.TrimSpace(rePMVHeroHTMLTags.ReplaceAllString(m[1], "")))
			if t != "" && !strings.Contains(strings.ToLower(t), "age verification") {
				videoTitle = t
				break
			}
		}
	}
	if videoTitle == "" {
		base := filepath.Base(targetURL.Path)
		videoTitle = base
	}

	return &Result{
		Type: ResultTypeVideo,
		Video: &VideoResult{
			Title:     common.SanitizeFilename(videoTitle),
			PageURL:   pageURL,
			StreamURL: streamURL,
			Referer:   targetURL.String(),
		},
	}, nil
}

func (p *PMVHeroExtractor) extractPlaylist(ctx context.Context, client *http.Client, targetURL *url.URL, opts common.DownloadOptions) (*Result, error) {
	bodyBytes, finalURL, err := common.FetchBytesWithRetry(ctx, client, targetURL.String(), "", common.MaxHTMLBytes, common.DefaultTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch pmvhero playlist page: %w", err)
	}
	if finalURL != nil {
		targetURL = finalURL
	}

	bodyStr := string(bodyBytes)
	playlistTitle := ""

	if m := rePMVHeroTitle.FindStringSubmatch(bodyStr); len(m) > 1 {
		t := html.UnescapeString(m[1])
		t = strings.TrimSpace(rePMVHeroSiteSuffix.ReplaceAllString(t, ""))
		if t != "" && !strings.EqualFold(t, "playlists") {
			playlistTitle = t
		}
	}
	if playlistTitle == "" {
		for _, m := range rePMVHeroH1.FindAllStringSubmatch(bodyStr, -1) {
			t := html.UnescapeString(strings.TrimSpace(rePMVHeroHTMLTags.ReplaceAllString(m[1], "")))
			if t != "" && !strings.Contains(strings.ToLower(t), "age verification") {
				playlistTitle = t
				break
			}
		}
	}
	if playlistTitle == "" {
		playlistTitle = "PMVHero Playlist"
	}

	var entries []PlaylistEntry
	seen := make(map[string]bool)

	for _, m := range rePMVHeroVideoLink.FindAllStringSubmatch(bodyStr, -1) {
		id := m[1]
		videoURL := fmt.Sprintf("https://pmvhero.com/video/%s", id)
		if !seen[videoURL] {
			seen[videoURL] = true
			entries = append(entries, PlaylistEntry{
				Type:  ResultTypeVideo,
				Title: id,
				URL:   videoURL,
			})
		}
	}

	if len(entries) == 0 {
		return nil, fmt.Errorf("no videos found in playlist %s", targetURL.String())
	}

	return &Result{
		Type: ResultTypePlaylist,
		Playlist: &PlaylistResult{
			Title:   common.SanitizeFilename(playlistTitle),
			PageURL: targetURL.String(),
			Entries: entries,
		},
	}, nil
}

func init() {
	Register(&PMVHeroExtractor{})
}
