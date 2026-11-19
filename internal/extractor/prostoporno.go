package extractor

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"pmv-dl/internal/common"
)

var (
	rePPVideoPath   = regexp.MustCompile(`^/videos/([0-9]+)/?$`)
	rePPEmbedPath   = regexp.MustCompile(`^/embed/([0-9]+)/?$`)
	rePPVideoLink   = regexp.MustCompile(`href=["'](?:https?://(?:www\.)?prostoporno\.dev)?/videos/([0-9]+)/?["']`)
	rePPAlbumLink   = regexp.MustCompile(`href=["'](?:https?://(?:www\.)?prostoporno\.dev)?/albums/([a-zA-Z0-9_.-]+)/?["']`)
	rePPFlashvars   = regexp.MustCompile(`(?s)(?:var\s+)?flashvars\s*=\s*\{([^}]+)\}`)
	rePPProp        = regexp.MustCompile(`(?i)["']?([a-zA-Z0-9_]+)["']?\s*:\s*['"]([^'"]*)['"]`)
	rePPFullImg     = regexp.MustCompile(`(?is)<a[^>]+class=["'][^"']*fullimg[^"']*["'][^>]+href=["'](https?://[^"'\s]+)["']`)
	rePPItmImg      = regexp.MustCompile(`(?is)<div[^>]+class=["'][^"']*itm[^"']*["'][^>]*>.*?<a[^>]+href=["'](https?://[^"'\s]+)["']`)
	rePPAnyAlbumImg = regexp.MustCompile(`(?is)<a[^>]+href=["'](https?://[^"'\s]+/contents/albums/main/[^"'\s]+\.jpg)["']`)
	rePPEmbedIframe = regexp.MustCompile(`(?is)<iframe[^>]+src=["'](https?://[^"']*(?:ppembed\.com|prostoporno\.dev)/embed/[^"']+)["']`)
	rePPOGVideo     = regexp.MustCompile(`(?is)<meta[^>]+property=["']og:video(?::url)?["'][^>]+content=["'](https?://[^"']+)["']`)
	rePPSourceVideo = regexp.MustCompile(`(?is)<source[^>]+src=["'](https?://[^"']+\.mp4[^"']*)["']`)
	rePPH1Name      = regexp.MustCompile(`(?is)<h1[^>]+itemprop=["']name["'][^>]*>(.*?)</h1>`)
	rePPH1Any       = regexp.MustCompile(`(?is)<h1[^>]*>(.*?)</h1>`)
	rePPOGTitle     = regexp.MustCompile(`(?is)<meta[^>]+property=["']og:title["'][^>]+content=["'](.*?)["']\s*(?:/?>|[a-zA-Z_-]+=)`)
	rePPTitleTag    = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	rePPHTMLTags    = regexp.MustCompile(`<[^>]*>`)
	rePPTitlePrefix = regexp.MustCompile(`(?i)^\s*(?:["'])?\s*(?:Порно\s+онлайн\s*[-—|:]\s*|Порно\s+фото\s*[-—|:]\s*|Порно\s*[-—|:]\s*|Смотрите\s+порно\s+видео\s+(?:по\s+)?тегу\s+|Смотрите\s+порно\s+видео\s+)`)
	rePPTitleSuffix = regexp.MustCompile(`(?i)\s*(?:[-—|:]\s*)?(?:ProstoPorno|ppembed|альбом\s+от\s+пользователя\s+[^"']+|смотреть\s+онлайн\s+бесплатно[^"']*|на\s+Prostoporno).*$`)
)

type prostoPornoCandidate struct {
	height int
	url    string
}

type ProstoPornoExtractor struct{}

func (p *ProstoPornoExtractor) Name() string {
	return "prostoporno"
}

func (p *ProstoPornoExtractor) Description() string {
	return "ProstoPorno and ppembed video, album gallery, and collection extractor"
}

func (p *ProstoPornoExtractor) Capabilities() Capability {
	return CapVideo | CapPlaylist | CapGallery | CapImage
}

