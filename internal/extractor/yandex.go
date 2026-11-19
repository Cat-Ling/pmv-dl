package extractor

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"pmv-dl/internal/common"
)

var (
	reYandexHost         = regexp.MustCompile(`(?i)(?:^|\.)(?:yandex\.[a-z]{2,3}(?:\.[a-z]{2})?|ya\.ru|dzen\.ru)$`)
	reYandexPreviewID    = regexp.MustCompile(`/video/preview/(\d+)`)
	reYandexPlayerID     = regexp.MustCompile(`/player/([\da-fA-F]{32}|[\w-]{12})`)
	reYandexPreloaded    = regexp.MustCompile(`(?is)<noframes[^>]+id=["']UniAppVideo-PreloadedState["'][^>]*>(.*?)</noframes>`)
	reYandexInlineParams = regexp.MustCompile(`window\.Ya\.__inline_params__\s*=\s*JSON\.parse\('([^']+?\\u0022video\\u0022:[^']+?)'\);`)
	reYandexIframeSrc    = regexp.MustCompile(`(?i)<iframe[^>]+src=["']([^"']+)["']`)
	reVKAccessToken      = regexp.MustCompile(`"access_token"\s*:\s*"(anonym\.[^"]+)"`)
	reVKPlayerParams     = regexp.MustCompile(`(?s)var\s+playerParams\s*=\s*(\{.+?\})\s*;\s*\n`)
	reVKVideoID          = regexp.MustCompile(`video(-?\d+_\d+)`)
)

type YandexExtractor struct{}

func (y *YandexExtractor) Name() string {
	return "yandex"
}

func (y *YandexExtractor) Description() string {
	return "Yandex Video & Yandex Video Preview extractor (all Yandex TLDs/subdomains)"
}

func (y *YandexExtractor) Capabilities() Capability {
	return CapVideo
}

func (y *YandexExtractor) Match(u *url.URL) bool {
	host := strings.ToLower(u.Hostname())
	return reYandexHost.MatchString(host)
}

func (y *YandexExtractor) Extract(ctx context.Context, client *http.Client, targetURL *url.URL, opts common.DownloadOptions) (*Result, error) {
	// 1. Direct Yandex VH player or stream_id query parameter (YandexVideoIE)
	if streamID := y.extractVHStreamID(targetURL); streamID != "" {
		return y.extractVHPlayer(ctx, client, streamID, targetURL.String(), opts)
	}

	// 2. Yandex Video Preview (/video/preview/<id> or ?filmId=<id>)
	previewID := y.extractPreviewID(targetURL)
	urlStr := targetURL.String()
	referer := targetURL.Scheme + "://" + targetURL.Host + "/"

	bodyBytes, finalURL, err := common.FetchBytesWithRetry(ctx, client, urlStr, referer, common.MaxHTMLBytes, common.DefaultTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch Yandex page %s: %w", urlStr, err)
	}
	if finalURL != nil {
		targetURL = finalURL
	}
	bodyStr := string(bodyBytes)

	if previewID == "" {
		previewID = y.extractPreviewID(targetURL)
	}

	return y.extractPreviewFromHTML(ctx, client, targetURL, bodyStr, previewID, opts)
}

func (y *YandexExtractor) extractVHStreamID(u *url.URL) string {
	if m := reYandexPlayerID.FindStringSubmatch(u.Path); len(m) > 1 {
		return m[1]
	}
	if sid := strings.TrimSpace(u.Query().Get("stream_id")); sid != "" {
		return sid
	}
	return ""
}

func (y *YandexExtractor) extractPreviewID(u *url.URL) string {
	if m := reYandexPreviewID.FindStringSubmatch(u.Path); len(m) > 1 {
		return m[1]
	}
	if fid := strings.TrimSpace(u.Query().Get("filmId")); fid != "" {
		return fid
	}
	return ""
}

type yandexPreloadedState struct {
	Clips  yandexClipsContainer `json:"clips"`
	Viewer struct {
		Clips yandexClipsContainer `json:"clips"`
	} `json:"viewer"`
}

type yandexClipsContainer struct {
	Items map[string]yandexClipItem `json:"items"`
	Dups  map[string]yandexClipDup  `json:"dups"`
}

type yandexClipItem struct {
	VideoID     string `json:"videoId"`
	Description string `json:"description"`
	URL         string `json:"url"`
}

type yandexClipDup struct {
	VideoID    string `json:"videoId"`
	Title      string `json:"title"`
	CleanTitle string `json:"cleanTitle"`
	Host       struct {
		Href      string `json:"href"`
		PlayerURI string `json:"playerUri"`
	} `json:"host"`
	Player struct {
		EmbedURL   string `json:"embedUrl"`
		VideoURL   string `json:"videoUrl"`
		ContentURL string `json:"contentUrl"`
		StreamURL  string `json:"streamUrl"`
	} `json:"player"`
}

