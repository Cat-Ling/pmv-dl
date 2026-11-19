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
	"strings"

	"pmv-dl/internal/common"
)

var (
	rePMVMasterM3u8 = regexp.MustCompile(`https?://[^"'\s\\<>]*master\.m3u8(?:\?[^"'\s\\<>]*)?`)
	rePMVAnyM3u8    = regexp.MustCompile(`https?://[^"'\s\\<>]*\.m3u8(?:\?[^"'\s\\<>]*)?`)
	rePMVH1Class    = regexp.MustCompile(`(?is)<h1[^>]*text-xl[^>]*>(.*?)</h1>`)
	rePMVH1Any      = regexp.MustCompile(`(?is)<h1[^>]*>(.*?)</h1>`)
	rePMVTitle      = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	rePMVHTMLTags   = regexp.MustCompile(`<[^>]*>`)
	rePMVSiteSuffix = regexp.MustCompile(`(?i)\s*[-|]\s*(?:PMVHaven|Playlist)\s*$`)
	rePMVSlug       = regexp.MustCompile(`/video/([^/?#]+?)(?:_[0-9a-fA-F]{24})?(?:[/?#]|$)`)
	rePMVVideoLink  = regexp.MustCompile(`/video/([a-zA-Z0-9_-]+(?:_[0-9a-fA-F]{24})?)`)
	rePMVNuxtData   = regexp.MustCompile(`(?s)<script[^>]*id="__NUXT_DATA__"[^>]*>(.*?)</script>`)
)

type PMVHavenExtractor struct{}

func (p *PMVHavenExtractor) Name() string {
	return "pmvhaven"
}

func (p *PMVHavenExtractor) Description() string {
	return "PMVHaven video and playlist extractor"
}

func (p *PMVHavenExtractor) Capabilities() Capability {
	return CapVideo | CapPlaylist
}

func (p *PMVHavenExtractor) Match(u *url.URL) bool {
	host := strings.ToLower(u.Host)
	return host == "pmvhaven.com" || host == "www.pmvhaven.com"
}

func (p *PMVHavenExtractor) Extract(ctx context.Context, client *http.Client, targetURL *url.URL, opts common.DownloadOptions) (*Result, error) {
	path := strings.ToLower(targetURL.Path)
	urlStr := targetURL.String()

	// Check if this is a playlist URL
	isPlaylistPath := strings.HasPrefix(path, "/playlists/") || strings.HasPrefix(path, "/playlist/")
	if isPlaylistPath {
		if opts.NoPlaylist {
			// User explicitly wants only the single video from the playlist URL
			res, err := p.extractVideo(ctx, client, targetURL, urlStr)
			if err == nil && res != nil && res.Video != nil {
				return res, nil
			}

			// Fall back to extracting playlist and taking the 1st entry
			plRes, err := p.extractPlaylist(ctx, client, targetURL, opts)
			if err != nil {
				return nil, err
			}
			if plRes.Playlist != nil && len(plRes.Playlist.Entries) > 0 {
				first := plRes.Playlist.Entries[0]
				if first.StreamURL != "" {
					return &Result{
						Type: ResultTypeVideo,
						Video: &VideoResult{
							Title:     first.Title,
							PageURL:   first.URL,
							StreamURL: first.StreamURL,
							Referer:   urlStr,
						},
					}, nil
				}
				firstURL, err := common.ValidateRemoteURL(first.URL)
				if err == nil {
					return p.extractVideo(ctx, client, firstURL, first.URL)
				}
			}
			return nil, fmt.Errorf("no videos found in playlist %s", urlStr)
		}
		return p.extractPlaylist(ctx, client, targetURL, opts)
	}

	return p.extractVideo(ctx, client, targetURL, urlStr)
}