func (p *ProstoPornoExtractor) Match(u *url.URL) bool {
	host := strings.ToLower(u.Host)
	if colon := strings.Index(host, ":"); colon != -1 {
		host = host[:colon]
	}

	matchesDomain := func(d string) bool {
		return host == d || strings.HasSuffix(host, "."+d)
	}

	return matchesDomain("prostoporno.dev") || matchesDomain("ppembed.com")
}

func (p *ProstoPornoExtractor) Extract(ctx context.Context, client *http.Client, targetURL *url.URL, opts common.DownloadOptions) (*Result, error) {
	urlStr := targetURL.String()
	referer := targetURL.Scheme + "://" + targetURL.Host + "/"

	bodyBytes, finalURL, err := common.FetchBytesWithRetry(ctx, client, urlStr, referer, common.MaxHTMLBytes, common.DefaultTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch %s: %w", urlStr, err)
	}
	if finalURL != nil {
		targetURL = finalURL
	}

	return p.ExtractFromHTML(ctx, client, targetURL, bodyBytes, opts)
}

func (p *ProstoPornoExtractor) ExtractFromHTML(ctx context.Context, client *http.Client, targetURL *url.URL, bodyBytes []byte, opts common.DownloadOptions) (*Result, error) {
	bodyStr := string(bodyBytes)
	path := strings.ToLower(targetURL.Path)

	isVideoPath := rePPVideoPath.MatchString(path) || rePPEmbedPath.MatchString(path) || strings.Contains(targetURL.Host, "ppembed.com")
	hasFlashvars := rePPFlashvars.MatchString(bodyStr)

	// 1. Single Video Page
	if isVideoPath || hasFlashvars {
		return p.extractVideo(ctx, client, targetURL, bodyBytes, opts)
	}

	// 2. Single Album / Photo Gallery Page
	if strings.HasPrefix(path, "/albums/") && !p.isAlbumListingPath(path) {
		res, err := p.extractAlbum(targetURL, bodyStr)
		if err == nil && res != nil {
			return res, nil
		}
	}

	// 3. Album Listing Page (e.g. /albums/, /main-albums/, /albums/top-rated/)
	if p.isAlbumListingPath(path) || strings.HasPrefix(path, "/main-albums") {
		return p.extractAlbumListing(ctx, client, targetURL, bodyStr, opts)
	}

	// 4. Video Listing Page (categories, models, tags, search, best, newest, home)
	listingResult, err := p.extractVideoListing(ctx, client, targetURL, bodyStr, opts)
	if err == nil && listingResult != nil {
		return listingResult, nil
	}

	// 5. Fallback video extraction if embed indicators are present
	if strings.Contains(bodyStr, "ppembed.com") || strings.Contains(bodyStr, "kt_player") || strings.Contains(bodyStr, "video_url") {
		return p.extractVideo(ctx, client, targetURL, bodyBytes, opts)
	}

	return nil, fmt.Errorf("unable to determine ProstoPorno content type for %s", targetURL.String())
}

func (p *ProstoPornoExtractor) isAlbumListingPath(path string) bool {
	path = strings.Trim(strings.ToLower(path), "/")
	if path == "albums" || path == "main-albums" {
		return true
	}
	if strings.HasPrefix(path, "albums/") {
		sub := strings.TrimPrefix(path, "albums/")
		switch sub {
		case "top-rated", "most-popular", "private", "categories":
			return true
		}
		if strings.HasPrefix(sub, "categories/") {
			return true
		}
		if _, err := strconv.Atoi(sub); err == nil {
			return true
		}
	}
	return false
}

func (p *ProstoPornoExtractor) isAlbumListingSlug(slug string) bool {
	slug = strings.Trim(strings.ToLower(slug), "/")
	if slug == "" || slug == "categories" || slug == "top-rated" || slug == "most-popular" || slug == "private" {
		return true
	}
	if strings.HasPrefix(slug, "categories/") {
		return true
	}
	if _, err := strconv.Atoi(slug); err == nil {
		return true
	}
	return false
}

