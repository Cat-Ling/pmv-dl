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
	reHeavyFetishVideoPath = regexp.MustCompile(`^/videos/([0-9]+)/?`)
	reHeavyFetishEmbedPath = regexp.MustCompile(`^/embed/([0-9]+)/?`)
	reHeavyFetishScript    = regexp.MustCompile(`(?is)<script[^>]*>(.*?)</script>`)
	reHeavyFetishProp      = regexp.MustCompile(`(?i)["']?([a-zA-Z0-9_]+)["']?\s*:\s*['"]([^'"]*)['"]`)
	reHeavyFetishH1        = regexp.MustCompile(`(?is)<h1[^>]*>(.*?)</h1>`)
	reHeavyFetishTitleTag  = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	reHeavyFetishOGTitle   = regexp.MustCompile(`(?is)<meta[^>]+property=["']og:title["'][^>]+content=["'](.*?)["']\s*(?:/?>|[a-zA-Z_-]+=)`)
	reHeavyFetishVideoCard = regexp.MustCompile(`(?is)<a[^>]+href=["']((?:https?://[^"'\s>]+)?/videos/([0-9]+)/[^"'\s>]+)["'][^>]*title=["']([^"']+)["']`)
	reHeavyFetishVideoLink = regexp.MustCompile(`(?is)href=["']((?:https?://(?:www\.)?heavyfetish\.com)?/videos/([0-9]+)/[^"'\s>]+)["']`)
	reHeavyFetishHTMLTags  = regexp.MustCompile(`<[^>]*>`)
	reHeavyFetishTitleSfx  = regexp.MustCompile(`(?i)\s*(?:[-|]\s*)?(?:HeavyFetish|Watch\s+Heavy\s+Fetish\s+Videos).*$`)
	reHeavyFetishDigits    = regexp.MustCompile(`[0-9]{3,4}`)
)

type heavyFetishCandidate struct {
	height int
	url    string
}

// HeavyFetishExtractor extracts videos and listings from heavyfetish.com.
type HeavyFetishExtractor struct{}

func (h *HeavyFetishExtractor) Name() string {
	return "heavyfetish"
}

func (h *HeavyFetishExtractor) Description() string {
	return "HeavyFetish video and listing extractor"
}

func (h *HeavyFetishExtractor) Capabilities() Capability {
	return CapVideo | CapPlaylist
}

func (h *HeavyFetishExtractor) Match(u *url.URL) bool {
	host := strings.ToLower(u.Hostname())
	return host == "heavyfetish.com" || strings.HasSuffix(host, ".heavyfetish.com")
}

func (h *HeavyFetishExtractor) Extract(ctx context.Context, client *http.Client, targetURL *url.URL, opts common.DownloadOptions) (*Result, error) {
	urlStr := targetURL.String()
	bodyBytes, finalURL, err := common.FetchBytesWithRetry(ctx, client, urlStr, urlStr, common.MaxHTMLBytes, common.DefaultTimeout)
	if err != nil {
		if strings.Contains(err.Error(), "403") {
			return nil, fmt.Errorf("heavyfetish.com returned HTTP 403 (BunnyCDN Shield challenge active). Supply session cookies via --cookies-from-browser <browser>, --cookies <file_or_string>, or custom headers via --add-header: %w", err)
		}
		return nil, fmt.Errorf("failed to fetch %s: %w", urlStr, err)
	}
	if finalURL != nil {
		targetURL = finalURL
	}

	return h.ExtractFromHTML(ctx, client, targetURL, bodyBytes, opts)
}

