package extractor

import (
	"context"
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
	rePMVTubeSource = regexp.MustCompile(`(?is)<source[^>]+src=["']([^"']+)["'][^>]*>`)
	rePMVTubeTitle  = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	rePMVTubeOGT    = regexp.MustCompile(`(?is)<meta[^>]+property=["']og:title["'][^>]+content=["'](.*?)["']\s*(?:/?>|[a-zA-Z_-]+=)`)
	rePMVTubeSfx    = regexp.MustCompile(`(?i)\s*[-|]\s*PMVtube\.com\s*$`)
	rePMVTubeTagVid = regexp.MustCompile(`https?://pmvtube\.com/([0-9]+-[a-zA-Z0-9_-]+)/?`)
)

type PMVTubeExtractor struct{}

func (p *PMVTubeExtractor) Name() string {
	return "pmvtube"
}

func (p *PMVTubeExtractor) Description() string {
	return "PMVTube video and collection extractor"
}

func (p *PMVTubeExtractor) Capabilities() Capability {
	return CapVideo | CapPlaylist
}

func (p *PMVTubeExtractor) Match(u *url.URL) bool {
	host := strings.ToLower(u.Host)
	return host == "pmvtube.com" || host == "www.pmvtube.com"
}

func (p *PMVTubeExtractor) Extract(ctx context.Context, client *http.Client, targetURL *url.URL, opts common.DownloadOptions) (*Result, error) {
	path := strings.ToLower(targetURL.Path)

	isTagPath := strings.HasPrefix(path, "/tag/") || strings.HasPrefix(path, "/actors/")
	if isTagPath && !opts.NoPlaylist {
		return p.extractCollection(ctx, client, targetURL, opts)
	}

	return p.extractVideo(ctx, client, targetURL, opts)
}

func (p *PMVTubeExtractor) extractVideo(ctx context.Context, client *http.Client, targetURL *url.URL, opts common.DownloadOptions) (*Result, error) {
	pageURL := targetURL.String()
	bodyBytes, finalURL, err := common.FetchBytesWithRetry(ctx, client, pageURL, "", common.MaxHTMLBytes, common.DefaultTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch pmvtube video page: %w", err)
	}
	if finalURL != nil {
		targetURL = finalURL
		pageURL = targetURL.String()
	}

	bodyStr := string(bodyBytes)
	videoTitle := ""

	if m := rePMVTubeOGT.FindStringSubmatch(bodyStr); len(m) > 1 {
		t := html.UnescapeString(m[1])
		t = strings.TrimSpace(rePMVTubeSfx.ReplaceAllString(t, ""))
		if t != "" {
			videoTitle = t
		}
	}
	if videoTitle == "" {
		if m := rePMVTubeTitle.FindStringSubmatch(bodyStr); len(m) > 1 {
			t := html.UnescapeString(m[1])
			t = strings.TrimSpace(rePMVTubeSfx.ReplaceAllString(t, ""))
			if t != "" {
				videoTitle = t
			}
		}
	}
	if videoTitle == "" {
		base := filepath.Base(targetURL.Path)
		videoTitle = base
	}

	// Extract available video sources
	var highURL, lowURL, fallbackURL string
	sources := rePMVTubeSource.FindAllStringSubmatch(bodyStr, -1)
	for _, s := range sources {
		src := html.UnescapeString(strings.TrimSpace(s[1]))
		fullTag := s[0]
		if strings.Contains(strings.ToLower(fullTag), `title="high"`) || strings.Contains(strings.ToLower(src), "_high.mp4") {
			highURL = src
		} else if strings.Contains(strings.ToLower(fullTag), `title="low"`) || strings.Contains(strings.ToLower(src), "_low.mp4") {
			lowURL = src
		} else if fallbackURL == "" {
			fallbackURL = src
		}
	}

	// Select stream based on quality preference
	streamURL := ""
	q := strings.ToLower(strings.TrimSpace(opts.Quality))
	if q == "low" || q == "360" || q == "480" {
		if lowURL != "" {
			streamURL = lowURL
		} else if highURL != "" {
			streamURL = highURL
		}
	} else {
		if highURL != "" {
			streamURL = highURL
		} else if lowURL != "" {
			streamURL = lowURL
		}
	}
	if streamURL == "" {
		streamURL = fallbackURL
	}

	if streamURL == "" {
		return nil, fmt.Errorf("no video stream found on pmvtube page %s", pageURL)
	}

	return &Result{
		Type: ResultTypeVideo,
		Video: &VideoResult{
			Title:     common.SanitizeFilename(videoTitle),
			PageURL:   pageURL,
			StreamURL: streamURL,
			Referer:   "https://pmvtube.com/",
		},
	}, nil
}

func (p *PMVTubeExtractor) extractCollection(ctx context.Context, client *http.Client, targetURL *url.URL, opts common.DownloadOptions) (*Result, error) {
	pageURL := targetURL.String()
	bodyBytes, finalURL, err := common.FetchBytesWithRetry(ctx, client, pageURL, "", common.MaxHTMLBytes, common.DefaultTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch pmvtube collection page: %w", err)
	}
	if finalURL != nil {
		targetURL = finalURL
		pageURL = targetURL.String()
	}

	bodyStr := string(bodyBytes)
	collectionTitle := ""

	if m := rePMVTubeTitle.FindStringSubmatch(bodyStr); len(m) > 1 {
		t := html.UnescapeString(m[1])
		t = strings.TrimSpace(rePMVTubeSfx.ReplaceAllString(t, ""))
		if t != "" {
			collectionTitle = t
		}
	}
	if collectionTitle == "" {
		collectionTitle = filepath.Base(targetURL.Path)
	}

	var entries []PlaylistEntry
	seen := make(map[string]bool)

	for _, m := range rePMVTubeTagVid.FindAllStringSubmatch(bodyStr, -1) {
		slug := m[1]
		if strings.HasPrefix(slug, "tag") || strings.HasPrefix(slug, "actors") || strings.HasPrefix(slug, "page") {
			continue
		}
		vidURL := fmt.Sprintf("https://pmvtube.com/%s/", slug)
		if !seen[vidURL] {
			seen[vidURL] = true
			cleanTitle := slug
			// Strip leading number prefix e.g. "14846-seductive-teen..."
			if dashIdx := strings.Index(slug, "-"); dashIdx != -1 && dashIdx+1 < len(slug) {
				cleanTitle = slug[dashIdx+1:]
			}
			entries = append(entries, PlaylistEntry{
				Type:  ResultTypeVideo,
				Title: cleanTitle,
				URL:   vidURL,
			})
		}
	}

	if len(entries) == 0 {
		return nil, fmt.Errorf("no videos found in pmvtube collection %s", pageURL)
	}

	return &Result{
		Type: ResultTypePlaylist,
		Playlist: &PlaylistResult{
			Title:   common.SanitizeFilename(collectionTitle),
			PageURL: pageURL,
			Entries: entries,
		},
	}, nil
}

func init() {
	Register(&PMVTubeExtractor{})
}
