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
	reRuvideosVideoPath  = regexp.MustCompile(`^/videos/([0-9]+)/?$`)
	reRuvideosEmbedPath  = regexp.MustCompile(`^/embed/([0-9]+)/?$`)
	reRuvideosProp       = regexp.MustCompile(`(?i)["']?([a-zA-Z0-9_]+)["']?\s*:\s*['"]([^'"]*)['"]`)
	reRuvideosScript     = regexp.MustCompile(`(?is)<script[^>]*>(.*?)</script>`)
	reRuvideosSource     = regexp.MustCompile(`(?is)<source[^>]+src=["']([^"']+)["'][^>]*>`)
	reRuvideosLabel      = regexp.MustCompile(`(?is)label=["']([^"']+)["']`)
	reRuvideosH1         = regexp.MustCompile(`(?is)<h1[^>]*>(.*?)</h1>`)
	reRuvideosTitleTag   = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	reRuvideosOGTitle    = regexp.MustCompile(`(?is)<meta[^>]+property=["']og:title["'][^>]+content=["'](.*?)["']\s*(?:/?>|[a-zA-Z_-]+=)`)
	reRuvideosMetaDesc   = regexp.MustCompile(`(?is)<meta[^>]+name=["']description["'][^>]+content=["'](.*?)["']\s*(?:/?>|[a-zA-Z_-]+=)`)
	reRuvideosVideoCard  = regexp.MustCompile(`(?is)<a[^>]+href=["']((?:https?://[^"'\s>]+)?/videos/([0-9]+)/?)["'][^>]*title=["']([^"']+)["']`)
	reRuvideosVideoLink  = regexp.MustCompile(`(?is)href=["']((?:https?://(?:www\.)?ruvideos\.net)?/videos/([0-9]+)/?)["']`)
	reRuvideosItemTitle  = regexp.MustCompile(`(?is)<strong[^>]*class=["'][^"']*title[^"']*["'][^>]*>(.*?)</strong>`)
	reRuvideosHTMLTags   = regexp.MustCompile(`<[^>]*>`)
	reRuvideosTitleSfx   = regexp.MustCompile(`(?i)\s*(?:[/|~-]\s*)?(?:Ruvideos(?:\.net)?|free\s+porn\s+video|Ruvideos\s+Hentai\s+Tube|Embed\s+плеер).*$`)
	reRuvideosDigits     = regexp.MustCompile(`[0-9]{3,4}`)
)

type ruvideosCandidate struct {
	height int
	url    string
}

// RuvideosExtractor extracts videos and listings from ruvideos.net.
type RuvideosExtractor struct{}

func (r *RuvideosExtractor) Name() string {
	return "ruvideos"
}

func (r *RuvideosExtractor) Description() string {
	return "Ruvideos video and listing extractor"
}

func (r *RuvideosExtractor) Capabilities() Capability {
	return CapVideo | CapPlaylist
}

func (r *RuvideosExtractor) Match(u *url.URL) bool {
	host := strings.ToLower(u.Hostname())
	return host == "ruvideos.net" || strings.HasSuffix(host, ".ruvideos.net")
}

func (r *RuvideosExtractor) Extract(ctx context.Context, client *http.Client, targetURL *url.URL, opts common.DownloadOptions) (*Result, error) {
	urlStr := targetURL.String()
	bodyBytes, finalURL, err := common.FetchBytesWithRetry(ctx, client, urlStr, urlStr, common.MaxHTMLBytes, common.DefaultTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch %s: %w", urlStr, err)
	}
	if finalURL != nil {
		targetURL = finalURL
	}

	return r.ExtractFromHTML(ctx, client, targetURL, bodyBytes, opts)
}