func (h *HeavyFetishExtractor) ExtractFromHTML(ctx context.Context, client *http.Client, targetURL *url.URL, bodyBytes []byte, opts common.DownloadOptions) (*Result, error) {
	bodyStr := string(bodyBytes)
	path := strings.ToLower(targetURL.Path)

	if strings.Contains(bodyStr, "bunny-shield") || strings.Contains(bodyStr, "shield-challenge") {
		return nil, fmt.Errorf("heavyfetish.com returned a BunnyCDN Shield challenge page; supply session cookies via --cookies-from-browser <browser> or --cookies <file_or_string>")
	}

	isVideoPath := reHeavyFetishVideoPath.MatchString(path) || reHeavyFetishEmbedPath.MatchString(path)
	hasKVS := strings.Contains(bodyStr, "flashvars") || strings.Contains(bodyStr, "video_url") || strings.Contains(bodyStr, "kt_player")

	if isVideoPath || hasKVS {
		res, err := h.extractVideo(targetURL, bodyStr, opts)
		if err == nil && res != nil {
			return res, nil
		}
		if isVideoPath {
			return nil, err
		}
	}

	return h.extractListing(ctx, client, targetURL, bodyStr, opts)
}

func (h *HeavyFetishExtractor) extractVideo(targetURL *url.URL, bodyStr string, opts common.DownloadOptions) (*Result, error) {
	props := h.parsePlayerProps(bodyStr)
	if len(props) == 0 {
		return nil, fmt.Errorf("failed to locate player config on %s", targetURL.String())
	}

	licenseCode := props["license_code"]
	candidates := h.collectCandidates(props, licenseCode, targetURL)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no playable video streams found on %s", targetURL.String())
	}

	streamURL := h.selectQuality(candidates, opts.Quality)
	title := h.extractVideoTitle(bodyStr, targetURL)

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

