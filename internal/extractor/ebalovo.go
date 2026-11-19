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
	reEbalovoProp         = regexp.MustCompile(`(?i)["']?([a-zA-Z0-9_]+)["']?\s*:\s*['"]([^'"]*)['"]`)
	reEbalovoScript       = regexp.MustCompile(`(?is)<script[^>]*>(.*?)</script>`)
	reEbalovoH1           = regexp.MustCompile(`(?is)<h1[^>]*>(.*?)</h1>`)
	reEbalovoTitleTag     = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	reEbalovoOGTitle      = regexp.MustCompile(`(?is)<meta[^>]+property=["']og:title["'][^>]+content=["'](.*?)["']\s*(?:/?>|[a-zA-Z_-]+=)`)
	reEbalovoMetaDesc     = regexp.MustCompile(`(?is)<meta[^>]+name=["']description["'][^>]+content=["'](.*?)["']\s*(?:/?>|[a-zA-Z_-]+=)`)
	reEbalovoVideoCard    = regexp.MustCompile(`(?is)<a[^>]+href=["']((?:https?://[^"'\s>]+)?/video/[^"'\s>]+)["'][^>]*>([\s\S]*?)</a>(?:\s*<div[^>]*class=["'][^"']*item-info[^"']*["'][^>]*>[\s\S]*?<div[^>]*class=["'][^"']*item-title[^"']*["'][^>]*>([\s\S]*?)</div>)?`)
	reEbalovoAltImg       = regexp.MustCompile(`(?is)alt=["']([^"']+)["']`)
	reEbalovoVideoLink    = regexp.MustCompile(`(?is)href=["']((?:https?://(?:www\.)?ebalovo\.(?:porn|click))?/video/[a-zA-Z0-9_-]+/?)[#"'?]`)
	reEbalovoHTMLTags     = regexp.MustCompile(`<[^>]*>`)
	reEbalovoTitleSfx     = regexp.MustCompile(`(?i)\s*(?:[-—|:]\s*)?(?:Ebalovo|Ебалово|русское\s+порно|порно\s+видео\s+онлайн).*$`)
	reEbalovoLeadingEmoji = regexp.MustCompile(`^[\s\p{P}\p{S}]+`)
	reEbalovoDigits       = regexp.MustCompile(`[0-9]{3,4}`)
)

type ebalovoCandidate struct {
	height int
	url    string
}

// EbalovoExtractor extracts media streams and listings from ebalovo.porn and mirrors.
type EbalovoExtractor struct{}

func (e *EbalovoExtractor) Name() string {
	return "ebalovo"
}

func (e *EbalovoExtractor) Description() string {
	return "Ebalovo video and listing extractor"
}

func (e *EbalovoExtractor) Capabilities() Capability {
	return CapVideo | CapPlaylist
}

func (e *EbalovoExtractor) Match(u *url.URL) bool {
	host := strings.ToLower(u.Hostname())
	return host == "ebalovo.porn" || strings.HasSuffix(host, ".ebalovo.porn") ||
		host == "ebalovo.click" || strings.HasSuffix(host, ".ebalovo.click")
}

func (e *EbalovoExtractor) Extract(ctx context.Context, client *http.Client, targetURL *url.URL, opts common.DownloadOptions) (*Result, error) {
	urlStr := targetURL.String()
	bodyBytes, finalURL, err := common.FetchBytesWithRetry(ctx, client, urlStr, urlStr, common.MaxHTMLBytes, common.DefaultTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch %s: %w", urlStr, err)
	}
	if finalURL != nil {
		targetURL = finalURL
	}

	return e.ExtractFromHTML(ctx, client, targetURL, bodyBytes, opts)
}