func (y *YandexExtractor) extractPreviewFromHTML(ctx context.Context, client *http.Client, targetURL *url.URL, bodyStr string, previewID string, opts common.DownloadOptions) (*Result, error) {
	var title, embedURL, videoURL, directStreamURL string

	// Strategy A: Modern UniAppVideo-PreloadedState JSON
	if m := reYandexPreloaded.FindStringSubmatch(bodyStr); len(m) > 1 {
		rawJSON := html.UnescapeString(strings.TrimSpace(m[1]))
		var state yandexPreloadedState
		if err := json.Unmarshal([]byte(rawJSON), &state); err == nil {
			dup, hasDup := pickYandexDup(state.Clips.Dups, previewID)
			if !hasDup {
				dup, hasDup = pickYandexDup(state.Viewer.Clips.Dups, previewID)
			}
			item, hasItem := pickYandexItem(state.Clips.Items, previewID)
			if !hasItem {
				item, hasItem = pickYandexItem(state.Viewer.Clips.Items, previewID)
			}

			if hasDup {
				if dup.CleanTitle != "" {
					title = dup.CleanTitle
				} else {
					title = dup.Title
				}
				embedURL = normalizeYandexURL(dup.Player.EmbedURL)
				if embedURL == "" && dup.Host.PlayerURI != "" {
					if im := reYandexIframeSrc.FindStringSubmatch(html.UnescapeString(dup.Host.PlayerURI)); len(im) > 1 {
						embedURL = normalizeYandexURL(im[1])
					}
				}
				videoURL = normalizeYandexURL(dup.Player.VideoURL)
				if videoURL == "" {
					videoURL = normalizeYandexURL(dup.Host.Href)
				}
				if dup.Player.ContentURL != "" {
					directStreamURL = normalizeYandexURL(dup.Player.ContentURL)
				} else if dup.Player.StreamURL != "" {
					directStreamURL = normalizeYandexURL(dup.Player.StreamURL)
				}
			}
			if hasItem {
				if title == "" {
					title = item.Description
				}
				if videoURL == "" {
					videoURL = normalizeYandexURL(item.URL)
				}
			}
		}
	}

	// Strategy B: Legacy window.Ya.__inline_params__ JSON (yt-dlp YandexVideoPreviewIE)
	if embedURL == "" && videoURL == "" && directStreamURL == "" {
		if m := reYandexInlineParams.FindStringSubmatch(bodyStr); len(m) > 1 {
			unescaped := common.NormalizeString(strings.ReplaceAll(m[1], `\u0022`, `"`))
			var legacy struct {
				Video struct {
					URL   string `json:"url"`
					Title string `json:"title"`
				} `json:"video"`
			}
			if err := json.Unmarshal([]byte(unescaped), &legacy); err == nil && legacy.Video.URL != "" {
				videoURL = normalizeYandexURL(legacy.Video.URL)
				if title == "" {
					title = legacy.Video.Title
				}
			}
		}
	}

	cleanYandexTitle := common.SanitizeFilename(strings.TrimSpace(title))

	// 1. Direct stream URL inside preview JSON
	if directStreamURL != "" {
		if valid, err := common.ValidateRemoteURL(directStreamURL); err == nil {
			return &Result{
				Type: ResultTypeVideo,
				Video: &VideoResult{
					Title:     cleanYandexTitle,
					PageURL:   targetURL.String(),
					StreamURL: valid.String(),
					Referer:   targetURL.String(),
				},
			}, nil
		}
	}

	// 2. Delegate to embedURL (e.g., nmcorp.video, vkvideo.ru/video_ext.php, frontend.vh.yandex.ru)
	if embedURL != "" {
		if res, err := y.resolveTargetURL(ctx, client, embedURL, cleanYandexTitle, targetURL.String(), opts); err == nil && res != nil {
			return res, nil
		}
	}

	// 3. Delegate to source videoURL (e.g., noodlemagazine.com/watch/..., vk.com/video..., etc.)
	if videoURL != "" {
		if res, err := y.resolveTargetURL(ctx, client, videoURL, cleanYandexTitle, targetURL.String(), opts); err == nil && res != nil {
			return res, nil
		}
	}

	return nil, fmt.Errorf("could not extract video stream from Yandex preview %s", targetURL.String())
}

