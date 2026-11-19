package extractor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"pmv-dl/internal/common"
)

var (
	reGenericDirectExt   = regexp.MustCompile(`(?i)\.(?:m3u8|mp4|webm|mkv|mov|flv|avi|m4v|ts|mp3|m4a|ogg|opus|wav|flac)(?:[?#]|$)`)
	reGenericJSONLD      = regexp.MustCompile(`(?is)<script[^>]*type=["']application/ld\+json["'][^>]*>(.*?)</script>`)
	reGenericOGVideo     = regexp.MustCompile(`(?is)<meta[^>]+property=["']og:video(?::(?:url|secure_url))?["'][^>]+content=["'](.*?)["']\s*(?:/?>|[a-zA-Z_-]+=)`)
	reGenericTwitterStrm = regexp.MustCompile(`(?is)<meta[^>]+(?:name|property)=["']twitter:player:stream["'][^>]+(?:content|value)=["'](.*?)["']\s*(?:/?>|[a-zA-Z_-]+=)`)
	reGenericTwitterPlay = regexp.MustCompile(`(?is)<meta[^>]+(?:name|property)=["']twitter:player["'][^>]+(?:content|value)=["'](.*?)["']\s*(?:/?>|[a-zA-Z_-]+=)`)
	reGenericVideoBlock  = regexp.MustCompile(`(?is)<(?:(?:amp|dl8(?:-live)?)-)?(?:video|audio)\b([^>]*)>(.*?)</(?:(?:amp|dl8(?:-live)?)-)?(?:video|audio)>`)
	reGenericVideoSelf   = regexp.MustCompile(`(?is)<(?:(?:amp|dl8(?:-live)?)-)?(?:video|audio)\b([^>]*)/>`)
	reGenericSourceTagEl = regexp.MustCompile(`(?is)<source\b([^>]+)>`)
	reGenericAttr        = regexp.MustCompile(`(?i)([a-zA-Z0-9_:-]+)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>"']+))`)
	reGenericJSSourceObj = regexp.MustCompile(`(?is)\{[^{}]*?\b(?:file|src|url)\b\s*:\s*["']([^"']+)["'][^{}]*?\}`)
	reGenericJWPlayer    = regexp.MustCompile(`(?is)(?:jwplayer\s*\([^)]*\)\.setup|flashvars|JWPlayerOptions|flowplayer)[^;]*?["']?(?:file|url)["']?\s*:\s*["']([^"']+)["']`)
	reGenericVideoJS     = regexp.MustCompile(`(?is)videojs\s*\([^)]*\)\.src\s*\(\s*\[?\s*\{[^}]*src\s*:\s*["']([^"']+)["']`)
	reGenericBunnyCDN    = regexp.MustCompile(`https?://(vz-[a-zA-Z0-9-]+\.b-cdn\.net)/([0-9a-fA-F-]{36})/`)
	reGenericM3U8InPage  = regexp.MustCompile(`https?://[^"'\s\\<>]+\.m3u8(?:\?[^"'\s\\<>]*)?`)
	reGenericVideoInPage = regexp.MustCompile(`(?i)["']?([a-zA-Z0-9_-]{1,32})["']?\s*[:=]\s*["']((?:https?:)?//[^"'\s\\<>]+\.(?:mp4|webm|mkv|mov|flv|m3u8)(?:\?[^"'\s\\<>]*)?)["']`)
	reGenericRefresh     = regexp.MustCompile(`(?i)<meta[^>]+http-equiv=["']refresh["'][^>]+content=["'][^;]+;\s*url=['"]?([^'">\s]+)["']?`)
	reGenericIframeSrc   = regexp.MustCompile(`(?is)<iframe\b[^>]+?\bsrc\s*=\s*["']([^"']+)["']`)
	reGenericTitle       = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	reGenericOGTitle     = regexp.MustCompile(`(?is)<meta[^>]+property=["']og:title["'][^>]+content=["'](.*?)["']\s*(?:/?>|[a-zA-Z_-]+=)`)
	reGenericTwitTitle   = regexp.MustCompile(`(?is)<meta[^>]+(?:name|property)=["']twitter:title["'][^>]+content=["'](.*?)["']\s*(?:/?>|[a-zA-Z_-]+=)`)
	reGenericH1          = regexp.MustCompile(`(?is)<h1[^>]*>(.*?)</h1>`)
	reGenericHTMLTags    = regexp.MustCompile(`<[^>]*>`)
	reGenericCommonSfx   = regexp.MustCompile(`(?i)\s*[-|–—]\s*(?:Free\s+Porn\s+Videos.*|Watch\s+Online.*|Pornhub.*|xHamster(?:\.com)?.*|XNXX(?:\.com)?.*|XVideos(?:\.com)?.*|PornXP.*|Faptap.*|PMV\s*Hero.*|PMVHaven.*)\s*$`)
	reGenericScriptTag   = regexp.MustCompile(`(?is)<script[^>]*>(.*?)</script>`)
	reGenericProp        = regexp.MustCompile(`(?i)["']?([a-zA-Z0-9_]+)["']?\s*:\s*['"]([^'"]*)['"]`)
	reGenericPlayerjs    = regexp.MustCompile(`(?i)(?:new\s+Playerjs\s*\(|player\s*=\s*new\s+Playerjs|Playerjs\s*\()[\s\S]*?file\s*:\s*['"]([^'"]+)['"]`)
	reGenericBracket     = regexp.MustCompile(`\[([0-9a-zA-Z]+)\]\s*((?:https?:)?//[^\s,"']+)`)
	reGenericKVSFlash    = regexp.MustCompile(`(?s)(?:kt_player|var\s+flashvars\s*=)[^;]*?\{([^}]+)\}`)
	reGenericKVSURL      = regexp.MustCompile(`(?i)["']?video_url(?:_text)?["']?\s*:\s*["']([^"']+)["']`)
	reGenericKVSLicense  = regexp.MustCompile(`(?i)["']?license_code["']?\s*:\s*["']([^"']+)["']`)
	reResWxH             = regexp.MustCompile(`(?i)(?:^|[^a-zA-Z0-9])(\d{3,4})\s*[xX×]\s*(\d{3,4})(?:[^a-zA-Z0-9]|$)`)
	reResHeight          = regexp.MustCompile(`(?i)(?:^|[^a-zA-Z0-9])(4320|2160|1440|1080|720|576|540|480|360|240|144)[pPiI]?(?:\d{2,3})?(?:[^a-zA-Z0-9]|$)`)
	reResK               = regexp.MustCompile(`(?i)\b([48])[kK]\b`)
)

