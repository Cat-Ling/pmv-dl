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
	"strconv"
	"strings"

	"pmv-dl/internal/common"
)

var (
	reEpornerVideoPath = regexp.MustCompile(`(?i)/(?:hd-porn/|embed/|video-)([a-zA-Z0-9]+)(?:/([a-zA-Z0-9_-]+))?/?`)
	reEpornerHash      = regexp.MustCompile(`(?i)(?:hash\s*[:=]|EP\.video\.player\.hash\s*=)\s*['"]([0-9a-fA-F]{32})['"]`)
	reEpornerVideoLink = regexp.MustCompile(`href=["']((?:https?://(?:www\.)?eporner\.com)?/(?:video-[a-zA-Z0-9]+|hd-porn/[a-zA-Z0-9]+)/[^"'\s>]+)["']`)
	reEpornerVideoCard = regexp.MustCompile(`(?is)<div[^>]*class=["'][^"']*mbinfo[^"']*["'][^>]*>[\s\S]*?<a[^>]+href=["']((?:https?://[^"'\s>]+)?/(?:video-[a-zA-Z0-9]+|hd-porn/[a-zA-Z0-9]+)/[^"'\s>]+)["'][^>]*title=["']([^"']+)["']`)
	reEpornerSource    = regexp.MustCompile(`(?is)<source[^>]+src=["']([^"']+)["']`)
	reEpornerH1        = regexp.MustCompile(`(?is)<h1[^>]*>(.*?)</h1>`)
	reEpornerTitleTag  = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	reEpornerOGTitle   = regexp.MustCompile(`(?is)<meta[^>]+property=["']og:title["'][^>]+content=["'](.*?)["']\s*(?:/?>|[a-zA-Z_-]+=)`)
	reEpornerHTMLTags  = regexp.MustCompile(`<[^>]*>`)
	reEpornerTitleSfx  = regexp.MustCompile(`(?i)\s*(?:[-|]\s*)?(?:EPORNER|Watch\s+HD\s+Porn\s+Videos).*$`)
	reEpornerDigits    = regexp.MustCompile(`[0-9]{3,4}`)
)

type epornerCandidate struct {
	height int
	url    string
}

type epornerXHRSourceItem struct {
	Src string `json:"src"`
}

type epornerXHRResponse struct {
	Available bool                                     `json:"available"`
	Message   string                                   `json:"message"`
	Title     string                                   `json:"title"`
	Sources   map[string]map[string]epornerXHRSourceItem `json:"sources"`
}

// EpornerExtractor extracts videos and playlist feeds from eporner.com.
type EpornerExtractor struct{}

func (e *EpornerExtractor) Name() string {
	return "eporner"
}

func (e *EpornerExtractor) Description() string {
	return "Eporner video and listing extractor"
}

func (e *EpornerExtractor) Capabilities() Capability {
	return CapVideo | CapPlaylist
}

func (e *EpornerExtractor) Match(u *url.URL) bool {
	host := strings.ToLower(u.Hostname())
	return host == "eporner.com" || strings.HasSuffix(host, ".eporner.com")
}