func (e *EbalovoExtractor) ExtractFromHTML(ctx context.Context, client *http.Client, targetURL *url.URL, bodyBytes []byte, opts common.DownloadOptions) (*Result, error) {
	bodyStr := string(bodyBytes)
	path := strings.ToLower(targetURL.Path)

	isVideoPath := strings.HasPrefix(path, "/video/") || strings.HasPrefix(path, "/embed/")
	hasPlayerConfig := strings.Contains(bodyStr, "video_url") || strings.Contains(bodyStr, "kt_player")

	if isVideoPath || hasPlayerConfig {
		res, err := e.extractVideo(ctx, client, targetURL, bodyStr, opts)
		if err == nil && res != nil {
			return res, nil
		}
		if isVideoPath {
			return nil, err
		}
	}

	return e.extractListing(ctx, client, targetURL, bodyStr, opts)
}

func (e *EbalovoExtractor) extractVideo(ctx context.Context, client *http.Client, targetURL *url.URL, bodyStr string, opts common.DownloadOptions) (*Result, error) {
	props := e.parsePlayerProps(bodyStr)
	if len(props) == 0 {
		return nil, fmt.Errorf("failed to locate player config on %s", targetURL.String())
	}

	// Follow embed canonical redirect when available to unlock higher quality sources
	if props["video_alt_url_redirect"] == "1" {
		canonical := props["video_alt_url"]
		if strings.Contains(canonical, "/video/") {
			canURL, err := common.ValidateRemoteURL(canonical)
			if err == nil {
				canBytes, finalURL, fetchErr := common.FetchBytesWithRetry(ctx, client, canURL.String(), targetURL.String(), common.MaxHTMLBytes, common.DefaultTimeout)
				if fetchErr == nil {
					if finalURL != nil {
						canURL = finalURL
					}
					canProps := e.parsePlayerProps(string(canBytes))
					if len(canProps) > 0 {
						props = canProps
						targetURL = canURL
						bodyStr = string(canBytes)
					}
				}
			}
		}
	}

	licenseCode := props["license_code"]
	candidates := e.collectCandidates(props, licenseCode, targetURL)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no playable video streams found on %s", targetURL.String())
	}

	streamURL := e.selectQuality(candidates, opts.Quality)
	title := e.extractVideoTitle(bodyStr, targetURL)

	return &Result{
		Type: ResultTypeVideo,
		Video: &VideoResult{
			Title:     title,
			PageURL:   targetURL.String(),
			StreamURL: streamURL,
			Referer:   targetURL.String(),
		},
	}, nil
}

func (e *EbalovoExtractor) parsePlayerProps(bodyStr string) map[string]string {
	scripts := reEbalovoScript.FindAllStringSubmatch(bodyStr, -1)
	props := make(map[string]string)

	for _, s := range scripts {
		if len(s) < 2 {
			continue
		}
		scriptContent := s[1]
		if !strings.Contains(scriptContent, "video_url") && !strings.Contains(scriptContent, "kt_player") {
			continue
		}
		for _, m := range reEbalovoProp.FindAllStringSubmatch(scriptContent, -1) {
			if len(m) >= 3 {
				props[strings.ToLower(m[1])] = m[2]
			}
		}
		if len(props) > 0 {
			break
		}
	}

	return props
}

func (e *EbalovoExtractor) collectCandidates(props map[string]string, licenseCode string, targetURL *url.URL) []ebalovoCandidate {
	urlKeys := []string{
		"video_url",
		"video_alt_url",
		"video_alt_url1",
		"video_alt_url2",
		"video_alt_url3",
		"video_alt_url4",
		"video_alt_url5",
	}

	var candidates []ebalovoCandidate
	for _, key := range urlKeys {
		rawURL, ok := props[key]
		if !ok || strings.TrimSpace(rawURL) == "" {
			continue
		}

		if props[key+"_redirect"] == "1" {
			continue
		}

		realURL := rawURL
		if strings.HasPrefix(realURL, "function/0/") && licenseCode != "" {
			realURL = kvsGetRealURL(realURL, licenseCode)
		}
		if strings.HasPrefix(realURL, "/") {
			realURL = fmt.Sprintf("%s://%s%s", targetURL.Scheme, targetURL.Host, realURL)
		}

		textLabel := props[key+"_text"]
		h := e.parseHeight(textLabel, realURL)
		if key == "video_url" && props["video_url_hd"] == "1" && h < 720 {
			h = 720
		}

		candidates = append(candidates, ebalovoCandidate{
			height: h,
			url:    realURL,
		})
	}

	return candidates
}