// FormatCandidate represents a discovered media stream with resolution metadata, modeled on yt-dlp format dicts.
type FormatCandidate struct {
	URL        string
	FormatID   string
	Height     int
	Width      int
	Bitrate    int
	IsManifest bool
}

// GenericExtractor implements a universal media and HTML webpage extractor modeled after yt-dlp's GenericIE.
type GenericExtractor struct{}

func (g *GenericExtractor) Name() string {
	return "generic"
}

func (g *GenericExtractor) Description() string {
	return "Universal generic media and webpage extractor (yt-dlp inspired)"
}

func (g *GenericExtractor) Capabilities() Capability {
	return CapVideo
}

func (g *GenericExtractor) Match(u *url.URL) bool {
	return u.Scheme == "http" || u.Scheme == "https"
}

func (g *GenericExtractor) Extract(ctx context.Context, client *http.Client, targetURL *url.URL, opts common.DownloadOptions) (*Result, error) {
	return g.extractWithRedirectDepth(ctx, client, targetURL, opts, 0)
}

func (g *GenericExtractor) extractWithRedirectDepth(ctx context.Context, client *http.Client, targetURL *url.URL, opts common.DownloadOptions, depth int) (*Result, error) {
	if depth > 3 {
		return nil, fmt.Errorf("exceeded maximum redirect depth in generic extractor")
	}

	urlStr := targetURL.String()

	// 1. Direct media link check (URL path ends in media extension)
	if reGenericDirectExt.MatchString(targetURL.Path) || common.IsKnownMediaExtension(common.DetermineExt(targetURL.Path, "")) {
		title := g.cleanBaseTitle(targetURL.Path)
		return &Result{
			Type: ResultTypeVideo,
			Video: &VideoResult{
				Title:     common.SanitizeFilename(title),
				PageURL:   urlStr,
				StreamURL: urlStr,
				Referer:   "",
			},
		}, nil
	}

	// 2. Fetch initial bytes / full HTML
	bodyBytes, finalURL, err := common.FetchBytesWithRetry(ctx, client, urlStr, "", common.MaxHTMLBytes, common.DefaultTimeout)
	if err != nil {
		return nil, fmt.Errorf("generic extractor failed to fetch %s: %w", urlStr, err)
	}
	if finalURL != nil {
		targetURL = finalURL
		urlStr = targetURL.String()
	}

	// 3. First bytes sniff: check if the response is an M3U8 manifest directly
	trimmed := bytes.TrimSpace(bodyBytes)
	if bytes.HasPrefix(trimmed, []byte("#EXTM3U")) {
		title := g.cleanBaseTitle(targetURL.Path)
		return &Result{
			Type: ResultTypeVideo,
			Video: &VideoResult{
				Title:     common.SanitizeFilename(title),
				PageURL:   urlStr,
				StreamURL: urlStr,
				Referer:   "",
			},
		}, nil
	}

	// 4. Sniff non-HTML binary data (yt-dlp: if not is_html(first_bytes))
	sniffLen := len(bodyBytes)
	if sniffLen > 512 {
		sniffLen = 512
	}
	if !isHTML(bodyBytes[:sniffLen]) {
		title := g.cleanBaseTitle(targetURL.Path)
		return &Result{
			Type: ResultTypeVideo,
			Video: &VideoResult{
				Title:     common.SanitizeFilename(title),
				PageURL:   urlStr,
				StreamURL: urlStr,
				Referer:   "",
			},
		}, nil
	}

	bodyStr := string(bodyBytes)
	normalized := common.NormalizeString(bodyStr)

	// 5. HTML Meta Refresh redirect (yt-dlp: http-equiv="refresh")
	if m := reGenericRefresh.FindStringSubmatch(bodyStr); len(m) > 1 {
		redirectTarget := html.UnescapeString(strings.TrimSpace(m[1]))
		if nextURL, err := common.ResolveURL(targetURL, redirectTarget); err == nil && nextURL.String() != urlStr {
			return g.extractWithRedirectDepth(ctx, client, nextURL, opts, depth+1)
		}
	}

	// 5b. Detect NoodleMagazine / Mat6tube proxy or clone dynamically
	if IsNoodleNetworkContent(bodyStr) {
		noodleExt := &NoodleMagazineExtractor{}
		return noodleExt.ExtractFromHTML(ctx, client, targetURL, bodyBytes, opts)
	}

	// 6. Extract best available title
	videoTitle := g.extractTitle(bodyStr, targetURL)

	// 7. Multi-format Extraction Cascade (each layer collects all formats & selects via opts.Quality)
	var candidates []FormatCandidate

	// (a) KVS Player (Kernel Video Sharing kt_player.js / flashvars with license code deobfuscation)
	candidates = extractKVSFormats(bodyStr)

	// (b) Playerjs multi-quality embedded player (file: '[720p]...[1080p]...')
	if len(candidates) == 0 {
		candidates = extractPlayerjsFormats(bodyStr)
	}

	// (c) HTML5 <video>/<audio> and <source> multi-resolution tags (yt-dlp: _parse_html5_media_entries)
	if len(candidates) == 0 {
		candidates = extractHTML5MediaFormats(bodyStr)
	}

	// (d) JWPlayer / Video.js / Plyr / Flowplayer multi-source JS objects
	if len(candidates) == 0 {
		candidates = extractJSPlayerFormats(bodyStr)
	}

	// (e) BunnyCDN Stream thumbnail pattern (FapTap and modern adult platforms)
	if len(candidates) == 0 {
		if m := reGenericBunnyCDN.FindStringSubmatch(bodyStr); len(m) > 2 {
			bunnyHost := m[1]
			videoUUID := m[2]
			candidates = append(candidates, FormatCandidate{
				URL:        fmt.Sprintf("https://%s/%s/playlist.m3u8", bunnyHost, videoUUID),
				IsManifest: true,
			})
		}
	}

	// (f) Schema.org JSON-LD VideoObject (yt-dlp: _search_json_ld)
	var embedFallbackURL string
	for _, m := range reGenericJSONLD.FindAllStringSubmatch(bodyStr, -1) {
		jsonText := strings.TrimSpace(m[1])
		if !strings.Contains(jsonText, "VideoObject") {
			continue
		}
		var parsed map[string]interface{}
		if err := json.Unmarshal([]byte(jsonText), &parsed); err == nil {
			if name, ok := parsed["name"].(string); ok && strings.TrimSpace(name) != "" && videoTitle == "video" {
				videoTitle = strings.TrimSpace(name)
			}
			if len(candidates) == 0 {
				if cURL, ok := parsed["contentUrl"].(string); ok && isPlayableCandidateURL(cURL) {
					cURL = strings.TrimSpace(cURL)
					candidates = append(candidates, FormatCandidate{
						URL:        cURL,
						Height:     ParseResolutionHeight(cURL),
						IsManifest: common.IsManifestExtension(common.DetermineExt(cURL, "")),
					})
				} else if eURL, ok := parsed["embedUrl"].(string); ok && strings.TrimSpace(eURL) != "" {
					embedFallbackURL = strings.TrimSpace(eURL)
				}
			}
		}
	}

	// (g) OpenGraph Video & Twitter Player Stream (yt-dlp: og:video, twitter:player:stream)
	if len(candidates) == 0 {
		for _, m := range reGenericTwitterStrm.FindAllStringSubmatch(bodyStr, -1) {
			u := html.UnescapeString(strings.TrimSpace(m[1]))
			if isPlayableCandidateURL(u) {
				candidates = append(candidates, FormatCandidate{
					URL:        u,
					Height:     ParseResolutionHeight(u),
					IsManifest: common.IsManifestExtension(common.DetermineExt(u, "")),
				})
			}
		}
	}
	if len(candidates) == 0 {
		for _, m := range reGenericOGVideo.FindAllStringSubmatch(bodyStr, -1) {
			u := html.UnescapeString(strings.TrimSpace(m[1]))
			if isPlayableCandidateURL(u) {
				candidates = append(candidates, FormatCandidate{
					URL:        u,
					Height:     ParseResolutionHeight(u),
					IsManifest: common.IsManifestExtension(common.DetermineExt(u, "")),
				})
			}
		}
	}
	if len(candidates) == 0 {
		if m := reGenericTwitterPlay.FindStringSubmatch(bodyStr); len(m) > 1 {
			candidate := html.UnescapeString(strings.TrimSpace(m[1]))
			if isPlayableCandidateURL(candidate) {
				candidates = append(candidates, FormatCandidate{
					URL:        candidate,
					Height:     ParseResolutionHeight(candidate),
					IsManifest: common.IsManifestExtension(common.DetermineExt(candidate, "")),
				})
			} else if embedFallbackURL == "" {
				embedFallbackURL = candidate
			}
		}
	}

	// (h) Embedded HLS manifests & script video variables across page
	if len(candidates) == 0 {
		candidates = extractScriptMediaFormats(normalized)
	}

	streamURL := SelectBestCandidate(candidates, opts.Quality)

	// (i) Follow iframe / embedUrl fallback if no direct stream was found on the outer page (yt-dlp GenericIE embed fallback)
	if streamURL == "" {
		if embedFallbackURL == "" {
			for _, im := range reGenericIframeSrc.FindAllStringSubmatch(bodyStr, -1) {
				src := html.UnescapeString(strings.TrimSpace(im[1]))
				if isLikelyVideoIframe(src) {
					embedFallbackURL = src
					break
				}
			}
		}
		if embedFallbackURL != "" {
			if nextURL, err := common.ResolveURL(targetURL, embedFallbackURL); err == nil && nextURL.String() != urlStr {
				if extr := Find(nextURL); extr != nil && extr.Name() != "generic" {
					res, exErr := extr.Extract(ctx, client, nextURL, opts)
					if exErr == nil && res != nil {
						if res.Video != nil && videoTitle != "" && videoTitle != "video" && (res.Video.Title == "" || res.Video.Title == "video" || strings.EqualFold(res.Video.Title, "Embed Player")) {
							res.Video.Title = common.SanitizeFilename(videoTitle)
						}
						return res, nil
					}
				}
				res, exErr := g.extractWithRedirectDepth(ctx, client, nextURL, opts, depth+1)
				if exErr == nil && res != nil {
					if res.Video != nil && videoTitle != "" && videoTitle != "video" && (res.Video.Title == "" || res.Video.Title == "video" || strings.EqualFold(res.Video.Title, "Embed Player")) {
						res.Video.Title = common.SanitizeFilename(videoTitle)
					}
					return res, nil
				}
			}
		}
		return nil, fmt.Errorf("generic extractor found no playable video or stream on %s", urlStr)
	}

	// Resolve relative or protocol-relative stream URLs
	resolvedStreamURL, err := common.ResolveURL(targetURL, streamURL)
	if err == nil {
		streamURL = resolvedStreamURL.String()
	}

	return &Result{
		Type: ResultTypeVideo,
		Video: &VideoResult{
			Title:     common.SanitizeFilename(videoTitle),
			PageURL:   urlStr,
			StreamURL: streamURL,
			Referer:   urlStr,
		},
	}, nil
}

