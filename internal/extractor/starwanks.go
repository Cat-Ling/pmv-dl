package extractor

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"pmv-dl/internal/common"
)

var (
	reSWVideoQuery    = regexp.MustCompile(`^/video/?$`)
	reSWVideoPathID   = regexp.MustCompile(`^/(?:video|v)/([0-9]+)/?$`)
	reSWVideoLink     = regexp.MustCompile(`(?is)href=["'](?:https?://(?:www\.)?starwanks\.com)?/video\?id=([0-9]+)["']`)
	reSWStreamSource  = regexp.MustCompile(`(?is)<source[^>]+src=["']([^"']*/stream/[^"']+)["']`)
	reSWAnyStream     = regexp.MustCompile(`(?is)["']([^"']*/stream/v2\.[^"']+)["']`)
	reSWDirectM3u8    = regexp.MustCompile(`(?is)(https?://[^"'\s<>]+\.m3u8(?:\?[^"'\s<>]*)?)`)
	reSWH1VideoTitle  = regexp.MustCompile(`(?is)<h1[^>]*class=["'][^"']*video-title[^"']*["'][^>]*>(.*?)</h1>`)
	reSWH1Any         = regexp.MustCompile(`(?is)<h1[^>]*>(.*?)</h1>`)
	reSWOGTitle       = regexp.MustCompile(`(?is)<meta[^>]+property=["']og:title["'][^>]+content=["'](.*?)["']\s*(?:/?>|[a-zA-Z_-]+=)`)
	reSWTitleTag      = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	reSWHTMLTags      = regexp.MustCompile(`<[^>]*>`)
	reSWTitlePrefix   = regexp.MustCompile(`(?i)^\s*Video:\s*`)
	reSWTitleDuration = regexp.MustCompile(`(?i)\s*\(\d+\s*(?:min|sec|h)\)\s*`)
	reSWTitleSuffix   = regexp.MustCompile(`(?i)\s*(?:[-—|:]\s*)?(?:StarWanks|Videos?)\s*$`)
)

type StarWanksExtractor struct{}

func (s *StarWanksExtractor) Name() string {
	return "starwanks"
}

func (s *StarWanksExtractor) Description() string {
	return "StarWanks video and category/pornstar/feed collection extractor"
}

func (s *StarWanksExtractor) Capabilities() Capability {
	return CapVideo | CapPlaylist
}

func (s *StarWanksExtractor) Match(u *url.URL) bool {
	host := strings.ToLower(u.Host)
	if colon := strings.Index(host, ":"); colon != -1 {
		host = host[:colon]
	}

	return host == "starwanks.com" || host == "www.starwanks.com" || strings.HasSuffix(host, ".starwanks.com")
}

func (s *StarWanksExtractor) normalizeURL(targetURL *url.URL) *url.URL {
	u := *targetURL
	path := strings.ToLower(u.Path)

	if m := reSWVideoPathID.FindStringSubmatch(path); len(m) > 1 {
		u.Path = "/video"
		q := u.Query()
		q.Set("id", m[1])
		u.RawQuery = q.Encode()
	}

	return &u
}

func (s *StarWanksExtractor) Extract(ctx context.Context, client *http.Client, targetURL *url.URL, opts common.DownloadOptions) (*Result, error) {
	normURL := s.normalizeURL(targetURL)
	urlStr := normURL.String()
	referer := normURL.Scheme + "://" + normURL.Host + "/"

	bodyBytes, finalURL, err := common.FetchBytesWithRetry(ctx, client, urlStr, referer, common.MaxHTMLBytes, common.DefaultTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch %s: %w", urlStr, err)
	}
	if finalURL != nil {
		normURL = finalURL
	}

	return s.ExtractFromHTML(ctx, client, normURL, bodyBytes, opts)
}

func (s *StarWanksExtractor) ExtractFromHTML(ctx context.Context, client *http.Client, targetURL *url.URL, bodyBytes []byte, opts common.DownloadOptions) (*Result, error) {
	bodyStr := string(bodyBytes)
	path := strings.ToLower(targetURL.Path)

	isVideoPage := (reSWVideoQuery.MatchString(path) && targetURL.Query().Get("id") != "") ||
		reSWStreamSource.MatchString(bodyStr) ||
		reSWAnyStream.MatchString(bodyStr)

	if isVideoPage {
		return s.extractVideo(ctx, client, targetURL, bodyStr)
	}

	// Listing / feed / category / search page
	return s.extractListing(ctx, client, targetURL, bodyStr, opts)
}

