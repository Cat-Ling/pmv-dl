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
	rePornHaoVideoPath   = regexp.MustCompile(`^/video/([a-zA-Z0-9_.-]+)/?$`)
	rePornHaoFileSpec    = regexp.MustCompile(`(?i)file\s*:\s*['"]([^'"]+)['"]`)
	rePornHaoBracket     = regexp.MustCompile(`\[([0-9a-zA-Z]+)\]\s*(https?://[^\s,]+)`)
	rePornHaoSource      = regexp.MustCompile(`(?is)<source[^>]+src=["']([^"']+)["']`)
	rePornHaoH1          = regexp.MustCompile(`(?is)<h1[^>]*>(.*?)</h1>`)
	rePornHaoTitleTag    = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	rePornHaoOGTitle     = regexp.MustCompile(`(?is)<meta[^>]+property=["']og:title["'][^>]+content=["'](.*?)["']\s*(?:/?>|[a-zA-Z_-]+=)`)
	rePornHaoVideoCard   = regexp.MustCompile(`(?is)<a[^>]+href=["']((?:https?://[^"'\s>]+)?/video/[^"'\s>]+)["'][^>]*title=["']([^"']+)["']`)
	rePornHaoVideoLink   = regexp.MustCompile(`(?is)href=["']((?:https?://(?:www\.)?pornhao\.com)?/video/[^"'\s>]+)["']`)
	rePornHaoHTMLTags    = regexp.MustCompile(`<[^>]*>`)
	rePornHaoTitleSfx    = regexp.MustCompile(`(?i)\s*(?:[-—|:]\s*)?(?:PornHao|Порно\s*Зак|ПорноЗак|Porn\s*videos).*$`)
	rePornHaoDigits      = regexp.MustCompile(`[0-9]{3,4}`)
)

type pornHaoCandidate struct {
	height int
	url    string
}

// PornHaoExtractor extracts videos and listings from pornhao.com and mirrors.
type PornHaoExtractor struct{}

func (p *PornHaoExtractor) Name() string {
	return "pornhao"
}

func (p *PornHaoExtractor) Description() string {
	return "PornHao video and listing extractor"
}

func (p *PornHaoExtractor) Capabilities() Capability {
	return CapVideo | CapPlaylist
}

func (p *PornHaoExtractor) Match(u *url.URL) bool {
	host := strings.ToLower(u.Hostname())
	return host == "pornhao.com" || strings.HasSuffix(host, ".pornhao.com") ||
		host == "pornozak.top" || strings.HasSuffix(host, ".pornozak.top")
}

func (p *PornHaoExtractor) Extract(ctx context.Context, client *http.Client, targetURL *url.URL, opts common.DownloadOptions) (*Result, error) {
	urlStr := targetURL.String()
	bodyBytes, finalURL, err := common.FetchBytesWithRetry(ctx, client, urlStr, urlStr, common.MaxHTMLBytes, common.DefaultTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch %s: %w", urlStr, err)
	}
	if finalURL != nil {
		targetURL = finalURL
	}

	return p.ExtractFromHTML(ctx, client, targetURL, bodyBytes, opts)
}

func (p *PornHaoExtractor) ExtractFromHTML(ctx context.Context, client *http.Client, targetURL *url.URL, bodyBytes []byte, opts common.DownloadOptions) (*Result, error) {
	bodyStr := string(bodyBytes)
	path := strings.ToLower(targetURL.Path)

	isVideoPath := rePornHaoVideoPath.MatchString(path) || strings.HasPrefix(path, "/video/")
	hasPlayer := strings.Contains(bodyStr, "Playerjs") || strings.Contains(bodyStr, "player-box")

	if isVideoPath || hasPlayer {
		res, err := p.extractVideo(targetURL, bodyStr, opts)
		if err == nil && res != nil {
			return res, nil
		}
		if isVideoPath {
			return nil, err
		}
	}

	return p.extractListing(ctx, client, targetURL, bodyStr, opts)
}

func (p *PornHaoExtractor) extractVideo(targetURL *url.URL, bodyStr string, opts common.DownloadOptions) (*Result, error) {
	candidates := p.collectCandidates(bodyStr)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no playable video streams found on %s", targetURL.String())
	}

	streamURL := p.selectQuality(candidates, opts.Quality)
	title := p.extractVideoTitle(bodyStr, targetURL)

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