// ParseResolutionHeight extracts vertical resolution (e.g. 1080, 720, 480, 360, 2160) from labels, attributes, or URLs,
// modeled on yt-dlp's parse_resolution.
func ParseResolutionHeight(s string) int {
	if s == "" {
		return 0
	}
	if m := reResWxH.FindStringSubmatch(s); len(m) > 2 {
		if h, err := strconv.Atoi(m[2]); err == nil && h >= 120 && h <= 8640 {
			return h
		}
	}
	if m := reResHeight.FindStringSubmatch(s); len(m) > 1 {
		if h, err := strconv.Atoi(m[1]); err == nil {
			return h
		}
	}
	if m := reResK.FindStringSubmatch(s); len(m) > 1 {
		if k, err := strconv.Atoi(m[1]); err == nil {
			return k * 540
		}
	}
	lower := strings.ToLower(strings.TrimSpace(s))
	switch lower {
	case "uhd", "4k":
		return 2160
	case "fhd", "fullhd", "full-hd":
		return 1080
	case "hd", "high":
		return 720
	case "hq", "med", "medium":
		return 480
	case "sd", "low":
		return 360
	case "lq", "mobile":
		return 240
	}
	return 0
}

// SelectBestCandidate chooses the optimal stream URL from candidates according to targetQuality
// ("best", "worst", "1080", "720p", "480", "360", etc.), matching yt-dlp's format selector behavior.
func SelectBestCandidate(candidates []FormatCandidate, targetQuality string) string {
	var valid []FormatCandidate
	seen := make(map[string]bool)
	for _, c := range candidates {
		u := strings.TrimSpace(c.URL)
		if u == "" || seen[u] || isExcludedPreviewOrAssetURL(u) {
			continue
		}
		seen[u] = true
		if c.Height == 0 {
			c.Height = ParseResolutionHeight(c.FormatID)
		}
		if c.Height == 0 {
			c.Height = ParseResolutionHeight(u)
		}
		if !c.IsManifest {
			c.IsManifest = common.IsManifestExtension(common.DetermineExt(u, ""))
		}
		valid = append(valid, c)
	}
	if len(valid) == 0 {
		return ""
	}
	if len(valid) == 1 {
		return valid[0].URL
	}

	qClean := strings.ToLower(strings.TrimSpace(targetQuality))
	qClean = strings.TrimSuffix(qClean, "p")

	// Separate candidates with known height from unknown/manifest candidates
	var withHeight []FormatCandidate
	var manifests []FormatCandidate
	for _, c := range valid {
		if c.Height > 0 {
			withHeight = append(withHeight, c)
		} else if c.IsManifest {
			manifests = append(manifests, c)
		}
	}

	if qClean == "worst" || qClean == "lowest" || qClean == "min" {
		if len(withHeight) > 0 {
			sort.SliceStable(withHeight, func(i, j int) bool {
				return withHeight[i].Height < withHeight[j].Height
			})
			return withHeight[0].URL
		}
		return valid[len(valid)-1].URL
	}

	if targetH, err := strconv.Atoi(qClean); err == nil && targetH > 0 {
		if len(withHeight) > 0 {
			// Sort descending by height
			sort.SliceStable(withHeight, func(i, j int) bool {
				if withHeight[i].Height != withHeight[j].Height {
					return withHeight[i].Height > withHeight[j].Height
				}
				return withHeight[i].Bitrate > withHeight[j].Bitrate
			})
			for _, c := range withHeight {
				if c.Height <= targetH {
					return c.URL
				}
			}
			// All known heights are above targetH -> return the lowest known height
			return withHeight[len(withHeight)-1].URL
		}
		if len(manifests) > 0 {
			return manifests[0].URL
		}
		return valid[0].URL
	}

	// Default ("best" / ""): prefer highest known height, or HLS manifest if no heights are known
	if len(withHeight) > 0 {
		sort.SliceStable(withHeight, func(i, j int) bool {
			if withHeight[i].Height != withHeight[j].Height {
				return withHeight[i].Height > withHeight[j].Height
			}
			return withHeight[i].Bitrate > withHeight[j].Bitrate
		})
		// If an HLS manifest exists and max direct height is < 720, prefer the HLS manifest
		if len(manifests) > 0 && withHeight[0].Height < 720 {
			return manifests[0].URL
		}
		return withHeight[0].URL
	}
	if len(manifests) > 0 {
		return manifests[0].URL
	}
	return valid[0].URL
}

