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
	reGoonMasterM3u8 = regexp.MustCompile(`https?://[^"'\s\\<>]*(?:master|playback)\.m3u8(?:\?[^"'\s\\<>]*)?`)
	reGoonAnyM3u8    = regexp.MustCompile(`https?://[^"'\s\\<>]*\.m3u8(?:\?[^"'\s\\<>]*)?`)
	reGoonH1Class    = regexp.MustCompile(`(?is)<h1[^>]*text-xl[^>]*>(.*?)</h1>`)
	reGoonH1Any      = regexp.MustCompile(`(?is)<h1[^>]*>(.*?)</h1>`)
	reGoonTitle      = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	reGoonHTMLTags   = regexp.MustCompile(`<[^>]*>`)
	reGoonSiteSuffix = regexp.MustCompile(`(?i)\s*[-|]\s*(?:GoonChan Gallery|GoonChan Shorts|GoonChan|PMVClub|PMVHaven)\s*$`)
	reGoonSlug       = regexp.MustCompile(`/(?:video|shorts|gallery|image)/([^/?#]+?)(?:[/?#]|$)`)
	reGoonJSRedirect = regexp.MustCompile(`(?i)<script[^>]*>\s*window\.location\.(?:replace|href)\s*=\s*["']([^"']+)["']`)
)

type PMVClubExtractor struct{}

func (p *PMVClubExtractor) Name() string {
	return "pmvclub"
}

func (p *PMVClubExtractor) Description() string {
	return "PMVClub / GoonChan video, shorts, gallery, and image extractor"
}

func (p *PMVClubExtractor) Capabilities() Capability {
	return CapVideo | CapShort | CapGallery | CapImage
}

func (p *PMVClubExtractor) Match(u *url.URL) bool {
	host := strings.ToLower(u.Host)
	return host == "pmvclub.com" || host == "www.pmvclub.com" ||
		host == "goonchan.org" || host == "www.goonchan.org"
}

func (p *PMVClubExtractor) normalizeGoonMedia(s string) string {
	return strings.ReplaceAll(s, "https://media.goonchan.org/", "https://test-media.goonchan.org/")
}

func (p *PMVClubExtractor) resolveTargetURL(ctx context.Context, client *http.Client, targetURL *url.URL) (*url.URL, error) {
	host := strings.ToLower(targetURL.Host)
	if host != "pmvclub.com" && host != "www.pmvclub.com" {
		return targetURL, nil
	}

	path := targetURL.Path
	switch {
	case strings.HasPrefix(path, "/video/"),
		strings.HasPrefix(path, "/shorts/"),
		strings.HasPrefix(path, "/gallery/"),
		strings.HasPrefix(path, "/image/"):
		uCopy := *targetURL
		uCopy.Scheme = "https"
		uCopy.Host = "goonchan.org"
		return &uCopy, nil
	default:
		// Attempt to fetch page and detect JS redirect
		bodyBytes, _, err := common.FetchBytesWithRetry(ctx, client, targetURL.String(), "", common.MaxHTMLBytes, common.DefaultTimeout)
		if err == nil {
			bodyStr := string(bodyBytes)
			if m := reGoonJSRedirect.FindStringSubmatch(bodyStr); len(m) > 1 {
				redirURL, parseErr := url.Parse(m[1])
				if parseErr == nil {
					trimmedPath := strings.Trim(path, "/")
					if trimmedPath != "" && (redirURL.Path == "" || redirURL.Path == "/") {
						candURL := fmt.Sprintf("%s://%s/video/%s", redirURL.Scheme, redirURL.Host, trimmedPath)
						candReq, _ := http.NewRequestWithContext(ctx, http.MethodHead, candURL, nil)
						candReq.Header.Set("User-Agent", common.UserAgent)
						if candResp, err := client.Do(candReq); err == nil {
							candResp.Body.Close()
							if candResp.StatusCode == http.StatusOK {
								return url.Parse(candURL)
							}
						}
					}
					return redirURL, nil
				}
			}
		}

		uCopy := *targetURL
		uCopy.Scheme = "https"
		uCopy.Host = "goonchan.org"
		return &uCopy, nil
	}
}