func (r *RuvideosExtractor) ExtractFromHTML(ctx context.Context, client *http.Client, targetURL *url.URL, bodyBytes []byte, opts common.DownloadOptions) (*Result, error) {
	bodyStr := string(bodyBytes)
	path := strings.ToLower(targetURL.Path)

	// If embed page, attempt canonical /videos/<id>/ first to unlock high-res streams
	if m := reRuvideosEmbedPath.FindStringSubmatch(path); len(m) > 1 {
		canonicalURLStr := fmt.Sprintf("%s://%s/videos/%s/", targetURL.Scheme, targetURL.Host, m[1])
		canURL, err := common.ValidateRemoteURL(canonicalURLStr)
		if err == nil {
			canBytes, finalURL, fetchErr := common.FetchBytesWithRetry(ctx, client, canURL.String(), targetURL.String(), common.MaxHTMLBytes, common.DefaultTimeout)
			if fetchErr == nil {
				if finalURL != nil {
					canURL = finalURL
				}
				res, extractErr := r.extractVideo(ctx, client, canURL, string(canBytes), opts)
				if extractErr == nil && res != nil {
					return res, nil
				}
			}
		}
	}

	isVideoPath := reRuvideosVideoPath.MatchString(path) || reRuvideosEmbedPath.MatchString(path)
	hasFlashvars := strings.Contains(bodyStr, "flashvars") || strings.Contains(bodyStr, "video_url")

	if isVideoPath || hasFlashvars {
		res, err := r.extractVideo(ctx, client, targetURL, bodyStr, opts)
		if err == nil && res != nil {
			return res, nil
		}
		if isVideoPath {
			return nil, err
		}
	}

	return r.extractListing(ctx, client, targetURL, bodyStr, opts)
}

