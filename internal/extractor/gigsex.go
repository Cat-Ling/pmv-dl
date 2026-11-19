package extractor

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"pmv-dl/internal/common"
)

var (
	reGigSexFlash   = regexp.MustCompile(`(?s)(?:var\s+)?flashvars\s*=\s*\{([^}]+)\}`)
	reGigSexProp    = regexp.MustCompile(`(?i)["']?([a-zA-Z0-9_]+)["']?\s*:\s*['"]([^'"]*)['"]`)
	reGigSexTitle   = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	reGigSexOGTitle = regexp.MustCompile(`(?is)<meta[^>]+property=["']og:title["'][^>]+content=["'](.*?)["']\s*(?:/?>|[a-zA-Z_-]+=)`)
	reGigSexSfx     = regexp.MustCompile(`(?i)\s*[-|]\s*GiG\.SEX\s*$`)
	reGigSexWatch   = regexp.MustCompile(`(?i)\s*(?:watch\s+online|playlist\s+watch\s+online)\s*$`)
	reGigSexVidLink = regexp.MustCompile(`https?://(?:www\.)?gig\.sex/video/([a-zA-Z0-9_-]+)/?`)
)

type GigSexExtractor struct{}

func (g *GigSexExtractor) Name() string {
	return "gigsex"
}

func (g *GigSexExtractor) Description() string {
	return "GiG.SEX video and playlist extractor"
}

func (g *GigSexExtractor) Capabilities() Capability {
	return CapVideo | CapPlaylist
}

func (g *GigSexExtractor) Match(u *url.URL) bool {
	host := strings.ToLower(u.Host)
	return host == "gig.sex" || host == "www.gig.sex" || strings.HasSuffix(host, ".gig.sex")
}

func (g *GigSexExtractor) Extract(ctx context.Context, client *http.Client, targetURL *url.URL, opts common.DownloadOptions) (*Result, error) {
	path := strings.ToLower(targetURL.Path)

	isPlaylistPath := strings.HasPrefix(path, "/playlists/") || strings.HasPrefix(path, "/playlist/")
	if isPlaylistPath && !opts.NoPlaylist {
		return g.extractPlaylist(ctx, client, targetURL, opts)
	}

	return g.extractVideo(ctx, client, targetURL, opts)
}

type gigSexCandidate struct {
	height int
	url    string
}

func (g *GigSexExtractor) extractVideo(ctx context.Context, client *http.Client, targetURL *url.URL, opts common.DownloadOptions) (*Result, error) {
	pageURL := targetURL.String()
	bodyBytes, finalURL, err := common.FetchBytesWithRetry(ctx, client, pageURL, "", common.MaxHTMLBytes, common.DefaultTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch gig.sex video page: %w", err)
	}
	if finalURL != nil {
		targetURL = finalURL
		pageURL = targetURL.String()
	}

	bodyStr := string(bodyBytes)

	// Parse flashvars
	flashMatch := reGigSexFlash.FindStringSubmatch(bodyStr)
	if len(flashMatch) < 2 {
		return nil, fmt.Errorf("failed to locate flashvars on gig.sex page %s", pageURL)
	}
	flashBlock := flashMatch[1]

	props := make(map[string]string)
	for _, m := range reGigSexProp.FindAllStringSubmatch(flashBlock, -1) {
		props[strings.ToLower(m[1])] = m[2]
	}

	licenseCode := props["license_code"]
	videoTitle := props["video_title"]

	if videoTitle == "" {
		if m := reGigSexOGTitle.FindStringSubmatch(bodyStr); len(m) > 1 {
			t := html.UnescapeString(m[1])
			t = strings.TrimSpace(reGigSexSfx.ReplaceAllString(t, ""))
			t = strings.TrimSpace(reGigSexWatch.ReplaceAllString(t, ""))
			if t != "" {
				videoTitle = t
			}
		}
	}
	if videoTitle == "" {
		if m := reGigSexTitle.FindStringSubmatch(bodyStr); len(m) > 1 {
			t := html.UnescapeString(m[1])
			t = strings.TrimSpace(reGigSexSfx.ReplaceAllString(t, ""))
			t = strings.TrimSpace(reGigSexWatch.ReplaceAllString(t, ""))
			if t != "" {
				videoTitle = t
			}
		}
	}
	if videoTitle == "" {
		videoTitle = filepath.Base(targetURL.Path)
	}

	var candidates []gigSexCandidate

	urlKeys := []string{
		"video_url",
		"video_alt_url",
		"video_alt_url1",
		"video_alt_url2",
		"video_alt_url3",
		"video_alt_url4",
	}

	for _, key := range urlKeys {
		rawURL, ok := props[key]
		if !ok || strings.TrimSpace(rawURL) == "" {
			continue
		}
		realURL := rawURL
		if strings.HasPrefix(realURL, "function/0/") && licenseCode != "" {
			realURL = kvsGetRealURL(realURL, licenseCode)
		}

		textLabel := props[key+"_text"]
		h := parseHeightFromLabel(textLabel)
		if h == 0 {
			h = parseHeightFromLabel(realURL)
		}
		candidates = append(candidates, gigSexCandidate{
			height: h,
			url:    realURL,
		})
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("no playable formats found in gig.sex flashvars for %s", pageURL)
	}

	// Select best matching quality
	streamURL := selectGigSexQuality(candidates, opts.Quality)

	return &Result{
		Type: ResultTypeVideo,
		Video: &VideoResult{
			Title:     common.SanitizeFilename(videoTitle),
			PageURL:   pageURL,
			StreamURL: streamURL,
			Referer:   pageURL,
		},
	}, nil
}