// extractHTML5MediaFormats implements yt-dlp's _parse_html5_media_entries, parsing all <video>/<audio>
// and <source> tags along with their label/title/res/height/width/type attributes.
func extractHTML5MediaFormats(webpage string) []FormatCandidate {
	var out []FormatCandidate

	parseSourceTag := func(attrStr string) {
		attrs := parseHTMLAttributes(attrStr)
		src := firstNonEmpty(attrs["src"], attrs["data-video-src"], attrs["data-src"], attrs["data-source"])
		src = html.UnescapeString(strings.TrimSpace(src))
		if src == "" || isExcludedPreviewOrAssetURL(src) {
			return
		}
		label := firstNonEmpty(attrs["label"], attrs["title"], attrs["res"], attrs["data-res"], attrs["data-quality"], attrs["size"])
		height := 0
		if h, err := strconv.Atoi(attrs["height"]); err == nil && h > 0 {
			height = h
		} else if h, err := strconv.Atoi(attrs["res"]); err == nil && h > 0 {
			height = h
		} else if h, err := strconv.Atoi(attrs["size"]); err == nil && h > 0 {
			height = h
		}
		if height == 0 && label != "" {
			height = ParseResolutionHeight(label)
		}
		if height == 0 {
			height = ParseResolutionHeight(src)
		}
		width := 0
		if w, err := strconv.Atoi(attrs["width"]); err == nil && w > 0 {
			width = w
		}
		ext := common.MimetypeToExt(attrs["type"])
		if ext == "" {
			ext = common.DetermineExt(src, "")
		}
		out = append(out, FormatCandidate{
			URL:        src,
			FormatID:   label,
			Height:     height,
			Width:      width,
			IsManifest: common.IsManifestExtension(ext),
		})
	}

	for _, block := range reGenericVideoBlock.FindAllStringSubmatch(webpage, -1) {
		videoAttrs := parseHTMLAttributes(block[1])
		if vSrc := firstNonEmpty(videoAttrs["src"], videoAttrs["data-video-src"], videoAttrs["data-src"], videoAttrs["data-source"]); vSrc != "" {
			vSrc = html.UnescapeString(strings.TrimSpace(vSrc))
			if !isExcludedPreviewOrAssetURL(vSrc) {
				out = append(out, FormatCandidate{
					URL:        vSrc,
					Height:     ParseResolutionHeight(vSrc),
					IsManifest: common.IsManifestExtension(common.DetermineExt(vSrc, "")),
				})
			}
		}
		inner := block[2]
		for _, sm := range reGenericSourceTagEl.FindAllStringSubmatch(inner, -1) {
			parseSourceTag(sm[1])
		}
	}

	if len(out) > 0 {
		return out
	}

	for _, vm := range reGenericVideoSelf.FindAllStringSubmatch(webpage, -1) {
		parseSourceTag(vm[1])
	}
	for _, sm := range reGenericSourceTagEl.FindAllStringSubmatch(webpage, -1) {
		parseSourceTag(sm[1])
	}
	return out
}