func (y *YandexExtractor) resolveTargetURL(ctx context.Context, client *http.Client, rawURL string, fallbackTitle string, referer string, opts common.DownloadOptions) (*Result, error) {
	parsedURL, err := common.ValidateRemoteURL(rawURL)
	if err != nil {
		return nil, err
	}

	host := strings.ToLower(parsedURL.Hostname())
	if strings.Contains(host, "vkvideo.ru") || strings.Contains(host, "vk.com") || strings.Contains(host, "vk.ru") {
		if res, vkErr := y.extractVKEmbedOrVideo(ctx, client, parsedURL, fallbackTitle, referer, opts); vkErr == nil && res != nil {
			return res, nil
		}
	}

	extr := Find(parsedURL)
	if extr == nil {
		return nil, fmt.Errorf("no extractor for %s", rawURL)
	}
	res, err := extr.Extract(ctx, client, parsedURL, opts)
	if err != nil {
		return nil, err
	}
	if res != nil && res.Video != nil && fallbackTitle != "" && fallbackTitle != "video" {
		cur := strings.ToLower(strings.TrimSpace(res.Video.Title))
		if cur == "" || cur == "video" || cur == "embed player" || strings.HasPrefix(cur, "player") {
			res.Video.Title = fallbackTitle
		}
	}
	return res, nil
}

func (y *YandexExtractor) extractVKEmbedOrVideo(ctx context.Context, client *http.Client, vkURL *url.URL, fallbackTitle string, referer string, opts common.DownloadOptions) (*Result, error) {
	q := vkURL.Query()
	oid := strings.TrimSpace(q.Get("oid"))
	id := strings.TrimSpace(q.Get("id"))
	hash := strings.TrimSpace(q.Get("hash"))

	if (oid == "" || id == "") && reVKVideoID.MatchString(vkURL.Path) {
		m := reVKVideoID.FindStringSubmatch(vkURL.Path)
		parts := strings.SplitN(m[1], "_", 2)
		if len(parts) == 2 {
			oid, id = parts[0], parts[1]
		}
	}
	if oid == "" || id == "" {
		return nil, fmt.Errorf("missing oid/id in VK URL %s", vkURL.String())
	}

	embedPageURL := vkURL.String()
	if !strings.Contains(vkURL.Path, "video_ext.php") {
		embedPageURL = fmt.Sprintf("https://vkvideo.ru/video_ext.php?oid=%s&id=%s", url.QueryEscape(oid), url.QueryEscape(id))
		if hash != "" {
			embedPageURL += "&hash=" + url.QueryEscape(hash)
		}
	}

	bodyBytes, _, err := common.FetchBytesWithRetry(ctx, client, embedPageURL, referer, common.MaxHTMLBytes, common.DefaultTimeout)
	if err != nil {
		return nil, err
	}
	bodyStr := string(bodyBytes)

	// Check legacy playerParams first (yt-dlp VKIE)
	if m := reVKPlayerParams.FindStringSubmatch(bodyStr); len(m) > 1 {
		var pp struct {
			Params []map[string]any `json:"params"`
		}
		if err := json.Unmarshal([]byte(m[1]), &pp); err == nil && len(pp.Params) > 0 {
			if stream, title := pickVKStreamFromMap(pp.Params[0], opts.Quality); stream != "" {
				if title == "" {
					title = fallbackTitle
				}
				return &Result{
					Type: ResultTypeVideo,
					Video: &VideoResult{
						Title:     common.SanitizeFilename(title),
						PageURL:   vkURL.String(),
						StreamURL: stream,
						Referer:   "https://vkvideo.ru/",
					},
				}, nil
			}
		}
	}

	// Modern VK Video BFF API (window.vk.webToken.access_token + api.vkvideo.ru/method/video.get)
	tokMatch := reVKAccessToken.FindStringSubmatch(bodyStr)
	if len(tokMatch) < 2 {
		return nil, fmt.Errorf("could not find VK webToken in %s", embedPageURL)
	}
	accessToken := tokMatch[1]

	videoSpec := oid + "_" + id
	if hash != "" {
		videoSpec += "_" + hash
	}

	form := url.Values{}
	form.Set("videos", videoSpec)
	form.Set("access_token", accessToken)
	form.Set("v", "5.289")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.vkvideo.ru/method/video.get", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", common.UserAgent)
	req.Header.Set("Origin", "https://vkvideo.ru")
	req.Header.Set("Referer", "https://vkvideo.ru/")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, common.MaxHTMLBytes))
	if err != nil {
		return nil, err
	}

	var apiResp struct {
		Response struct {
			Items []struct {
				Title string            `json:"title"`
				Files map[string]string `json:"files"`
			} `json:"items"`
		} `json:"response"`
	}
	if err := json.Unmarshal(respBytes, &apiResp); err != nil || len(apiResp.Response.Items) == 0 {
		return nil, fmt.Errorf("empty response from api.vkvideo.ru for %s", videoSpec)
	}

	item := apiResp.Response.Items[0]
	streamURL := pickVKFilesStream(item.Files, opts.Quality)
	if streamURL == "" {
		return nil, fmt.Errorf("no playable streams in VK video %s", videoSpec)
	}

	finalTitle := fallbackTitle
	if finalTitle == "" || finalTitle == "video" {
		finalTitle = item.Title
	}

	return &Result{
		Type: ResultTypeVideo,
		Video: &VideoResult{
			Title:     common.SanitizeFilename(finalTitle),
			PageURL:   vkURL.String(),
			StreamURL: streamURL,
			Referer:   "https://vkvideo.ru/",
		},
	}, nil
}

