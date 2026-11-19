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
	reFapTapBunnyCDN    = regexp.MustCompile(`https?://(vz-[a-zA-Z0-9-]+\.b-cdn\.net)/([0-9a-fA-F-]{36})/`)
	reFapTapTitle       = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	reFapTapOGTitle     = regexp.MustCompile(`(?is)<meta[^>]+property=["']og:title["'][^>]+content=["'](.*?)["']\s*(?:/?>|[a-zA-Z_-]+=)`)
	reFapTapSiteSuffix  = regexp.MustCompile(`(?i)\s*[-|]\s*Faptap\s*$`)
	reFapTapPlaylistCur = regexp.MustCompile(`(?i)\s*[-|]\s*Curated\s+List\s*$`)
)

type FapTapExtractor struct{}

func (f *FapTapExtractor) Name() string {
	return "faptap"
}

func (f *FapTapExtractor) Description() string {
	return "FapTap video and playlist extractor"
}

func (f *FapTapExtractor) Capabilities() Capability {
	return CapVideo | CapPlaylist
}

func (f *FapTapExtractor) Match(u *url.URL) bool {
	host := strings.ToLower(u.Host)
	return host == "faptap.net" || host == "www.faptap.net"
}

func (f *FapTapExtractor) Extract(ctx context.Context, client *http.Client, targetURL *url.URL, opts common.DownloadOptions) (*Result, error) {
	path := strings.ToLower(targetURL.Path)

	isPlaylistPath := strings.HasPrefix(path, "/playlist/") || strings.HasPrefix(path, "/playlists/")
	if isPlaylistPath && !opts.NoPlaylist {
		return f.extractPlaylist(ctx, client, targetURL, opts)
	}

	return f.extractVideo(ctx, client, targetURL)
}