func (p *PMVClubExtractor) Extract(ctx context.Context, client *http.Client, rawURL *url.URL, opts common.DownloadOptions) (*Result, error) {
	targetURL, err := p.resolveTargetURL(ctx, client, rawURL)
	if err != nil {
		return nil, err
	}

	path := strings.ToLower(targetURL.Path)

	// Gallery extraction
	isGalleryPath := strings.HasPrefix(path, "/gallery/") || strings.HasPrefix(path, "/galleries/")
	if isGalleryPath && !opts.NoPlaylist {
		return p.extractGallery(ctx, client, targetURL, opts)
	}

	// Single image post extraction
	isImagePath := strings.HasPrefix(path, "/image/") || strings.HasPrefix(path, "/images/")
	if isImagePath {
		return p.extractImagePost(ctx, client, targetURL)
	}

	// Single video or short extraction
	return p.extractVideo(ctx, client, targetURL)
}

func (p *PMVClubExtractor) extractGallery(ctx context.Context, client *http.Client, pageURL *url.URL, opts common.DownloadOptions) (*Result, error) {
	bodyBytes, finalURL, err := common.FetchBytesWithRetry(ctx, client, pageURL.String(), "", common.MaxHTMLBytes, common.DefaultTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch gallery page: %w", err)
	}
	if finalURL != nil {
		pageURL = finalURL
	}

	bodyStr := string(bodyBytes)
	normalized := p.normalizeGoonMedia(common.NormalizeString(bodyStr))

	galleryTitle := ""
	reGalleryTitle := regexp.MustCompile(`"gallery":\s*\{[^}]*"title":\s*"([^"]+)"`)
	if tm := reGalleryTitle.FindStringSubmatch(normalized); len(tm) > 1 {
		galleryTitle = html.UnescapeString(tm[1])
	}
	if galleryTitle == "" {
		if m := reGoonH1Any.FindStringSubmatch(bodyStr); len(m) > 1 {
			galleryTitle = html.UnescapeString(strings.TrimSpace(reGoonHTMLTags.ReplaceAllString(m[1], "")))
		}
	}
	if galleryTitle == "" {
		if m := reGoonTitle.FindStringSubmatch(bodyStr); len(m) > 1 {
			t := html.UnescapeString(m[1])
			t = strings.TrimSpace(reGoonSiteSuffix.ReplaceAllString(t, ""))
			galleryTitle = t
		}
	}
	if galleryTitle == "" {
		galleryTitle = "Gallery"
	}

	var entries []PlaylistEntry
	seen := make(map[string]bool)

	// Extract videos from initialData
	reVideoObj := regexp.MustCompile(`\{"_id":"[^"]+","type":"video","title":"([^"]+)","slug":"([^"]+)"`)
	for _, vm := range reVideoObj.FindAllStringSubmatch(normalized, -1) {
		vTitle := html.UnescapeString(vm[1])
		vSlug := vm[2]
		vURL := fmt.Sprintf("https://goonchan.org/video/%s", vSlug)
		if !seen[vURL] {
			seen[vURL] = true
			entries = append(entries, PlaylistEntry{
				Type:  ResultTypeVideo,
				Title: vTitle,
				URL:   vURL,
			})
		}
	}

	// Extract images from initialData
	reImageObj := regexp.MustCompile(`\{"_id":"[^"]+","type":"image","title":"([^"]+)","slug":"([^"]+)",(?:[^{}]|"uploader":\{[^{}]*\})*"imageUrls":\[([^\]]+)\]`)
	for _, im := range reImageObj.FindAllStringSubmatch(normalized, -1) {
		iTitle := html.UnescapeString(im[1])
		iSlug := im[2]
		rawURLs := im[3]
		var imgURLs []string
		for _, u := range strings.Split(rawURLs, ",") {
			u = strings.Trim(strings.TrimSpace(u), `"`)
			u = p.normalizeGoonMedia(u)
			if strings.HasPrefix(u, "http") {
				imgURLs = append(imgURLs, u)
			}
		}
		iURL := fmt.Sprintf("https://goonchan.org/image/%s", iSlug)
		if !seen[iURL] {
			seen[iURL] = true
			entries = append(entries, PlaylistEntry{
				Type:      ResultTypeImage,
				Title:     iTitle,
				URL:       iURL,
				ImageURLs: imgURLs,
			})
		}
	}

	// Fallback to HTML link parsing if initialData didn't yield entries
	if len(entries) == 0 {
		reLink := regexp.MustCompile(`href="(/(?:video|shorts|image)/([a-zA-Z0-9_-]+)(?:\?[^"]*)?)"(?:[^>]*>([^<]*))?`)
		for _, lm := range reLink.FindAllStringSubmatch(bodyStr, -1) {
			itemPath := lm[1]
			itemSlug := lm[2]
			itemTitle := strings.TrimSpace(lm[3])
			if itemTitle == "" {
				itemTitle = itemSlug
			}
			itemType := ResultTypeVideo
			if strings.HasPrefix(itemPath, "/image/") {
				itemType = ResultTypeImage
			}
			fullURL := fmt.Sprintf("https://goonchan.org%s", itemPath)
			if !seen[fullURL] {
				seen[fullURL] = true
				entries = append(entries, PlaylistEntry{
					Type:  itemType,
					Title: html.UnescapeString(itemTitle),
					URL:   fullURL,
				})
			}
		}
	}

	if len(entries) == 0 {
		return nil, fmt.Errorf("no videos or images found in gallery %s", pageURL.String())
	}

	return &Result{
		Type: ResultTypePlaylist,
		Playlist: &PlaylistResult{
			Title:   common.SanitizeFilename(galleryTitle),
			PageURL: pageURL.String(),
			Entries: entries,
		},
	}, nil
}