func (p *ProstoPornoExtractor) extractAlbum(targetURL *url.URL, bodyStr string) (*Result, error) {
	var imgURLs []string
	seen := make(map[string]bool)

	// Prefer fullimg class links
	for _, m := range rePPFullImg.FindAllStringSubmatch(bodyStr, -1) {
		u := m[1]
		if !seen[u] {
			seen[u] = true
			imgURLs = append(imgURLs, u)
		}
	}

	// Fallback to itm class links
	if len(imgURLs) == 0 {
		for _, m := range rePPItmImg.FindAllStringSubmatch(bodyStr, -1) {
			u := m[1]
			if !seen[u] {
				seen[u] = true
				imgURLs = append(imgURLs, u)
			}
		}
	}

	// Fallback to any /contents/albums/main/ images
	if len(imgURLs) == 0 {
		for _, m := range rePPAnyAlbumImg.FindAllStringSubmatch(bodyStr, -1) {
			u := m[1]
			if !seen[u] {
				seen[u] = true
				imgURLs = append(imgURLs, u)
			}
		}
	}

	if len(imgURLs) == 0 {
		if strings.Contains(bodyStr, "Это личный альбом") || strings.Contains(bodyStr, "is_private") {
			return nil, fmt.Errorf("this album is private and requires user authentication: %s", targetURL.String())
		}
		return nil, fmt.Errorf("no photos found in album %s", targetURL.String())
	}

	title := p.extractAlbumTitle(bodyStr, targetURL)
	referer := targetURL.Scheme + "://" + targetURL.Host + "/"

	return &Result{
		Type: ResultTypeImage,
		Image: &ImageResult{
			Title:     title,
			PageURL:   targetURL.String(),
			ImageURLs: imgURLs,
			Referer:   referer,
		},
	}, nil
}