func parseHeightFromLabel(s string) int {
	s = strings.ToLower(s)
	reNum := regexp.MustCompile(`(\d{3,4})p?`)
	if m := reNum.FindStringSubmatch(s); len(m) > 1 {
		if v, err := strconv.Atoi(m[1]); err == nil {
			return v
		}
	}
	return 0
}

func selectGigSexQuality(candidates []gigSexCandidate, targetQuality string) string {
	q := strings.ToLower(strings.TrimSpace(targetQuality))

	if q == "low" {
		minH := 99999
		bestURL := candidates[0].url
		for _, c := range candidates {
			if c.height > 0 && c.height < minH {
				minH = c.height
				bestURL = c.url
			}
		}
		return bestURL
	}

	if q == "best" || q == "" {
		maxH := 0
		bestURL := candidates[0].url
		for _, c := range candidates {
			if c.height > maxH {
				maxH = c.height
				bestURL = c.url
			}
		}
		return bestURL
	}

	targetH, _ := strconv.Atoi(q)
	if targetH == 0 {
		targetH = 1080
	}

	// Exact match
	for _, c := range candidates {
		if c.height == targetH {
			return c.url
		}
	}

	// Closest <= target
	bestDiff := 99999
	bestURL := candidates[0].url
	for _, c := range candidates {
		diff := targetH - c.height
		if diff >= 0 && diff < bestDiff {
			bestDiff = diff
			bestURL = c.url
		}
	}
	return bestURL
}

func (g *GigSexExtractor) extractPlaylist(ctx context.Context, client *http.Client, targetURL *url.URL, opts common.DownloadOptions) (*Result, error) {
	pageURL := targetURL.String()
	bodyBytes, finalURL, err := common.FetchBytesWithRetry(ctx, client, pageURL, "", common.MaxHTMLBytes, common.DefaultTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch gig.sex playlist page: %w", err)
	}
	if finalURL != nil {
		targetURL = finalURL
		pageURL = targetURL.String()
	}

	bodyStr := string(bodyBytes)
	playlistTitle := ""

	if m := reGigSexTitle.FindStringSubmatch(bodyStr); len(m) > 1 {
		t := html.UnescapeString(m[1])
		t = strings.TrimSpace(reGigSexSfx.ReplaceAllString(t, ""))
		t = strings.TrimSpace(reGigSexWatch.ReplaceAllString(t, ""))
		if t != "" {
			playlistTitle = t
		}
	}
	if playlistTitle == "" {
		playlistTitle = filepath.Base(targetURL.Path)
	}

	var entries []PlaylistEntry
	seen := make(map[string]bool)

	for _, m := range reGigSexVidLink.FindAllStringSubmatch(bodyStr, -1) {
		slug := m[1]
		vidURL := fmt.Sprintf("https://www.gig.sex/video/%s/", slug)
		if !seen[vidURL] {
			seen[vidURL] = true
			entries = append(entries, PlaylistEntry{
				Type:  ResultTypeVideo,
				Title: slug,
				URL:   vidURL,
			})
		}
	}

	if len(entries) == 0 {
		return nil, fmt.Errorf("no videos found in gig.sex playlist %s", pageURL)
	}

	return &Result{
		Type: ResultTypePlaylist,
		Playlist: &PlaylistResult{
			Title:   common.SanitizeFilename(playlistTitle),
			PageURL: pageURL,
			Entries: entries,
		},
	}, nil
}

func init() {
	Register(&GigSexExtractor{})
}