func (p *PMVClubExtractor) extractImagePost(ctx context.Context, client *http.Client, pageURL *url.URL) (*Result, error) {
	bodyBytes, finalURL, err := common.FetchBytesWithRetry(ctx, client, pageURL.String(), "", common.MaxHTMLBytes, common.DefaultTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch image page: %w", err)
	}
	if finalURL != nil {
		pageURL = finalURL
	}

	bodyStr := string(bodyBytes)
	normalized := p.normalizeGoonMedia(common.NormalizeString(bodyStr))

	postTitle := ""
	if m := reGoonH1Any.FindStringSubmatch(bodyStr); len(m) > 1 {
		postTitle = html.UnescapeString(strings.TrimSpace(reGoonHTMLTags.ReplaceAllString(m[1], "")))
	}
	if postTitle == "" {
		if m := reGoonTitle.FindStringSubmatch(bodyStr); len(m) > 1 {
			t := html.UnescapeString(m[1])
			t = strings.TrimSpace(reGoonSiteSuffix.ReplaceAllString(t, ""))
			postTitle = t
		}
	}
	if postTitle == "" {
		postTitle = filepath.Base(pageURL.Path)
	}

	var imgURLs []string
	reImgUrls := regexp.MustCompile(`\\?"imageUrls\\?":\s*\[([^\]]+)\]`)
	if m := reImgUrls.FindStringSubmatch(normalized); len(m) > 1 {
		for _, raw := range strings.Split(m[1], ",") {
			clean := strings.Trim(strings.TrimSpace(raw), `\"' `)
			clean = p.normalizeGoonMedia(clean)
			if strings.HasPrefix(clean, "http") {
				imgURLs = append(imgURLs, clean)
			}
		}
	}

	// Fallback: look for direct media URLs matching the slug in the HTML
	if len(imgURLs) == 0 {
		slug := filepath.Base(pageURL.Path)
		pattern := fmt.Sprintf(`https?://[^"'\s\\<>]+/images/%s/[0-9]+\.(?:jpg|jpeg|png|webp|gif)`, regexp.QuoteMeta(slug))
		reDirect := regexp.MustCompile(pattern)
		seen := make(map[string]bool)
		for _, m := range reDirect.FindAllString(normalized, -1) {
			m = p.normalizeGoonMedia(m)
			if !seen[m] {
				seen[m] = true
				imgURLs = append(imgURLs, m)
			}
		}
	}

	if len(imgURLs) == 0 {
		return nil, fmt.Errorf("no image URLs found on page %s", pageURL.String())
	}

	return &Result{
		Type: ResultTypeImage,
		Image: &ImageResult{
			Title:     common.SanitizeFilename(postTitle),
			PageURL:   pageURL.String(),
			ImageURLs: imgURLs,
			Referer:   pageURL.String(),
		},
	}, nil
}