func (e *EpornerExtractor) Extract(ctx context.Context, client *http.Client, targetURL *url.URL, opts common.DownloadOptions) (*Result, error) {
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

func (e *EpornerExtractor) ExtractFromHTML(ctx context.Context, client *http.Client, targetURL *url.URL, bodyBytes []byte, opts common.DownloadOptions) (*Result, error) {
	bodyStr := string(bodyBytes)
	path := targetURL.Path

	isVideoPath := reEpornerVideoPath.MatchString(path)
	hasPlayerHash := reEpornerHash.MatchString(bodyStr)

	if isVideoPath || hasPlayerHash {
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

func (e *EpornerExtractor) extractVideo(ctx context.Context, client *http.Client, targetURL *url.URL, bodyStr string, opts common.DownloadOptions) (*Result, error) {
	videoID := ""
	if m := reEpornerVideoPath.FindStringSubmatch(targetURL.Path); len(m) > 1 {
		videoID = m[1]
	}

	var candidates []epornerCandidate
	title := ""

	hashMatch := reEpornerHash.FindStringSubmatch(bodyStr)
	if len(hashMatch) > 1 && videoID != "" {
		calcHash, err := calculateEpornerHash(hashMatch[1])
		if err == nil {
			xhrURL := fmt.Sprintf("%s://%s/xhr/video/%s?hash=%s&device=generic&domain=www.eporner.com&fallback=false", targetURL.Scheme, targetURL.Host, videoID, calcHash)
			xhrBytes, _, xhrErr := common.FetchBytesWithRetry(ctx, client, xhrURL, targetURL.String(), common.MaxHTMLBytes, common.DefaultTimeout)
			if xhrErr == nil {
				var xResp epornerXHRResponse
				if jsonErr := json.Unmarshal(xhrBytes, &xResp); jsonErr == nil && len(xResp.Sources) > 0 {
					if strings.TrimSpace(xResp.Title) != "" {
						title = xResp.Title
					}
					candidates = e.parseXHRSources(xResp.Sources)
				}
			}
		}
	}

	if len(candidates) == 0 {
		for _, m := range reEpornerSource.FindAllStringSubmatch(bodyStr, -1) {
			srcURL := m[1]
			h := e.parseHeight("", srcURL)
			candidates = append(candidates, epornerCandidate{
				height: h,
				url:    srcURL,
			})
		}
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("no playable video streams found on %s", targetURL.String())
	}

	if title == "" {
		title = e.extractVideoTitle(bodyStr, targetURL)
	} else {
		title = e.cleanTitleString(title)
	}

	streamURL := e.selectQuality(candidates, opts.Quality)

	return &Result{
		Type: ResultTypeVideo,
		Video: &VideoResult{
			Title:     common.SanitizeFilename(title),
			PageURL:   targetURL.String(),
			StreamURL: streamURL,
			Referer:   targetURL.String(),
		},
	}, nil
}

func calculateEpornerHash(rawHash string) (string, error) {
	if len(rawHash) != 32 {
		return "", fmt.Errorf("invalid hash length %d", len(rawHash))
	}
	var sb strings.Builder
	for i := 0; i < 32; i += 8 {
		chunk := rawHash[i : i+8]
		val, err := strconv.ParseUint(chunk, 16, 64)
		if err != nil {
			return "", err
		}
		sb.WriteString(strconv.FormatUint(val, 36))
	}
	return sb.String(), nil
}

func (e *EpornerExtractor) parseXHRSources(sources map[string]map[string]epornerXHRSourceItem) []epornerCandidate {
	var candidates []epornerCandidate
	for kind, sdict := range sources {
		for formatID, item := range sdict {
			src := strings.TrimSpace(item.Src)
			if src == "" || !strings.HasPrefix(src, "http") {
				continue
			}
			h := e.parseHeight(formatID, src)
			if kind == "hls" && h == 0 {
				h = 1080
			}
			candidates = append(candidates, epornerCandidate{
				height: h,
				url:    src,
			})
		}
	}
	return candidates
}

func (e *EpornerExtractor) parseHeight(label string, streamURL string) int {
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

	urlLower := strings.ToLower(streamURL)
	if m := reEpornerDigits.FindString(urlLower); m != "" {
		if val, err := strconv.Atoi(m); err == nil && val >= 240 && val <= 2160 {
			return val
		}
	}

	return 720
}

func (e *EpornerExtractor) selectQuality(candidates []epornerCandidate, targetQuality string) string {
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

func (e *EpornerExtractor) extractListing(ctx context.Context, client *http.Client, targetURL *url.URL, bodyStr string, opts common.DownloadOptions) (*Result, error) {
	var entries []PlaylistEntry
	seen := make(map[string]bool)

	cardMatches := reEpornerVideoCard.FindAllStringSubmatch(bodyStr, -1)
	for _, m := range cardMatches {
		href := m[1]
		if strings.HasPrefix(href, "/") {
			href = fmt.Sprintf("%s://%s%s", targetURL.Scheme, targetURL.Host, href)
		}
		if seen[href] {
			continue
		}
		seen[href] = true

		titleText := m[2]
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
		linkMatches := reEpornerVideoLink.FindAllStringSubmatch(bodyStr, -1)
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

func (e *EpornerExtractor) extractVideoTitle(bodyStr string, targetURL *url.URL) string {
	raw := ""
	if m := reEpornerOGTitle.FindStringSubmatch(bodyStr); len(m) > 1 && strings.TrimSpace(m[1]) != "" {
		raw = m[1]
	} else if m := reEpornerTitleTag.FindStringSubmatch(bodyStr); len(m) > 1 && strings.TrimSpace(m[1]) != "" {
		raw = m[1]
	} else if m := reEpornerH1.FindStringSubmatch(bodyStr); len(m) > 1 && strings.TrimSpace(m[1]) != "" {
		raw = m[1]
	}

	cleaned := e.cleanTitleString(raw)
	if cleaned == "" {
		cleaned = filepath.Base(strings.TrimSuffix(targetURL.Path, "/"))
	}
	return common.SanitizeFilename(cleaned)
}

func (e *EpornerExtractor) extractListingTitle(bodyStr string, targetURL *url.URL) string {
	raw := ""
	if m := reEpornerH1.FindStringSubmatch(bodyStr); len(m) > 1 && strings.TrimSpace(m[1]) != "" {
		raw = m[1]
	} else if m := reEpornerOGTitle.FindStringSubmatch(bodyStr); len(m) > 1 && strings.TrimSpace(m[1]) != "" {
		raw = m[1]
	} else if m := reEpornerTitleTag.FindStringSubmatch(bodyStr); len(m) > 1 && strings.TrimSpace(m[1]) != "" {
		raw = m[1]
	}

	cleaned := e.cleanTitleString(raw)
	if cleaned == "" {
		cleaned = filepath.Base(strings.TrimSuffix(targetURL.Path, "/"))
	}
	return common.SanitizeFilename(cleaned)
}

func (e *EpornerExtractor) cleanTitleString(raw string) string {
	raw = html.UnescapeString(raw)
	raw = reEpornerHTMLTags.ReplaceAllString(raw, "")
	raw = strings.TrimSpace(raw)
	raw = reEpornerTitleSfx.ReplaceAllString(raw, "")
	raw = strings.TrimSpace(raw)
	raw = strings.TrimRight(raw, " .-_/|~…")
	return strings.TrimSpace(raw)
}

func init() {
	Register(&EpornerExtractor{})
}