func (p *PMVHavenExtractor) extractPlaylist(ctx context.Context, client *http.Client, targetURL *url.URL, opts common.DownloadOptions) (*Result, error) {
	bodyBytes, finalURL, err := common.FetchBytesWithRetry(ctx, client, targetURL.String(), "", common.MaxHTMLBytes, common.DefaultTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch playlist page: %w", err)
	}
	if finalURL != nil {
		targetURL = finalURL
	}

	bodyStr := string(bodyBytes)
	normalized := common.NormalizeString(bodyStr)

	playlistTitle := ""
	var entries []PlaylistEntry
	seen := make(map[string]bool)

	// Try extracting via Nuxt 3 data payload
	if m := rePMVNuxtData.FindStringSubmatch(bodyStr); len(m) > 1 {
		var data []any
		if err := json.Unmarshal([]byte(m[1]), &data); err == nil {
			for _, item := range data {
				itemMap, ok := item.(map[string]any)
				if !ok {
					continue
				}
				if vDetailsRaw, exists := itemMap["videoDetails"]; exists {
					if nameRaw, hasName := itemMap["name"]; hasName {
						if n, ok := resolveNuxtVal(nameRaw, data).(string); ok && strings.TrimSpace(n) != "" {
							playlistTitle = strings.TrimSpace(n)
						}
					}
					vDetailsResolved := resolveNuxtVal(vDetailsRaw, data)
					if vList, ok := vDetailsResolved.([]any); ok {
						for _, vRef := range vList {
							vObjRaw := resolveNuxtVal(vRef, data)
							vObj, ok := vObjRaw.(map[string]any)
							if !ok {
								continue
							}
							id, _ := resolveNuxtVal(vObj["_id"], data).(string)
							title, _ := resolveNuxtVal(vObj["title"], data).(string)
							m3u8, _ := resolveNuxtVal(vObj["hlsMasterPlaylistUrl"], data).(string)
							if title == "" {
								title = id
							}
							videoURL := ""
							if id != "" {
								videoURL = fmt.Sprintf("https://pmvhaven.com/video/%s", id)
							}
							dedupKey := videoURL
							if dedupKey == "" {
								dedupKey = m3u8
							}
							if dedupKey != "" && !seen[dedupKey] {
								seen[dedupKey] = true
								entries = append(entries, PlaylistEntry{
									Type:      ResultTypeVideo,
									Title:     common.SanitizeFilename(title),
									URL:       videoURL,
									StreamURL: m3u8,
								})
							}
						}
					}
					break
				}
			}
		}
	}

	if playlistTitle == "" {
		if m := rePMVTitle.FindStringSubmatch(bodyStr); len(m) > 1 {
			t := html.UnescapeString(m[1])
			t = strings.TrimSpace(rePMVSiteSuffix.ReplaceAllString(t, ""))
			if t != "" && !strings.EqualFold(t, "playlist") && !strings.EqualFold(t, "pmvhaven") {
				playlistTitle = t
			}
		}
	}
	if playlistTitle == "" {
		if m := rePMVH1Any.FindStringSubmatch(bodyStr); len(m) > 1 {
			t := html.UnescapeString(strings.TrimSpace(rePMVHTMLTags.ReplaceAllString(m[1], "")))
			if t != "" && !strings.EqualFold(t, "playlist") {
				playlistTitle = t
			}
		}
	}
	if playlistTitle == "" {
		playlistTitle = "PMVHaven Playlist"
	}

	// Fallback to DOM parsing if Nuxt data did not yield entries
	if len(entries) == 0 {
		for _, m := range rePMVVideoLink.FindAllStringSubmatch(normalized, -1) {
			slug := m[1]
			videoURL := fmt.Sprintf("https://pmvhaven.com/video/%s", slug)
			if !seen[videoURL] {
				seen[videoURL] = true
				cleanTitle := slug
				if unescaped, err := url.PathUnescape(slug); err == nil {
					cleanTitle = unescaped
				}
				entries = append(entries, PlaylistEntry{
					Type:  ResultTypeVideo,
					Title: cleanTitle,
					URL:   videoURL,
				})
			}
		}
	}

	if len(entries) == 0 {
		return nil, fmt.Errorf("no videos found in playlist %s", targetURL.String())
	}

	return &Result{
		Type: ResultTypePlaylist,
		Playlist: &PlaylistResult{
			Title:   common.SanitizeFilename(playlistTitle),
			PageURL: targetURL.String(),
			Entries: entries,
		},
	}, nil
}