func (s *StarWanksExtractor) extractVideo(ctx context.Context, client *http.Client, targetURL *url.URL, bodyStr string) (*Result, error) {
	pageURL := targetURL.String()
	streamPath := ""

	if m := reSWStreamSource.FindStringSubmatch(bodyStr); len(m) > 1 {
		streamPath = m[1]
	} else if m := reSWAnyStream.FindStringSubmatch(bodyStr); len(m) > 1 {
		streamPath = m[1]
	}

	finalStreamURL := ""

	if streamPath != "" {
		streamEndpoint := streamPath
		if strings.HasPrefix(streamPath, "/") {
			streamEndpoint = fmt.Sprintf("%s://%s%s", targetURL.Scheme, targetURL.Host, streamPath)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, streamEndpoint, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to build stream request %s: %w", streamEndpoint, err)
		}
		req.Header.Set("User-Agent", common.UserAgent)
		req.Header.Set("Referer", pageURL)

		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve stream endpoint %s: %w", streamEndpoint, err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusFound {
			return nil, fmt.Errorf("HTTP %s when resolving stream endpoint %s", resp.Status, streamEndpoint)
		}

		finalStreamURL = resp.Request.URL.String()
	} else if m := reSWDirectM3u8.FindStringSubmatch(bodyStr); len(m) > 1 {
		finalStreamURL = m[1]
	}

	if finalStreamURL == "" {
		return nil, fmt.Errorf("could not locate stream URL in %s", pageURL)
	}

	validStream, err := common.ValidateRemoteURL(finalStreamURL)
	if err != nil {
		return nil, fmt.Errorf("invalid stream URL %q: %w", finalStreamURL, err)
	}

	title := s.extractVideoTitle(bodyStr, targetURL)

	return &Result{
		Type: ResultTypeVideo,
		Video: &VideoResult{
			Title:     title,
			PageURL:   pageURL,
			StreamURL: validStream.String(),
			Referer:   pageURL,
		},
	}, nil
}

func (s *StarWanksExtractor) extractListing(ctx context.Context, client *http.Client, targetURL *url.URL, bodyStr string, opts common.DownloadOptions) (*Result, error) {
	matches := reSWVideoLink.FindAllStringSubmatch(bodyStr, -1)
	if len(matches) == 0 {
		return nil, fmt.Errorf("no videos found on %s", targetURL.String())
	}

	var entries []PlaylistEntry
	seen := make(map[string]bool)

	for _, m := range matches {
		id := m[1]
		vURL := fmt.Sprintf("https://starwanks.com/video?id=%s", id)
		if !seen[vURL] {
			seen[vURL] = true
			title := "starwanks_" + id

			// Try to extract title from h3 link or card img alt
			h3Regex := regexp.MustCompile(fmt.Sprintf(`(?is)<h3[^>]*>\s*<a[^>]+href=["'][^"']*/video\?id=%s["'][^>]*>(.*?)</a>`, id))
			if tm := h3Regex.FindStringSubmatch(bodyStr); len(tm) > 1 {
				clean := s.cleanTitle(tm[1], "")
				if clean != "" {
					title = clean
				}
			}
			if title == "starwanks_"+id {
				altRegex := regexp.MustCompile(fmt.Sprintf(`(?is)<a[^>]+href=["'][^"']*/video\?id=%s["'][^>]*>.*?<img[^>]+alt=["']([^"']+)["']`, id))
				if tm := altRegex.FindStringSubmatch(bodyStr); len(tm) > 1 {
					clean := s.cleanTitle(tm[1], "")
					if clean != "" {
						title = clean
					}
				}
			}

			entries = append(entries, PlaylistEntry{
				Type:  ResultTypeVideo,
				Title: title,
				URL:   vURL,
			})
		}
	}

	if len(entries) == 0 {
		return nil, fmt.Errorf("no valid video entries found on %s", targetURL.String())
	}

	if opts.NoPlaylist {
		firstURL, err := common.ValidateRemoteURL(entries[0].URL)
		if err != nil {
			return nil, err
		}
		return s.Extract(ctx, client, firstURL, opts)
	}

	plTitle := s.extractListingTitle(bodyStr, targetURL)

	return &Result{
		Type: ResultTypePlaylist,
		Playlist: &PlaylistResult{
			Title:   common.SanitizeFilename(plTitle),
			PageURL: targetURL.String(),
			Entries: entries,
		},
	}, nil
}

func (s *StarWanksExtractor) extractVideoTitle(bodyStr string, targetURL *url.URL) string {
	raw := ""
	if m := reSWH1VideoTitle.FindStringSubmatch(bodyStr); len(m) > 1 {
		raw = m[1]
	} else if m := reSWH1Any.FindStringSubmatch(bodyStr); len(m) > 1 {
		raw = m[1]
	} else if m := reSWOGTitle.FindStringSubmatch(bodyStr); len(m) > 1 {
		raw = m[1]
	} else if m := reSWTitleTag.FindStringSubmatch(bodyStr); len(m) > 1 {
		raw = m[1]
	}

	id := targetURL.Query().Get("id")
	fallback := "starwanks_video"
	if id != "" {
		fallback = "starwanks_" + id
	}

	return s.cleanTitle(raw, fallback)
}

func (s *StarWanksExtractor) extractListingTitle(bodyStr string, targetURL *url.URL) string {
	raw := ""
	if m := reSWH1Any.FindStringSubmatch(bodyStr); len(m) > 1 {
		raw = m[1]
	} else if m := reSWOGTitle.FindStringSubmatch(bodyStr); len(m) > 1 {
		raw = m[1]
	} else if m := reSWTitleTag.FindStringSubmatch(bodyStr); len(m) > 1 {
		raw = m[1]
	}

	return s.cleanTitle(raw, "starwanks_collection")
}

func (s *StarWanksExtractor) cleanTitle(raw string, fallback string) string {
	raw = reSWHTMLTags.ReplaceAllString(raw, "")
	raw = html.UnescapeString(raw)
	raw = strings.TrimSpace(raw)

	raw = reSWTitlePrefix.ReplaceAllString(raw, "")
	raw = reSWTitleDuration.ReplaceAllString(raw, "")
	raw = reSWTitleSuffix.ReplaceAllString(raw, "")
	raw = strings.Trim(raw, "\"' ")

	if raw == "" {
		raw = fallback
	}

	return common.SanitizeFilename(raw)
}

func init() {
	Register(&StarWanksExtractor{})
}