func (p *PornHaoExtractor) collectCandidates(bodyStr string) []pornHaoCandidate {
	var candidates []pornHaoCandidate

	if m := rePornHaoFileSpec.FindStringSubmatch(bodyStr); len(m) > 1 {
		fileSpec := m[1]
		bracketMatches := rePornHaoBracket.FindAllStringSubmatch(fileSpec, -1)
		for _, bm := range bracketMatches {
			label := bm[1]
			u := bm[2]
			h := p.parseHeight(label, u)
			candidates = append(candidates, pornHaoCandidate{
				height: h,
				url:    u,
			})
		}

		if len(candidates) == 0 {
			for _, part := range strings.Split(fileSpec, ",") {
				part = strings.TrimSpace(part)
				if strings.HasPrefix(part, "http://") || strings.HasPrefix(part, "https://") {
					h := p.parseHeight("", part)
					candidates = append(candidates, pornHaoCandidate{
						height: h,
						url:    part,
					})
				}
			}
		}
	}

	if len(candidates) == 0 {
		for _, m := range rePornHaoSource.FindAllStringSubmatch(bodyStr, -1) {
			u := m[1]
			h := p.parseHeight("", u)
			candidates = append(candidates, pornHaoCandidate{
				height: h,
				url:    u,
			})
		}
	}

	return candidates
}

func (p *PornHaoExtractor) parseHeight(label string, streamURL string) int {
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
	if strings.Contains(urlLower, "1080p") {
		return 1080
	}
	if strings.Contains(urlLower, "_hq") {
		return 480
	}
	if strings.Contains(urlLower, "_sd") {
		return 360
	}
	if strings.Contains(urlLower, "_m") {
		return 240
	}
	if m := rePornHaoDigits.FindString(urlLower); m != "" {
		if val, err := strconv.Atoi(m); err == nil && val >= 240 && val <= 2160 {
			return val
		}
	}

	return 720
}

func (p *PornHaoExtractor) selectQuality(candidates []pornHaoCandidate, targetQuality string) string {
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

	targetH := p.parseHeight(q, "")
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

func (p *PornHaoExtractor) extractListing(ctx context.Context, client *http.Client, targetURL *url.URL, bodyStr string, opts common.DownloadOptions) (*Result, error) {
	var entries []PlaylistEntry
	seen := make(map[string]bool)

	cardMatches := rePornHaoVideoCard.FindAllStringSubmatch(bodyStr, -1)
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
		cleanTitle := p.cleanTitleString(titleText)
		if cleanTitle == "" {
			cleanTitle = filepath.Base(strings.TrimSuffix(href, "/"))
		}

		entries = append(entries, PlaylistEntry{
			URL:   href,
			Title: common.SanitizeFilename(cleanTitle),
		})
	}

	if len(entries) == 0 {
		linkMatches := rePornHaoVideoLink.FindAllStringSubmatch(bodyStr, -1)
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
		return p.Extract(ctx, client, firstURL, opts)
	}

	listingTitle := p.extractListingTitle(bodyStr, targetURL)

	return &Result{
		Type: ResultTypePlaylist,
		Playlist: &PlaylistResult{
			Title:   listingTitle,
			Entries: entries,
		},
	}, nil
}

func (p *PornHaoExtractor) extractVideoTitle(bodyStr string, targetURL *url.URL) string {
	raw := ""
	if m := rePornHaoH1.FindStringSubmatch(bodyStr); len(m) > 1 && strings.TrimSpace(m[1]) != "" {
		raw = m[1]
	} else if m := rePornHaoOGTitle.FindStringSubmatch(bodyStr); len(m) > 1 && strings.TrimSpace(m[1]) != "" {
		raw = m[1]
	} else if m := rePornHaoTitleTag.FindStringSubmatch(bodyStr); len(m) > 1 && strings.TrimSpace(m[1]) != "" {
		raw = m[1]
	}

	cleaned := p.cleanTitleString(raw)
	if cleaned == "" {
		cleaned = filepath.Base(strings.TrimSuffix(targetURL.Path, "/"))
	}
	return common.SanitizeFilename(cleaned)
}

func (p *PornHaoExtractor) extractListingTitle(bodyStr string, targetURL *url.URL) string {
	raw := ""
	if m := rePornHaoH1.FindStringSubmatch(bodyStr); len(m) > 1 && strings.TrimSpace(m[1]) != "" {
		raw = m[1]
	} else if m := rePornHaoOGTitle.FindStringSubmatch(bodyStr); len(m) > 1 && strings.TrimSpace(m[1]) != "" {
		raw = m[1]
	} else if m := rePornHaoTitleTag.FindStringSubmatch(bodyStr); len(m) > 1 && strings.TrimSpace(m[1]) != "" {
		raw = m[1]
	}

	cleaned := p.cleanTitleString(raw)
	if cleaned == "" {
		cleaned = filepath.Base(strings.TrimSuffix(targetURL.Path, "/"))
	}
	return common.SanitizeFilename(cleaned)
}

func (p *PornHaoExtractor) cleanTitleString(raw string) string {
	raw = html.UnescapeString(raw)
	raw = rePornHaoHTMLTags.ReplaceAllString(raw, "")
	raw = strings.TrimSpace(raw)
	raw = rePornHaoTitleSfx.ReplaceAllString(raw, "")
	raw = strings.TrimSpace(raw)
	raw = strings.TrimRight(raw, " .-_/|~…")
	return strings.TrimSpace(raw)
}

func init() {
	Register(&PornHaoExtractor{})
}