func (p *ProstoPornoExtractor) extractVideo(ctx context.Context, client *http.Client, targetURL *url.URL, bodyBytes []byte, opts common.DownloadOptions) (*Result, error) {
	bodyStr := string(bodyBytes)
	flashMatch := rePPFlashvars.FindStringSubmatch(bodyStr)

	if len(flashMatch) < 2 {
		embedURLStr := ""
		if m := rePPEmbedIframe.FindStringSubmatch(bodyStr); len(m) > 1 {
			embedURLStr = m[1]
		} else if m := rePPOGVideo.FindStringSubmatch(bodyStr); len(m) > 1 {
			embedURLStr = m[1]
		}

		if embedURLStr != "" {
			embURL, err := url.Parse(embedURLStr)
			if err == nil {
				if embURL.Scheme == "" {
					embURL.Scheme = targetURL.Scheme
				}
				if embURL.Host == "" {
					embURL.Host = targetURL.Host
				}
				embBytes, _, fetchErr := common.FetchBytesWithRetry(ctx, client, embURL.String(), targetURL.String(), common.MaxHTMLBytes, common.DefaultTimeout)
				if fetchErr == nil {
					bodyStr = string(embBytes)
					flashMatch = rePPFlashvars.FindStringSubmatch(bodyStr)
				}
			}
		}
	}

	if len(flashMatch) < 2 {
		if m := rePPSourceVideo.FindStringSubmatch(bodyStr); len(m) > 1 {
			title := p.extractVideoTitle(bodyStr, targetURL)
			return &Result{
				Type: ResultTypeVideo,
				Video: &VideoResult{
					Title:     title,
					PageURL:   targetURL.String(),
					StreamURL: m[1],
					Referer:   targetURL.String(),
				},
			}, nil
		}
		return nil, fmt.Errorf("failed to locate flashvars or video stream in %s", targetURL.String())
	}

	flashBlock := flashMatch[1]
	props := make(map[string]string)
	for _, m := range rePPProp.FindAllStringSubmatch(flashBlock, -1) {
		props[strings.ToLower(m[1])] = m[2]
	}

	licenseCode := props["license_code"]

	var candidates []prostoPornoCandidate
	urlKeys := []string{
		"video_url",
		"video_alt_url",
		"video_alt_url1",
		"video_alt_url2",
		"video_alt_url3",
		"video_alt_url4",
		"video_alt_url5",
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
		if strings.HasPrefix(realURL, "/") {
			realURL = fmt.Sprintf("%s://%s%s", targetURL.Scheme, targetURL.Host, realURL)
		}

		textLabel := props[key+"_text"]
		h := parseProstoPornoHeight(textLabel, realURL)
		if key == "video_url" && props["video_url_hd"] == "1" && h < 720 {
			h = 720
		}
		candidates = append(candidates, prostoPornoCandidate{
			height: h,
			url:    realURL,
		})
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("no playable formats found in flashvars for %s", targetURL.String())
	}

	streamURL := selectProstoPornoQuality(candidates, opts.Quality)
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

func (p *ProstoPornoExtractor) extractVideoListing(ctx context.Context, client *http.Client, targetURL *url.URL, bodyStr string, opts common.DownloadOptions) (*Result, error) {
	matches := rePPVideoLink.FindAllStringSubmatch(bodyStr, -1)
	if len(matches) == 0 {
		return nil, nil
	}

	var entries []PlaylistEntry
	seen := make(map[string]bool)

	for _, m := range matches {
		id := m[1]
		vURL := fmt.Sprintf("https://prostoporno.dev/videos/%s/", id)
		if !seen[vURL] {
			seen[vURL] = true
			entries = append(entries, PlaylistEntry{
				Type:  ResultTypeVideo,
				Title: "prostoporno_" + id,
				URL:   vURL,
			})
		}
	}

	if len(entries) == 0 {
		return nil, nil
	}

	for i := range entries {
		id := matches[i][1]
		cardRegex := regexp.MustCompile(fmt.Sprintf(`(?s)href=["'][^"']*/videos/%s/?[^"']*["'][^>]*title=["']([^"']+)["']`, id))
		if m := cardRegex.FindStringSubmatch(bodyStr); len(m) > 1 {
			t := html.UnescapeString(m[1])
			t = strings.TrimSpace(t)
			if t != "" {
				entries[i].Title = common.SanitizeFilename(t)
			}
		}
	}

	if opts.NoPlaylist {
		firstURL, err := common.ValidateRemoteURL(entries[0].URL)
		if err != nil {
			return nil, err
		}
		return p.Extract(ctx, client, firstURL, opts)
	}

	plTitle := p.extractListingTitle(bodyStr, targetURL)

	return &Result{
		Type: ResultTypePlaylist,
		Playlist: &PlaylistResult{
			Title:   common.SanitizeFilename(plTitle),
			PageURL: targetURL.String(),
			Entries: entries,
		},
	}, nil
}

func (p *ProstoPornoExtractor) extractAlbumListing(ctx context.Context, client *http.Client, targetURL *url.URL, bodyStr string, opts common.DownloadOptions) (*Result, error) {
	matches := rePPAlbumLink.FindAllStringSubmatch(bodyStr, -1)
	if len(matches) == 0 {
		return nil, fmt.Errorf("no albums found on album listing page %s", targetURL.String())
	}

	var entries []PlaylistEntry
	seen := make(map[string]bool)

	for _, m := range matches {
		slug := m[1]
		if p.isAlbumListingSlug(slug) {
			continue
		}
		aURL := fmt.Sprintf("https://prostoporno.dev/albums/%s/", slug)
		if !seen[aURL] {
			seen[aURL] = true
			entries = append(entries, PlaylistEntry{
				Type:  ResultTypeImage,
				Title: "album_" + slug,
				URL:   aURL,
			})
		}
	}

	if len(entries) == 0 {
		return nil, fmt.Errorf("no valid album entries found on %s", targetURL.String())
	}

	if opts.NoPlaylist {
		firstURL, err := common.ValidateRemoteURL(entries[0].URL)
		if err != nil {
			return nil, err
		}
		return p.Extract(ctx, client, firstURL, opts)
	}

	plTitle := p.extractListingTitle(bodyStr, targetURL)

	return &Result{
		Type: ResultTypePlaylist,
		Playlist: &PlaylistResult{
			Title:   common.SanitizeFilename(plTitle),
			PageURL: targetURL.String(),
			Entries: entries,
		},
	}, nil
}

func (p *ProstoPornoExtractor) extractVideoTitle(bodyStr string, targetURL *url.URL) string {
	raw := ""
	if m := rePPH1Name.FindStringSubmatch(bodyStr); len(m) > 1 {
		raw = m[1]
	} else if m := rePPH1Any.FindStringSubmatch(bodyStr); len(m) > 1 {
		raw = m[1]
	} else if m := rePPOGTitle.FindStringSubmatch(bodyStr); len(m) > 1 {
		raw = m[1]
	} else if m := rePPTitleTag.FindStringSubmatch(bodyStr); len(m) > 1 {
		raw = m[1]
	}

	return p.cleanTitle(raw, "prostoporno_video")
}

func (p *ProstoPornoExtractor) extractAlbumTitle(bodyStr string, targetURL *url.URL) string {
	raw := ""
	if m := rePPH1Name.FindStringSubmatch(bodyStr); len(m) > 1 {
		raw = m[1]
	} else if m := rePPH1Any.FindStringSubmatch(bodyStr); len(m) > 1 {
		raw = m[1]
	} else if m := rePPOGTitle.FindStringSubmatch(bodyStr); len(m) > 1 {
		raw = m[1]
	} else if m := rePPTitleTag.FindStringSubmatch(bodyStr); len(m) > 1 {
		raw = m[1]
	}

	return p.cleanTitle(raw, "prostoporno_album")
}

func (p *ProstoPornoExtractor) extractListingTitle(bodyStr string, targetURL *url.URL) string {
	raw := ""
	if m := rePPH1Any.FindStringSubmatch(bodyStr); len(m) > 1 {
		raw = m[1]
	} else if m := rePPOGTitle.FindStringSubmatch(bodyStr); len(m) > 1 {
		raw = m[1]
	} else if m := rePPTitleTag.FindStringSubmatch(bodyStr); len(m) > 1 {
		raw = m[1]
	}

	return p.cleanTitle(raw, "prostoporno_collection")
}

func (p *ProstoPornoExtractor) cleanTitle(raw string, fallback string) string {
	raw = rePPHTMLTags.ReplaceAllString(raw, "")
	raw = html.UnescapeString(raw)
	raw = strings.TrimSpace(raw)

	raw = rePPTitlePrefix.ReplaceAllString(raw, "")
	raw = rePPTitleSuffix.ReplaceAllString(raw, "")
	raw = strings.Trim(raw, "\"' ")

	if raw == "" {
		raw = fallback
	}

	return common.SanitizeFilename(raw)
}

func parseProstoPornoHeight(label string, streamURL string) int {
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
	if strings.Contains(urlLower, "_h.mp4") || strings.Contains(urlLower, "_1080p") || strings.Contains(urlLower, "_720p") {
		return 720
	}
	if strings.Contains(urlLower, ".mp4") {
		return 360
	}
	return 0
}

func selectProstoPornoQuality(candidates []prostoPornoCandidate, targetQuality string) string {
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

	targetH := parseProstoPornoHeight(q, "")
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

	var bestMatch *prostoPornoCandidate
	for i := range candidates {
		c := &candidates[i]
		if c.height == targetH {
			return c.url
		}
		if c.height <= targetH {
			if bestMatch == nil || c.height > bestMatch.height {
				bestMatch = c
			}
		}
	}

	if bestMatch != nil {
		return bestMatch.url
	}

	return candidates[0].url
}

func init() {
	Register(&ProstoPornoExtractor{})
}