func (f *FapTapExtractor) extractVideo(ctx context.Context, client *http.Client, targetURL *url.URL) (*Result, error) {
	videoID := filepath.Base(targetURL.Path)
	pageURL := targetURL.String()

	bodyBytes, finalURL, err := common.FetchBytesWithRetry(ctx, client, pageURL, "", common.MaxHTMLBytes, common.DefaultTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch faptap video page: %w", err)
	}
	if finalURL != nil {
		targetURL = finalURL
	}

	bodyStr := string(bodyBytes)
	videoTitle := ""
	streamURL := ""

	// Extract title from og:title or <title>
	if m := reFapTapOGTitle.FindStringSubmatch(bodyStr); len(m) > 1 {
		t := html.UnescapeString(m[1])
		t = strings.TrimSpace(reFapTapSiteSuffix.ReplaceAllString(t, ""))
		if t != "" {
			videoTitle = t
		}
	}
	if videoTitle == "" {
		if m := reFapTapTitle.FindStringSubmatch(bodyStr); len(m) > 1 {
			t := html.UnescapeString(m[1])
			t = strings.TrimSpace(reFapTapSiteSuffix.ReplaceAllString(t, ""))
			if t != "" {
				videoTitle = t
			}
		}
	}

	// Look for BunnyCDN host and stream UUID in HTML (from og:image or text)
	if m := reFapTapBunnyCDN.FindStringSubmatch(bodyStr); len(m) > 2 {
		bunnyHost := m[1]
		videoUUID := m[2]
		streamURL = fmt.Sprintf("https://%s/%s/playlist.m3u8", bunnyHost, videoUUID)
	}

	// If streamURL not found from HTML, query the FapTap API
	if streamURL == "" && videoID != "" && videoID != "." && videoID != "/" {
		apiURL := fmt.Sprintf("https://faptap.net/api/videos/%s", videoID)
		apiBytes, _, apiErr := common.FetchBytesWithRetry(ctx, client, apiURL, pageURL, 2<<20, common.DefaultTimeout)
		if apiErr == nil {
			var resp struct {
				Data struct {
					Name                 string `json:"name"`
					StreamURLSelfhosted  string `json:"stream_url_selfhosted"`
					ThumbnailURL         string `json:"thumbnail_url"`
					StreamURL            string `json:"stream_url"`
				} `json:"data"`
			}
			if err := json.Unmarshal(apiBytes, &resp); err == nil {
				if videoTitle == "" && resp.Data.Name != "" {
					videoTitle = resp.Data.Name
				}
				if resp.Data.StreamURLSelfhosted != "" {
					bunnyHost := "vz-3d77bc0a-a87.b-cdn.net"
					if resp.Data.ThumbnailURL != "" {
						if m := reFapTapBunnyCDN.FindStringSubmatch(resp.Data.ThumbnailURL); len(m) > 1 {
							bunnyHost = m[1]
						}
					}
					streamURL = fmt.Sprintf("https://%s/%s/playlist.m3u8", bunnyHost, resp.Data.StreamURLSelfhosted)
				} else if resp.Data.StreamURL != "" {
					streamURL = resp.Data.StreamURL
				}
			}
		}
	}

	if streamURL == "" {
		return nil, fmt.Errorf("no stream URL found for faptap video %s", targetURL.String())
	}

	if videoTitle == "" {
		videoTitle = videoID
	}

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

func (f *FapTapExtractor) extractPlaylist(ctx context.Context, client *http.Client, targetURL *url.URL, opts common.DownloadOptions) (*Result, error) {
	playlistID := filepath.Base(targetURL.Path)
	pageURL := targetURL.String()

	playlistTitle := ""

	// 1. Fetch playlist metadata from API or HTML
	infoAPI := fmt.Sprintf("https://faptap.net/api/playlists/%s", playlistID)
	infoBytes, _, infoErr := common.FetchBytesWithRetry(ctx, client, infoAPI, pageURL, 1<<20, common.DefaultTimeout)
	if infoErr == nil {
		var infoResp struct {
			Data struct {
				Name string `json:"name"`
			} `json:"data"`
		}
		if err := json.Unmarshal(infoBytes, &infoResp); err == nil && infoResp.Data.Name != "" {
			playlistTitle = infoResp.Data.Name
		}
	}

	if playlistTitle == "" {
		bodyBytes, _, err := common.FetchBytesWithRetry(ctx, client, pageURL, "", common.MaxHTMLBytes, common.DefaultTimeout)
		if err == nil {
			bodyStr := string(bodyBytes)
			if m := reFapTapTitle.FindStringSubmatch(bodyStr); len(m) > 1 {
				t := html.UnescapeString(m[1])
				t = strings.TrimSpace(reFapTapSiteSuffix.ReplaceAllString(t, ""))
				t = strings.TrimSpace(reFapTapPlaylistCur.ReplaceAllString(t, ""))
				if t != "" {
					playlistTitle = t
				}
			}
		}
	}

	if playlistTitle == "" {
		playlistTitle = fmt.Sprintf("Faptap Playlist %s", playlistID)
	}

	// 2. Fetch all playlist videos from API
	var entries []PlaylistEntry
	seen := make(map[string]bool)

	page := 0
	limit := 50
	for {
		videosAPI := fmt.Sprintf("https://faptap.net/api/playlists/%s/videos?limit=%d&page=%d", playlistID, limit, page)
		vidBytes, _, vidErr := common.FetchBytesWithRetry(ctx, client, videosAPI, pageURL, 4<<20, common.DefaultTimeout)
		if vidErr != nil {
			break
		}

		var vidResp struct {
			Data struct {
				Total  int `json:"total"`
				Videos []struct {
					Video struct {
						ID   string `json:"id"`
						Name string `json:"name"`
					} `json:"video"`
				} `json:"videos"`
			} `json:"data"`
		}

		if err := json.Unmarshal(vidBytes, &vidResp); err != nil {
			break
		}

		if len(vidResp.Data.Videos) == 0 {
			break
		}

		for _, item := range vidResp.Data.Videos {
			id := item.Video.ID
			if id == "" {
				continue
			}
			vidURL := fmt.Sprintf("https://faptap.net/v/%s", id)
			if !seen[vidURL] {
				seen[vidURL] = true
				cleanTitle := item.Video.Name
				if cleanTitle == "" {
					cleanTitle = id
				}
				entries = append(entries, PlaylistEntry{
					Type:  ResultTypeVideo,
					Title: cleanTitle,
					URL:   vidURL,
				})
			}
		}

		if len(entries) >= vidResp.Data.Total || len(vidResp.Data.Videos) < limit {
			break
		}
		page++
	}

	if len(entries) == 0 {
		return nil, fmt.Errorf("no videos found in faptap playlist %s", targetURL.String())
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
	Register(&FapTapExtractor{})
}