func resolveNuxtVal(idx any, data []any) any {
	switch v := idx.(type) {
	case float64:
		i := int(v)
		if i >= 0 && i < len(data) {
			return data[i]
		}
	case int:
		if v >= 0 && v < len(data) {
			return data[v]
		}
	}
	return idx
}

func (p *PMVHavenExtractor) extractVideo(ctx context.Context, client *http.Client, pageURL *url.URL, urlStr string) (*Result, error) {
	bodyBytes, finalURL, err := common.FetchBytesWithRetry(ctx, client, urlStr, "", common.MaxHTMLBytes, common.DefaultTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch page: %w", err)
	}
	if finalURL != nil {
		pageURL = finalURL
		urlStr = pageURL.String()
	}

	bodyStr := string(bodyBytes)
	normalizedStr := common.NormalizeString(bodyStr)

	m3u8Raw := rePMVMasterM3u8.FindString(normalizedStr)
	if m3u8Raw == "" {
		m3u8Raw = rePMVAnyM3u8.FindString(normalizedStr)
	}

	var nuxtTitle string
	if m := rePMVNuxtData.FindStringSubmatch(bodyStr); len(m) > 1 {
		var data []any
		if err := json.Unmarshal([]byte(m[1]), &data); err == nil {
			for _, item := range data {
				itemMap, ok := item.(map[string]any)
				if !ok {
					continue
				}
				if m3u8RawVal, exists := itemMap["hlsMasterPlaylistUrl"]; exists {
					if m3u8Str, ok := resolveNuxtVal(m3u8RawVal, data).(string); ok && m3u8Str != "" {
						if m3u8Raw == "" {
							m3u8Raw = m3u8Str
						}
					}
					if titleRawVal, exists := itemMap["title"]; exists {
						if tStr, ok := resolveNuxtVal(titleRawVal, data).(string); ok && tStr != "" && nuxtTitle == "" {
							nuxtTitle = common.SanitizeFilename(tStr)
						}
					}
					if m3u8Raw != "" && nuxtTitle != "" {
						break
					}
				}
			}
		}
	}

	if m3u8Raw == "" {
		return nil, errorsNew("could not find .m3u8 stream URL in the page source")
	}

	m3u8URL, err := common.ValidateRemoteURL(m3u8Raw)
	if err != nil {
		return nil, fmt.Errorf("invalid m3u8 URL: %w", err)
	}

	title := nuxtTitle
	if title == "" {
		title = p.extractVideoTitle(bodyStr, urlStr)
	}

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

func (p *PMVHavenExtractor) extractVideoTitle(bodyStr, pageURL string) string {
	title := ""
	if h1Match := rePMVH1Class.FindStringSubmatch(bodyStr); len(h1Match) > 1 {
		title = h1Match[1]
	} else if h1Match := rePMVH1Any.FindStringSubmatch(bodyStr); len(h1Match) > 1 {
		title = h1Match[1]
	} else if titleMatch := rePMVTitle.FindStringSubmatch(bodyStr); len(titleMatch) > 1 {
		title = titleMatch[1]
	}

	if title != "" {
		title = rePMVHTMLTags.ReplaceAllString(title, "")
		title = html.UnescapeString(title)
		title = strings.TrimSpace(title)
		for {
			cleaned := rePMVSiteSuffix.ReplaceAllString(title, "")
			if cleaned == title {
				break
			}
			title = strings.TrimSpace(cleaned)
		}
		if strings.EqualFold(title, "pmvhaven") {
			title = ""
		}
	}

	if title == "" {
		if slugMatch := rePMVSlug.FindStringSubmatch(pageURL); len(slugMatch) > 1 {
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
			if base != "" && base != "." && base != "/" && !strings.EqualFold(base, "master") && !strings.EqualFold(base, "index") && !strings.EqualFold(base, "playlist") {
				title = base
			}
		}
	}

	if title == "" {
		title = "pmvhaven_video"
	}

	return common.SanitizeFilename(title)
}

func errorsNew(msg string) error {
	return fmt.Errorf("%s", msg)
}

func init() {
	Register(&PMVHavenExtractor{})
}