func (r *RuvideosExtractor) extractVideo(ctx context.Context, client *http.Client, targetURL *url.URL, bodyStr string, opts common.DownloadOptions) (*Result, error) {
	props := r.parsePlayerProps(bodyStr)
	licenseCode := props["license_code"]

	candidates := r.collectCandidates(props, licenseCode, targetURL)

	// Fallback to HTML5 <source> tags if flashvars is absent or incomplete (e.g. embed player)
	if len(candidates) == 0 {
		for _, m := range reRuvideosSource.FindAllStringSubmatch(bodyStr, -1) {
			fullTag := m[0]
			srcURL := m[1]
			label := ""
			if lblM := reRuvideosLabel.FindStringSubmatch(fullTag); len(lblM) > 1 {
				label = lblM[1]
			}
			if strings.HasPrefix(srcURL, "/") {
				srcURL = fmt.Sprintf("%s://%s%s", targetURL.Scheme, targetURL.Host, srcURL)
			}
			h := r.parseHeight(label, srcURL)
			candidates = append(candidates, ruvideosCandidate{
				height: h,
				url:    srcURL,
			})
		}
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("no playable video streams found on %s", targetURL.String())
	}

	streamURL := r.selectQuality(candidates, opts.Quality)
	title := r.extractVideoTitle(bodyStr, props, targetURL)

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

func (r *RuvideosExtractor) parsePlayerProps(bodyStr string) map[string]string {
	scripts := reRuvideosScript.FindAllStringSubmatch(bodyStr, -1)
	props := make(map[string]string)

	for _, s := range scripts {
		if len(s) < 2 {
			continue
		}
		scriptContent := s[1]
		if !strings.Contains(scriptContent, "flashvars") && !strings.Contains(scriptContent, "video_url") && !strings.Contains(scriptContent, "kt_player") {
			continue
		}
		for _, m := range reRuvideosProp.FindAllStringSubmatch(scriptContent, -1) {
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

func (r *RuvideosExtractor) collectCandidates(props map[string]string, licenseCode string, targetURL *url.URL) []ruvideosCandidate {
	urlKeys := []string{
		"video_url",
		"video_alt_url",
		"video_alt_url1",
		"video_alt_url2",
		"video_alt_url3",
		"video_alt_url4",
		"video_alt_url5",
	}

	var candidates []ruvideosCandidate
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
		h := r.parseHeight(textLabel, realURL)
		if key == "video_url" && props["video_url_hd"] == "1" && h < 720 {
			h = 720
		}

		candidates = append(candidates, ruvideosCandidate{
			height: h,
			url:    realURL,
		})
	}

	return candidates
}

func (r *RuvideosExtractor) parseHeight(label string, streamURL string) int {
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
	if m := reRuvideosDigits.FindString(urlLower); m != "" {
		if val, err := strconv.Atoi(m); err == nil && val >= 240 && val <= 2160 {
			return val
		}
	}
	if strings.Contains(urlLower, "_720p") || strings.Contains(urlLower, "_hd.mp4") {
		return 720
	}
	if strings.Contains(urlLower, "_hq.mp4") {
		return 360
	}
	if strings.Contains(urlLower, ".mp4") {
		return 480
	}

	return 0
}

func (r *RuvideosExtractor) selectQuality(candidates []ruvideosCandidate, targetQuality string) string {
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

	targetH := r.parseHeight(q, "")
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

func (r *RuvideosExtractor) extractListing(ctx context.Context, client *http.Client, targetURL *url.URL, bodyStr string, opts common.DownloadOptions) (*Result, error) {
	var entries []PlaylistEntry
	seen := make(map[string]bool)

	cardMatches := reRuvideosVideoCard.FindAllStringSubmatch(bodyStr, -1)
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
		cleanTitle := r.cleanTitleString(titleText)
		if cleanTitle == "" {
			cleanTitle = fmt.Sprintf("video-%s", id)
		}

		entries = append(entries, PlaylistEntry{
			URL:   href,
			Title: common.SanitizeFilename(cleanTitle),
		})
	}

	if len(entries) == 0 {
		linkMatches := reRuvideosVideoLink.FindAllStringSubmatch(bodyStr, -1)
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
		return r.Extract(ctx, client, firstURL, opts)
	}

	listingTitle := r.extractListingTitle(bodyStr, targetURL)

	return &Result{
		Type: ResultTypePlaylist,
		Playlist: &PlaylistResult{
			Title:   listingTitle,
			Entries: entries,
		},
	}, nil
}

func (r *RuvideosExtractor) extractVideoTitle(bodyStr string, props map[string]string, targetURL *url.URL) string {
	if vt, ok := props["video_title"]; ok && strings.TrimSpace(vt) != "" {
		cleaned := r.cleanTitleString(vt)
		if cleaned != "" {
			return common.SanitizeFilename(cleaned)
		}
	}

	raw := ""
	if m := reRuvideosH1.FindStringSubmatch(bodyStr); len(m) > 1 && strings.TrimSpace(m[1]) != "" {
		raw = m[1]
	} else if m := reRuvideosOGTitle.FindStringSubmatch(bodyStr); len(m) > 1 && strings.TrimSpace(m[1]) != "" {
		raw = m[1]
	} else if m := reRuvideosTitleTag.FindStringSubmatch(bodyStr); len(m) > 1 && strings.TrimSpace(m[1]) != "" {
		raw = m[1]
	}

	cleaned := r.cleanTitleString(raw)
	if cleaned == "" {
		cleaned = filepath.Base(strings.TrimSuffix(targetURL.Path, "/"))
	}
	return common.SanitizeFilename(cleaned)
}

func (r *RuvideosExtractor) extractListingTitle(bodyStr string, targetURL *url.URL) string {
	raw := ""
	if m := reRuvideosH1.FindStringSubmatch(bodyStr); len(m) > 1 && strings.TrimSpace(m[1]) != "" {
		raw = m[1]
	} else if m := reRuvideosOGTitle.FindStringSubmatch(bodyStr); len(m) > 1 && strings.TrimSpace(m[1]) != "" {
		raw = m[1]
	} else if m := reRuvideosTitleTag.FindStringSubmatch(bodyStr); len(m) > 1 && strings.TrimSpace(m[1]) != "" {
		raw = m[1]
	} else if m := reRuvideosMetaDesc.FindStringSubmatch(bodyStr); len(m) > 1 && strings.TrimSpace(m[1]) != "" {
		raw = m[1]
	}

	cleaned := r.cleanTitleString(raw)
	if cleaned == "" {
		cleaned = filepath.Base(strings.TrimSuffix(targetURL.Path, "/"))
	}
	return common.SanitizeFilename(cleaned)
}

func (r *RuvideosExtractor) cleanTitleString(raw string) string {
	raw = html.UnescapeString(raw)
	raw = reRuvideosHTMLTags.ReplaceAllString(raw, "")
	raw = strings.TrimSpace(raw)
	raw = reRuvideosTitleSfx.ReplaceAllString(raw, "")
	raw = strings.TrimSpace(raw)
	raw = strings.TrimRight(raw, " .-_/|~")
	return strings.TrimSpace(raw)
}

func init() {
	Register(&RuvideosExtractor{})
}