func extractJSPlayerFormats(webpage string) []FormatCandidate {
	var out []FormatCandidate

	for _, m := range reGenericJSSourceObj.FindAllStringSubmatch(webpage, -1) {
		objStr := m[0]
		props := make(map[string]string)
		for _, pm := range reGenericProp.FindAllStringSubmatch(objStr, -1) {
			if len(pm) >= 3 {
				props[strings.ToLower(pm[1])] = pm[2]
			}
		}
		rawURL := firstNonEmpty(props["file"], props["src"], props["url"])
		rawURL = html.UnescapeString(common.NormalizeString(strings.TrimSpace(rawURL)))
		if !isPlayableCandidateURL(rawURL) {
			continue
		}
		label := firstNonEmpty(props["label"], props["title"], props["res"], props["quality"], props["height"])
		h := ParseResolutionHeight(label)
		if h == 0 {
			h = ParseResolutionHeight(rawURL)
		}
		ext := common.MimetypeToExt(props["type"])
		if ext == "" {
			ext = common.DetermineExt(rawURL, "")
		}
		out = append(out, FormatCandidate{
			URL:        rawURL,
			FormatID:   label,
			Height:     h,
			IsManifest: common.IsManifestExtension(ext) || strings.EqualFold(props["type"], "hls"),
		})
	}

	if len(out) == 0 {
		for _, m := range reGenericJWPlayer.FindAllStringSubmatch(webpage, -1) {
			u := html.UnescapeString(common.NormalizeString(strings.TrimSpace(m[1])))
			if isPlayableCandidateURL(u) {
				out = append(out, FormatCandidate{
					URL:        u,
					Height:     ParseResolutionHeight(u),
					IsManifest: common.IsManifestExtension(common.DetermineExt(u, "")),
				})
			}
		}
		for _, m := range reGenericVideoJS.FindAllStringSubmatch(webpage, -1) {
			u := html.UnescapeString(common.NormalizeString(strings.TrimSpace(m[1])))
			if isPlayableCandidateURL(u) {
				out = append(out, FormatCandidate{
					URL:        u,
					Height:     ParseResolutionHeight(u),
					IsManifest: common.IsManifestExtension(common.DetermineExt(u, "")),
				})
			}
		}
	}
	return out
}