func (h *HeavyFetishExtractor) parsePlayerProps(bodyStr string) map[string]string {
	scripts := reHeavyFetishScript.FindAllStringSubmatch(bodyStr, -1)
	props := make(map[string]string)

	for _, s := range scripts {
		if len(s) < 2 {
			continue
		}
		scriptContent := s[1]
		if !strings.Contains(scriptContent, "video_url") && !strings.Contains(scriptContent, "kt_player") && !strings.Contains(scriptContent, "flashvars") {
			continue
		}
		for _, m := range reHeavyFetishProp.FindAllStringSubmatch(scriptContent, -1) {
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

func (h *HeavyFetishExtractor) collectCandidates(props map[string]string, licenseCode string, targetURL *url.URL) []heavyFetishCandidate {
	urlKeys := []string{
		"video_url",
		"video_alt_url",
		"video_alt_url1",
		"video_alt_url2",
		"video_alt_url3",
		"video_alt_url4",
		"video_alt_url5",
	}

	var candidates []heavyFetishCandidate
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
		height := h.parseHeight(textLabel, realURL)
		if key == "video_url" && props["video_url_hd"] == "1" && height < 720 {
			height = 720
		}

		candidates = append(candidates, heavyFetishCandidate{
			height: height,
			url:    realURL,
		})
	}

	return candidates
}

func (h *HeavyFetishExtractor) parseHeight(label string, streamURL string) int {
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
	if m := reHeavyFetishDigits.FindString(urlLower); m != "" {
		if val, err := strconv.Atoi(m); err == nil && val >= 240 && val <= 2160 {
			return val
		}
	}
	if strings.Contains(urlLower, "_720p") || strings.Contains(urlLower, "_hd.mp4") {
		return 720
	}
	if strings.Contains(urlLower, "_hq.mp4") {
		return 480
	}
	if strings.Contains(urlLower, ".mp4") {
		return 480
	}

	return 720
}

func (h *HeavyFetishExtractor) selectQuality(candidates []heavyFetishCandidate, targetQuality string) string {
	q := strings.ToLower(strings.TrimSpace(targetQuality))

	if q == "low" || q == "lq" || q == "360" || q == "240" {
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

	targetH := h.parseHeight(q, "")
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

func (h *HeavyFetishExtractor) extractListing(ctx context.Context, client *http.Client, targetURL *url.URL, bodyStr string, opts common.DownloadOptions) (*Result, error) {
	var entries []PlaylistEntry
	seen := make(map[string]bool)

	cardMatches := reHeavyFetishVideoCard.FindAllStringSubmatch(bodyStr, -1)
	for _, m := range cardMatches {
		href := m[1]
		if strings.HasPrefix(href, "/") {
			href = fmt.Sprintf("%s://%s%s", targetURL.Scheme, targetURL.Host, href)
		}
		id := m[2]
		if seen[id] {
			continue
		}
		seen[id] = true

		titleText := m[3]
		cleanTitle := h.cleanTitleString(titleText)
		if cleanTitle == "" {
			cleanTitle = fmt.Sprintf("video-%s", id)
		}

		entries = append(entries, PlaylistEntry{
			URL:   href,
			Title: common.SanitizeFilename(cleanTitle),
		})
	}

	if len(entries) == 0 {
		linkMatches := reHeavyFetishVideoLink.FindAllStringSubmatch(bodyStr, -1)
		for _, m := range linkMatches {
			href := m[1]
			if strings.HasPrefix(href, "/") {
				href = fmt.Sprintf("%s://%s%s", targetURL.Scheme, targetURL.Host, href)
			}
			id := m[2]
			if seen[id] {
				continue
			}
			seen[id] = true

			entries = append(entries, PlaylistEntry{
				URL:   href,
				Title: common.SanitizeFilename(fmt.Sprintf("video-%s", id)),
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
		return h.Extract(ctx, client, firstURL, opts)
	}

	listingTitle := h.extractListingTitle(bodyStr, targetURL)

	return &Result{
		Type: ResultTypePlaylist,
		Playlist: &PlaylistResult{
			Title:   listingTitle,
			Entries: entries,
		},
	}, nil
}

func (h *HeavyFetishExtractor) extractVideoTitle(bodyStr string, targetURL *url.URL) string {
	raw := ""
	if m := reHeavyFetishH1.FindStringSubmatch(bodyStr); len(m) > 1 && strings.TrimSpace(m[1]) != "" {
		raw = m[1]
	} else if m := reHeavyFetishOGTitle.FindStringSubmatch(bodyStr); len(m) > 1 && strings.TrimSpace(m[1]) != "" {
		raw = m[1]
	} else if m := reHeavyFetishTitleTag.FindStringSubmatch(bodyStr); len(m) > 1 && strings.TrimSpace(m[1]) != "" {
		raw = m[1]
	}

	cleaned := h.cleanTitleString(raw)
	if cleaned == "" {
		cleaned = filepath.Base(strings.TrimSuffix(targetURL.Path, "/"))
	}
	return common.SanitizeFilename(cleaned)
}

func (h *HeavyFetishExtractor) extractListingTitle(bodyStr string, targetURL *url.URL) string {
	raw := ""
	if m := reHeavyFetishH1.FindStringSubmatch(bodyStr); len(m) > 1 && strings.TrimSpace(m[1]) != "" {
		raw = m[1]
	} else if m := reHeavyFetishOGTitle.FindStringSubmatch(bodyStr); len(m) > 1 && strings.TrimSpace(m[1]) != "" {
		raw = m[1]
	} else if m := reHeavyFetishTitleTag.FindStringSubmatch(bodyStr); len(m) > 1 && strings.TrimSpace(m[1]) != "" {
		raw = m[1]
	}

	cleaned := h.cleanTitleString(raw)
	if cleaned == "" {
		cleaned = filepath.Base(strings.TrimSuffix(targetURL.Path, "/"))
	}
	return common.SanitizeFilename(cleaned)
}

func (h *HeavyFetishExtractor) cleanTitleString(raw string) string {
	raw = html.UnescapeString(raw)
	raw = reHeavyFetishHTMLTags.ReplaceAllString(raw, "")
	raw = strings.TrimSpace(raw)
	raw = reHeavyFetishTitleSfx.ReplaceAllString(raw, "")
	raw = strings.TrimSpace(raw)
	raw = strings.TrimRight(raw, " .-_/|~…")
	return strings.TrimSpace(raw)
}

func init() {
	Register(&HeavyFetishExtractor{})
}