func pickVKFilesStream(files map[string]string, targetQuality string) string {
	type cand struct {
		height int
		url    string
	}
	var cands []cand
	for k, v := range files {
		if v == "" || (!strings.HasPrefix(v, "http://") && !strings.HasPrefix(v, "https://")) {
			continue
		}
		if strings.HasPrefix(k, "mp4_") || strings.HasPrefix(k, "url") {
			numStr := strings.TrimPrefix(strings.TrimPrefix(k, "mp4_"), "url")
			if h, err := strconv.Atoi(numStr); err == nil {
				cands = append(cands, cand{height: h, url: v})
			}
		}
	}
	if len(cands) > 0 {
		sort.Slice(cands, func(i, j int) bool {
			return cands[i].height > cands[j].height
		})
		qNum, _ := strconv.Atoi(strings.TrimSuffix(strings.ToLower(strings.TrimSpace(targetQuality)), "p"))
		if qNum > 0 {
			for _, c := range cands {
				if c.height <= qNum {
					return c.url
				}
			}
		}
		return cands[0].url
	}
	if hls := files["hls"]; hls != "" {
		return hls
	}
	return ""
}

func pickVKStreamFromMap(m map[string]any, targetQuality string) (string, string) {
	files := make(map[string]string)
	for k, v := range m {
		if s, ok := v.(string); ok {
			files[k] = s
		}
	}
	title, _ := m["md_title"].(string)
	return pickVKFilesStream(files, targetQuality), title
}

func (y *YandexExtractor) extractVHPlayer(ctx context.Context, client *http.Client, videoID string, pageURL string, opts common.DownloadOptions) (*Result, error) {
	apiURL := fmt.Sprintf("https://frontend.vh.yandex.ru/v23/player/%s.json?stream_options=hires&disable_trackings=1", url.PathEscape(videoID))
	bodyBytes, _, err := common.FetchBytesWithRetry(ctx, client, apiURL, pageURL, common.MaxHTMLBytes, common.DefaultTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch Yandex VH player JSON for %s: %w", videoID, err)
	}

	var payload struct {
		Content struct {
			Title         string `json:"title"`
			ComputedTitle string `json:"computed_title"`
			ContentURL    string `json:"content_url"`
			Streams       []struct {
				URL string `json:"url"`
			} `json:"streams"`
		} `json:"content"`
	}
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse Yandex VH player JSON: %w", err)
	}

	title := strings.TrimSpace(payload.Content.Title)
	if title == "" {
		title = strings.TrimSpace(payload.Content.ComputedTitle)
	}
	if title == "" {
		title = "yandex_" + videoID
	}

	var candidates []string
	for _, s := range payload.Content.Streams {
		if u := normalizeYandexURL(s.URL); u != "" {
			candidates = append(candidates, u)
		}
	}
	if u := normalizeYandexURL(payload.Content.ContentURL); u != "" {
		candidates = append(candidates, u)
	}

	var chosen string
	for _, c := range candidates {
		ext := common.DetermineExt(c, "")
		if ext == "ismc" || ext == "mpd" {
			continue
		}
		if ext == "m3u8" {
			chosen = c
			break
		}
		if chosen == "" {
			chosen = c
		}
	}
	if chosen == "" {
		return nil, fmt.Errorf("no supported streams found in Yandex VH player for %s", videoID)
	}

	return &Result{
		Type: ResultTypeVideo,
		Video: &VideoResult{
			Title:     common.SanitizeFilename(title),
			PageURL:   pageURL,
			StreamURL: chosen,
			Referer:   "https://frontend.vh.yandex.ru/",
		},
	}, nil
}

func pickYandexDup(dups map[string]yandexClipDup, id string) (yandexClipDup, bool) {
	if len(dups) == 0 {
		return yandexClipDup{}, false
	}
	if id != "" {
		if d, ok := dups[id]; ok {
			return d, true
		}
	}
	for k, d := range dups {
		if k != "undefined" {
			return d, true
		}
	}
	return yandexClipDup{}, false
}

func pickYandexItem(items map[string]yandexClipItem, id string) (yandexClipItem, bool) {
	if len(items) == 0 {
		return yandexClipItem{}, false
	}
	if id != "" {
		if it, ok := items[id]; ok {
			return it, true
		}
	}
	for k, it := range items {
		if k != "undefined" {
			return it, true
		}
	}
	return yandexClipItem{}, false
}

func normalizeYandexURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "//") {
		return "https:" + raw
	}
	return raw
}

func init() {
	Register(&YandexExtractor{})
}