func extractScriptMediaFormats(normalized string) []FormatCandidate {
	var out []FormatCandidate
	for _, m := range reGenericM3U8InPage.FindAllString(normalized, -1) {
		if !isExcludedPreviewOrAssetURL(m) {
			out = append(out, FormatCandidate{
				URL:        m,
				Height:     ParseResolutionHeight(m),
				IsManifest: true,
			})
		}
	}
	for _, m := range reGenericVideoInPage.FindAllStringSubmatch(normalized, -1) {
		key := m[1]
		u := m[2]
		if strings.Contains(strings.ToLower(key), "preview") || strings.Contains(strings.ToLower(key), "thumb") || isExcludedPreviewOrAssetURL(u) {
			continue
		}
		h := ParseResolutionHeight(key)
		if h == 0 {
			h = ParseResolutionHeight(u)
		}
		out = append(out, FormatCandidate{
			URL:        u,
			FormatID:   key,
			Height:     h,
			IsManifest: common.IsManifestExtension(common.DetermineExt(u, "")),
		})
	}
	return out
}

func parseHTMLAttributes(attrText string) map[string]string {
	attrs := make(map[string]string)
	for _, m := range reGenericAttr.FindAllStringSubmatch(attrText, -1) {
		key := strings.ToLower(strings.TrimSpace(m[1]))
		val := m[2]
		if val == "" {
			val = m[3]
		}
		if val == "" {
			val = m[4]
		}
		attrs[key] = val
	}
	return attrs
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func isPlayableCandidateURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || isExcludedPreviewOrAssetURL(raw) {
		return false
	}
	ext := common.DetermineExt(raw, "")
	return common.IsKnownMediaExtension(ext) || reGenericDirectExt.MatchString(raw)
}

func isExcludedPreviewOrAssetURL(raw string) bool {
	lower := strings.ToLower(strings.TrimSpace(raw))
	if lower == "" || strings.HasPrefix(lower, "javascript:") || strings.HasPrefix(lower, "data:") || strings.HasPrefix(lower, "blob:") {
		return true
	}
	ext := common.DetermineExt(lower, "")
	switch ext {
	case "vtt", "srt", "ass", "ssa", "jpg", "jpeg", "png", "webp", "gif", "svg", "ico", "css", "js", "swf", "xml", "json":
		return true
	}
	if strings.Contains(lower, "/t.pornxp.") || strings.Contains(lower, "trailer") || strings.Contains(lower, "thumb") || strings.Contains(lower, "sprite") {
		return true
	}
	return false
}

func isLikelyVideoIframe(raw string) bool {
	lower := strings.ToLower(strings.TrimSpace(raw))
	if lower == "" || strings.HasPrefix(lower, "javascript:") || strings.HasPrefix(lower, "about:") {
		return false
	}
	for _, adToken := range []string{"doubleclick", "googlesyndication", "exoclick", "juicyads", "trafficjunky", "tsyndicate", "magsrv", "realsrv", "adsco", "popads", "captcha", "cloudflare"} {
		if strings.Contains(lower, adToken) {
			return false
		}
	}
	for _, vidToken := range []string{"embed", "player", "video", "/e/", "/v/"} {
		if strings.Contains(lower, vidToken) {
			return true
		}
	}
	return false
}