func (e *EbalovoExtractor) parseHeight(label string, streamURL string) int {
	labelLower := strings.ToLower(label)
	if strings.Contains(labelLower, "1080") {
		return 1080
	}
	if strings.Contains(labelLower, "720") {
		return 720
	}
	if strings.Contains(labelLower, "480") {
		return 480
	}
	if strings.Contains(labelLower, "360") {
		return 360
	}
	if strings.Contains(labelLower, "240") {
		return 240
	}
	if labelLower == "hq" || labelLower == "hd" {
		return 720
	}
	if labelLower == "lq" || labelLower == "sd" {
		return 360
	}

	urlLower := strings.ToLower(streamURL)
	if m := reEbalovoDigits.FindString(urlLower); m != "" {
		if val, err := strconv.Atoi(m); err == nil && val >= 240 && val <= 2160 {
			return val
		}
	}
	if strings.Contains(urlLower, "_h.mp4") || strings.Contains(urlLower, "_720p") {
		return 720
	}
	if strings.Contains(urlLower, ".mp4") {
		return 480
	}

	return 0
}

func (e *EbalovoExtractor) selectQuality(candidates []ebalovoCandidate, targetQuality string) string {
	q := strings.ToLower(strings.TrimSpace(targetQuality))

	if q == "low" || q == "lq" || q == "360" {
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

	if q == "best" || q == "high" || q == "hq" || q == "" {
		maxH := -1
		bestURL := candidates[0].url
		for _, c := range candidates {
			if c.height > maxH {
				maxH = c.height
				bestURL = c.url
			}
		}
		return bestURL
	}

	targetH := e.parseHeight(q, "")
	if targetH == 0 {
		maxH := -1
		bestURL := candidates[0].url
		for _, c := range candidates {
			if c.height > maxH {
				maxH = c.height
				bestURL = c.url
			}
		}
		return bestURL
	}

	for _, c := range candidates {
		if c.height == targetH {
			return c.url
		}
	}

	closestURL := candidates[0].url
	smallestDiff := 99999
	for _, c := range candidates {
		diff := c.height - targetH
		if diff < 0 {
			diff = -diff
		}
		if diff < smallestDiff {
			smallestDiff = diff
			closestURL = c.url
		}
	}

	return closestURL
}

func (e *EbalovoExtractor) extractListing(ctx context.Context, client *http.Client, targetURL *url.URL, bodyStr string, opts common.DownloadOptions) (*Result, error) {
	var entries []PlaylistEntry
	seen := make(map[string]bool)

	cardMatches := reEbalovoVideoCard.FindAllStringSubmatch(bodyStr, -1)
	for _, m := range cardMatches {
		href := m[1]
		if strings.HasPrefix(href, "/") {
			href = fmt.Sprintf("%s://%s%s", targetURL.Scheme, targetURL.Host, href)
		}
		if seen[href] {
			continue
		}
		seen[href] = true

		titleText := ""
		if len(m) > 3 && strings.TrimSpace(m[3]) != "" {
			titleText = m[3]
		} else if len(m) > 2 {
			if altM := reEbalovoAltImg.FindStringSubmatch(m[2]); len(altM) > 1 {
				titleText = altM[1]
			}
		}
		cleanTitle := e.cleanTitleString(titleText)
		if cleanTitle == "" {
			cleanTitle = filepath.Base(strings.TrimSuffix(href, "/"))
		}

		entries = append(entries, PlaylistEntry{
			URL:   href,
			Title: common.SanitizeFilename(cleanTitle),
		})
	}

	if len(entries) == 0 {
		linkMatches := reEbalovoVideoLink.FindAllStringSubmatch(bodyStr, -1)
		for _, m := range linkMatches {
			href := m[1]
			if strings.HasPrefix(href, "/") {
				href = fmt.Sprintf("%s://%s%s", targetURL.Scheme, targetURL.Host, href)
			}
			if seen[href] {
				continue
			}
			seen[href] = true
			slug := filepath.Base(strings.TrimSuffix(href, "/"))
			entries = append(entries, PlaylistEntry{
				URL:   href,
				Title: common.SanitizeFilename(slug),
			})
		}
	}

	if len(entries) == 0 {
		return nil, fmt.Errorf("no video entries found on %s", targetURL.String())
	}

	if opts.NoPlaylist {
		firstURL, err := common.ValidateRemoteURL(entries[0].URL)
		if err != nil {
			return nil, err
		}
		return e.Extract(ctx, client, firstURL, opts)
	}

	listingTitle := e.extractListingTitle(bodyStr, targetURL)

	return &Result{
		Type: ResultTypePlaylist,
		Playlist: &PlaylistResult{
			Title:   listingTitle,
			Entries: entries,
		},
	}, nil
}

func (e *EbalovoExtractor) extractVideoTitle(bodyStr string, targetURL *url.URL) string {
	raw := ""
	if m := reEbalovoH1.FindStringSubmatch(bodyStr); len(m) > 1 && strings.TrimSpace(m[1]) != "" {
		raw = m[1]
	} else if m := reEbalovoOGTitle.FindStringSubmatch(bodyStr); len(m) > 1 && strings.TrimSpace(m[1]) != "" {
		raw = m[1]
	} else if m := reEbalovoTitleTag.FindStringSubmatch(bodyStr); len(m) > 1 && strings.TrimSpace(m[1]) != "" {
		raw = m[1]
	}

	cleaned := e.cleanTitleString(raw)
	if cleaned == "" {
		cleaned = filepath.Base(strings.TrimSuffix(targetURL.Path, "/"))
	}
	return common.SanitizeFilename(cleaned)
}

func (e *EbalovoExtractor) extractListingTitle(bodyStr string, targetURL *url.URL) string {
	raw := ""
	if m := reEbalovoH1.FindStringSubmatch(bodyStr); len(m) > 1 && strings.TrimSpace(m[1]) != "" {
		raw = m[1]
	} else if m := reEbalovoOGTitle.FindStringSubmatch(bodyStr); len(m) > 1 && strings.TrimSpace(m[1]) != "" {
		raw = m[1]
	} else if m := reEbalovoTitleTag.FindStringSubmatch(bodyStr); len(m) > 1 && strings.TrimSpace(m[1]) != "" {
		raw = m[1]
	} else if m := reEbalovoMetaDesc.FindStringSubmatch(bodyStr); len(m) > 1 && strings.TrimSpace(m[1]) != "" {
		raw = m[1]
	}

	cleaned := e.cleanTitleString(raw)
	if cleaned == "" {
		cleaned = filepath.Base(strings.TrimSuffix(targetURL.Path, "/"))
	}
	return common.SanitizeFilename(cleaned)
}

func (e *EbalovoExtractor) cleanTitleString(raw string) string {
	raw = html.UnescapeString(raw)
	raw = reEbalovoHTMLTags.ReplaceAllString(raw, "")
	raw = strings.TrimSpace(raw)
	raw = reEbalovoLeadingEmoji.ReplaceAllString(raw, "")
	raw = reEbalovoTitleSfx.ReplaceAllString(raw, "")
	raw = strings.TrimSpace(raw)
	raw = strings.TrimSuffix(raw, ".")
	return strings.TrimSpace(raw)
}

func init() {
	Register(&EbalovoExtractor{})
}