func (p *PMVClubExtractor) extractVideo(ctx context.Context, client *http.Client, pageURL *url.URL) (*Result, error) {
	urlStr := pageURL.String()
	bodyBytes, finalURL, err := common.FetchBytesWithRetry(ctx, client, urlStr, "", common.MaxHTMLBytes, common.DefaultTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch page: %w", err)
	}
	if finalURL != nil {
		pageURL = finalURL
		urlStr = pageURL.String()
	}

	bodyStr := string(bodyBytes)
	normalized := p.normalizeGoonMedia(common.NormalizeString(bodyStr))

	m3u8Raw := reGoonMasterM3u8.FindString(normalized)
	if m3u8Raw == "" {
		m3u8Raw = reGoonAnyM3u8.FindString(normalized)
	}
	if m3u8Raw == "" {
		return nil, fmt.Errorf("could not find .m3u8 stream URL in page %s", urlStr)
	}

	m3u8URL, err := common.ValidateRemoteURL(m3u8Raw)
	if err != nil {
		return nil, fmt.Errorf("invalid m3u8 URL %q: %w", m3u8Raw, err)
	}

	title := p.extractVideoTitle(bodyStr, urlStr)

	return &Result{
		Type: ResultTypeVideo,
		Video: &VideoResult{
			Title:     title,
			PageURL:   urlStr,
			StreamURL: m3u8URL.String(),
			Referer:   urlStr,
		},
	}, nil
}

func (p *PMVClubExtractor) extractVideoTitle(bodyStr, pageURL string) string {
	title := ""
	if h1Match := reGoonH1Class.FindStringSubmatch(bodyStr); len(h1Match) > 1 {
		title = h1Match[1]
	} else if h1Match := reGoonH1Any.FindStringSubmatch(bodyStr); len(h1Match) > 1 {
		title = h1Match[1]
	} else if titleMatch := reGoonTitle.FindStringSubmatch(bodyStr); len(titleMatch) > 1 {
		title = titleMatch[1]
	}

	if title != "" {
		title = reGoonHTMLTags.ReplaceAllString(title, "")
		title = html.UnescapeString(title)
		title = strings.TrimSpace(title)
		for {
			cleaned := reGoonSiteSuffix.ReplaceAllString(title, "")
			if cleaned == title {
				break
			}
			title = strings.TrimSpace(cleaned)
		}
		if strings.EqualFold(title, "goonchan") || strings.EqualFold(title, "pmvclub") || strings.EqualFold(title, "pmvhaven") {
			title = ""
		}
	}

	if title == "" {
		if slugMatch := reGoonSlug.FindStringSubmatch(pageURL); len(slugMatch) > 1 {
			if unescaped, err := url.PathUnescape(slugMatch[1]); err == nil {
				title = unescaped
			} else {
				title = slugMatch[1]
			}
		} else if u, err := url.Parse(pageURL); err == nil {
			base := filepath.Base(u.Path)
			if len(base) > 5 && strings.EqualFold(base[len(base)-5:], ".m3u8") {
				base = base[:len(base)-5]
			}
			if unescaped, err := url.PathUnescape(base); err == nil {
				base = unescaped
			}
			if base != "" && base != "." && base != "/" && !strings.EqualFold(base, "master") && !strings.EqualFold(base, "index") && !strings.EqualFold(base, "playback") {
				title = base
			}
		}
	}

	if title == "" {
		title = "video"
	}

	return common.SanitizeFilename(title)
}

func init() {
	Register(&PMVClubExtractor{})
}