func isHTML(b []byte) bool {
	lower := bytes.ToLower(b)
	return bytes.Contains(lower, []byte("<!doctype html")) ||
		bytes.Contains(lower, []byte("<html")) ||
		bytes.Contains(lower, []byte("<head")) ||
		bytes.Contains(lower, []byte("<body")) ||
		bytes.Contains(lower, []byte("<script")) ||
		bytes.Contains(lower, []byte("<title"))
}

func extractKVSFormats(webpage string) []FormatCandidate {
	var out []FormatCandidate
	scripts := reGenericScriptTag.FindAllStringSubmatch(webpage, -1)
	for _, s := range scripts {
		if len(s) < 2 {
			continue
		}
		content := s[1]
		if !strings.Contains(content, "kt_player") && !strings.Contains(content, "flashvars") && !strings.Contains(content, "video_url") {
			continue
		}

		props := make(map[string]string)
		for _, m := range reGenericProp.FindAllStringSubmatch(content, -1) {
			if len(m) >= 3 {
				props[strings.ToLower(m[1])] = m[2]
			}
		}

		licenseCode := props["license_code"]
		keys := []string{
			"video_alt_url5",
			"video_alt_url4",
			"video_alt_url3",
			"video_alt_url2",
			"video_alt_url",
			"video_url",
		}

		for idx, key := range keys {
			cand := strings.TrimSpace(props[key])
			if cand == "" || (strings.HasPrefix(cand, "/") && !strings.Contains(cand, ".mp4") && !strings.Contains(cand, ".m3u8")) {
				continue
			}
			if strings.HasPrefix(cand, "function/0/") && licenseCode != "" {
				cand = kvsGetRealURL(cand, licenseCode)
			}
			if cand == "" || strings.HasPrefix(cand, "function/0/") {
				continue
			}
			label := props[key+"_text"]
			h := ParseResolutionHeight(label)
			if h == 0 {
				h = ParseResolutionHeight(cand)
			}
			if h == 0 {
				// Fallback priority order matching KVS alt_url tiers
				h = (len(keys) - idx) * 10
			}
			out = append(out, FormatCandidate{
				URL:        cand,
				FormatID:   label,
				Height:     h,
				IsManifest: common.IsManifestExtension(common.DetermineExt(cand, "")),
			})
		}
	}

	if len(out) > 0 {
		return out
	}

	m := reGenericKVSFlash.FindStringSubmatch(webpage)
	if len(m) >= 2 {
		flashvars := m[1]
		videoURL := ""
		if vm := reGenericKVSURL.FindStringSubmatch(flashvars); len(vm) > 1 {
			videoURL = vm[1]
		}
		if videoURL != "" {
			licenseCode := ""
			if lm := reGenericKVSLicense.FindStringSubmatch(flashvars); len(lm) > 1 {
				licenseCode = lm[1]
			}
			if strings.HasPrefix(videoURL, "function/0/") && licenseCode != "" {
				videoURL = kvsGetRealURL(videoURL, licenseCode)
			}
			out = append(out, FormatCandidate{
				URL:    videoURL,
				Height: ParseResolutionHeight(videoURL),
			})
		}
	}

	return out
}

func extractKVSPlayer(webpage string, quality ...string) string {
	q := "best"
	if len(quality) > 0 && quality[0] != "" {
		q = quality[0]
	}
	return SelectBestCandidate(extractKVSFormats(webpage), q)
}

func extractPlayerjsFormats(webpage string) []FormatCandidate {
	var out []FormatCandidate
	for _, m := range reGenericPlayerjs.FindAllStringSubmatch(webpage, -1) {
		fileSpec := m[1]
		brackets := reGenericBracket.FindAllStringSubmatch(fileSpec, -1)
		if len(brackets) > 0 {
			for _, b := range brackets {
				lbl := strings.TrimSpace(b[1])
				u := strings.TrimSpace(b[2])
				h := ParseResolutionHeight(lbl)
				if h == 0 {
					h = ParseResolutionHeight(u)
				}
				out = append(out, FormatCandidate{
					URL:        u,
					FormatID:   lbl,
					Height:     h,
					IsManifest: common.IsManifestExtension(common.DetermineExt(u, "")),
				})
			}
			continue
		}
		for _, part := range strings.Split(fileSpec, ",") {
			part = strings.TrimSpace(part)
			if isPlayableCandidateURL(part) {
				out = append(out, FormatCandidate{
					URL:        part,
					Height:     ParseResolutionHeight(part),
					IsManifest: common.IsManifestExtension(common.DetermineExt(part, "")),
				})
			}
		}
	}
	return out
}

func extractPlayerjs(webpage string, quality ...string) string {
	q := "best"
	if len(quality) > 0 && quality[0] != "" {
		q = quality[0]
	}
	return SelectBestCandidate(extractPlayerjsFormats(webpage), q)
}

func kvsGetLicenseToken(licenseCode string) []int {
	cleanCode := strings.ReplaceAll(licenseCode, "$", "")
	if len(cleanCode) == 0 {
		return nil
	}
	licenseValues := make([]int, len(cleanCode))
	for i, c := range cleanCode {
		if c >= '0' && c <= '9' {
			licenseValues[i] = int(c - '0')
		}
	}

	modlicense := strings.ReplaceAll(cleanCode, "0", "1")
	center := len(modlicense) / 2
	frontStr := modlicense[:center+1]
	backStr := modlicense[center:]
	frontVal, _ := strconv.ParseInt(frontStr, 10, 64)
	backVal, _ := strconv.ParseInt(backStr, 10, 64)
	diff := frontVal - backVal
	if diff < 0 {
		diff = -diff
	}
	modStr := strconv.FormatInt(4*diff, 10)
	if len(modStr) > center+1 {
		modStr = modStr[:center+1]
	}

	token := make([]int, 0, len(modStr)*4)
	for _, c := range modStr {
		current := int(c - '0')
		for offset := 0; offset < 4; offset++ {
			pos := len(token)/4 + offset
			if pos < len(licenseValues) {
				token = append(token, (licenseValues[pos]+current)%10)
			}
		}
	}
	return token
}

func kvsGetRealURL(videoURL string, licenseCode string) string {
	const prefix = "function/0/"
	if !strings.HasPrefix(videoURL, prefix) {
		return videoURL
	}

	raw := videoURL[len(prefix):]
	parsed, err := url.Parse(raw)
	if err != nil {
		return videoURL
	}

	token := kvsGetLicenseToken(licenseCode)
	if len(token) < 32 {
		return videoURL
	}

	urlparts := strings.Split(parsed.Path, "/")
	if len(urlparts) < 4 {
		return videoURL
	}

	const hashLength = 32
	if len(urlparts[3]) < hashLength {
		return videoURL
	}

	hashChars := []byte(urlparts[3][:hashLength])
	indices := make([]int, hashLength)
	for i := range indices {
		indices[i] = i
	}

	accum := 0
	for src := hashLength - 1; src >= 0; src-- {
		accum += token[src]
		dest := (src + accum) % hashLength
		indices[src], indices[dest] = indices[dest], indices[src]
	}

	swapped := make([]byte, hashLength)
	for i, idx := range indices {
		swapped[i] = hashChars[idx]
	}

	urlparts[3] = string(swapped) + urlparts[3][hashLength:]
	parsed.Path = strings.Join(urlparts, "/")
	return parsed.String()
}

func (g *GenericExtractor) cleanBaseTitle(path string) string {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	if ext != "" {
		base = strings.TrimSuffix(base, ext)
	}
	if unescaped, err := url.PathUnescape(base); err == nil {
		base = unescaped
	}
	base = strings.TrimSpace(base)
	if base == "" || base == "." || base == "/" || strings.EqualFold(base, "master") || strings.EqualFold(base, "index") || strings.EqualFold(base, "playlist") {
		return "video"
	}
	return base
}

func (g *GenericExtractor) extractTitle(bodyStr string, targetURL *url.URL) string {
	if m := reGenericOGTitle.FindStringSubmatch(bodyStr); len(m) > 1 {
		t := html.UnescapeString(m[1])
		t = strings.TrimSpace(reGenericCommonSfx.ReplaceAllString(t, ""))
		if t != "" {
			return t
		}
	}
	if m := reGenericTwitTitle.FindStringSubmatch(bodyStr); len(m) > 1 {
		t := html.UnescapeString(m[1])
		t = strings.TrimSpace(reGenericCommonSfx.ReplaceAllString(t, ""))
		if t != "" {
			return t
		}
	}

	var h1Candidate string
	for _, m := range reGenericH1.FindAllStringSubmatch(bodyStr, -1) {
		t := html.UnescapeString(strings.TrimSpace(reGenericHTMLTags.ReplaceAllString(m[1], "")))
		lower := strings.ToLower(t)
		if t != "" && !strings.Contains(lower, "age verification") && !strings.Contains(lower, "backup domain") {
			h1Candidate = t
			break
		}
	}

	if m := reGenericTitle.FindStringSubmatch(bodyStr); len(m) > 1 {
		t := html.UnescapeString(strings.TrimSpace(m[1]))
		t = strings.TrimSpace(reGenericCommonSfx.ReplaceAllString(t, ""))
		// If <h1> is a clean prefix of <title> (e.g. "Nude Photo Workshop" vs "Nude Photo Workshop – SiteName"), prefer <h1>
		if h1Candidate != "" && strings.HasPrefix(strings.ToLower(t), strings.ToLower(h1Candidate)) {
			return h1Candidate
		}
		if t != "" {
			return t
		}
	}

	if h1Candidate != "" {
		return h1Candidate
	}
	return g.cleanBaseTitle(targetURL.Path)
}

func init() {
	Register(&GenericExtractor{})
}
