package common

import (
	"mime"
	"net/http"
	"net/url"
	"strings"
)

var (
	videoExts = []string{
		"avi", "flv", "mkv", "mov", "mp4", "webm",
		"3g2", "3gp", "f4v", "mk3d", "divx", "mpg", "ogv", "m4v", "wmv",
		"asx", "ismv", "m2t", "m2ts", "m2v", "m4s", "mng", "mp2v", "mp4v",
		"mpe", "mpeg", "mpeg1", "mpeg2", "mpeg4", "mxf", "ogm", "qt", "rm",
		"swf", "ts", "vid", "vob", "vp9",
	}

	audioExts = []string{
		"aiff", "alac", "flac", "m4a", "mka", "mp3", "ogg", "opus", "wav",
		"aac", "ape", "asf", "f4a", "f4b", "m4b", "m4r", "oga", "ogx",
		"spx", "vorbis", "wma", "weba",
		"3ga", "ac3", "adts", "aif", "au", "dts", "isma", "it", "mid",
		"mod", "mpga", "mp1", "mp2", "mp4a", "mpa", "ra", "shn", "xm",
	}

	imageExts = []string{
		"jpg", "png", "webp",
		"avif", "bmp", "gif", "heic", "ico", "image", "jfif", "jng",
		"jpe", "jpeg", "jxl", "svg", "tif", "tiff", "wbmp",
	}

	manifestExts = []string{
		"f4f", "f4m", "m3u8", "smil", "mpd", "ism", "m3u",
	}

	subtitleExts = []string{
		"srt", "vtt", "ass", "lrc",
		"dfxp", "fs", "ismt", "json3", "sami", "scc",
		"srv1", "srv2", "srv3", "ssa", "tt", "ttml", "sbv",
	}

	internalExts = []string{
		"description", "json", "meta", "orig", "part", "temp",
		"uncut", "unknown_video", "ytdl", "xml", "mhtml",
	}

	directMediaExtSet = make(map[string]bool)
	knownMediaExtSet  = make(map[string]bool)
	imageExtSet       = make(map[string]bool)
	manifestExtSet    = make(map[string]bool)
	allowedExtSet     = make(map[string]bool)

	mimetypeToExtMap = map[string]string{
		// video
		"3gpp":              "3gp",
		"mp2t":              "ts",
		"mp4":               "mp4",
		"mpeg":              "mpeg",
		"mpegurl":           "m3u8",
		"quicktime":         "mov",
		"webm":              "webm",
		"vp9":               "vp9",
		"video/ogg":         "ogv",
		"x-flv":             "flv",
		"x-m4v":             "m4v",
		"x-matroska":        "mkv",
		"x-mng":             "mng",
		"x-mp4-fragmented":  "mp4",
		"x-ms-asf":          "asf",
		"x-ms-wmv":          "wmv",
		"x-msvideo":         "avi",
		"vnd.dlna.mpeg-tts": "mpeg",

		// application (streaming playlists)
		"dash+xml":          "mpd",
		"f4m+xml":           "f4m",
		"hds+xml":           "f4m",
		"vnd.apple.mpegurl": "m3u8",
		"vnd.ms-sstr+xml":   "ism",
		"x-mpegurl":         "m3u8",

		// audio
		"audio/mp4":        "m4a",
		"audio/mpeg":       "mp3",
		"audio/webm":       "webm",
		"audio/x-matroska": "mka",
		"audio/x-mpegurl":  "m3u",
		"aacp":             "aac",
		"flac":             "flac",
		"midi":             "mid",
		"ogg":              "ogg",
		"opus":             "opus",
		"wav":              "wav",
		"wave":             "wav",
		"x-aac":            "aac",
		"x-flac":           "flac",
		"x-m4a":            "m4a",
		"x-realaudio":      "ra",
		"x-wav":            "wav",

		// image
		"avif":         "avif",
		"bmp":          "bmp",
		"gif":          "gif",
		"heic":         "heic",
		"heif":         "heic",
		"jpeg":         "jpg",
		"jxl":          "jxl",
		"png":          "png",
		"svg+xml":      "svg",
		"tiff":         "tif",
		"vnd.wap.wbmp": "wbmp",
		"webp":         "webp",
		"x-icon":       "ico",
		"x-jng":        "jng",
		"x-ms-bmp":     "bmp",

		// caption
		"filmstrip+json": "fs",
		"smptett+xml":    "tt",
		"ttaf+xml":       "dfxp",
		"ttml+xml":       "ttml",
		"vtt":            "vtt",
		"x-ms-sami":      "sami",
		"x-subrip":       "srt",
		"x-srt":          "srt",

		// misc
		"gzip": "gz",
		"json": "json",
		"xml":  "xml",
		"zip":  "zip",
	}

	extToMimetypeMap = map[string]string{
		"mp4":  "video/mp4",
		"m4v":  "video/x-m4v",
		"mkv":  "video/x-matroska",
		"webm": "video/webm",
		"mov":  "video/quicktime",
		"flv":  "video/x-flv",
		"avi":  "video/x-msvideo",
		"wmv":  "video/x-ms-wmv",
		"ts":   "video/mp2t",
		"m2ts": "video/mp2t",
		"3gp":  "video/3gpp",
		"ogv":  "video/ogg",
		"mpeg": "video/mpeg",
		"mpg":  "video/mpeg",
		"m3u8": "application/vnd.apple.mpegurl",
		"mpd":  "application/dash+xml",
		"f4m":  "application/f4m+xml",
		"ism":  "application/vnd.ms-sstr+xml",
		"mp3":  "audio/mpeg",
		"m4a":  "audio/mp4",
		"aac":  "audio/aac",
		"flac": "audio/flac",
		"ogg":  "audio/ogg",
		"oga":  "audio/ogg",
		"opus": "audio/opus",
		"wav":  "audio/wav",
		"mka":  "audio/x-matroska",
		"weba": "audio/webm",
		"jpg":  "image/jpeg",
		"jpeg": "image/jpeg",
		"png":  "image/png",
		"webp": "image/webp",
		"gif":  "image/gif",
		"avif": "image/avif",
		"jxl":  "image/jxl",
		"heic": "image/heic",
		"svg":  "image/svg+xml",
		"bmp":  "image/bmp",
		"ico":  "image/x-icon",
		"tif":  "image/tiff",
		"tiff": "image/tiff",
		"vtt":  "text/vtt",
		"srt":  "application/x-subrip",
	}
)

func init() {
	for _, e := range videoExts {
		directMediaExtSet[e] = true
		knownMediaExtSet[e] = true
		allowedExtSet[e] = true
	}
	for _, e := range audioExts {
		directMediaExtSet[e] = true
		knownMediaExtSet[e] = true
		allowedExtSet[e] = true
	}
	for _, e := range manifestExts {
		manifestExtSet[e] = true
		knownMediaExtSet[e] = true
		allowedExtSet[e] = true
	}
	for _, e := range imageExts {
		imageExtSet[e] = true
		allowedExtSet[e] = true
	}
	for _, e := range subtitleExts {
		allowedExtSet[e] = true
	}
	for _, e := range internalExts {
		allowedExtSet[e] = true
	}
}

// MimetypeToExt converts a MIME type string to a canonical file extension (without leading dot),
// matching yt-dlp's mimetype2ext behavior.
func MimetypeToExt(mt string) string {
	mt = strings.TrimSpace(strings.ToLower(strings.SplitN(mt, ";", 2)[0]))
	if mt == "" {
		return ""
	}
	if ext, ok := mimetypeToExtMap[mt]; ok {
		return ext
	}
	slashIdx := strings.LastIndexByte(mt, '/')
	if slashIdx < 0 || slashIdx == len(mt)-1 {
		return ""
	}
	subtype := mt[slashIdx+1:]
	if ext, ok := mimetypeToExtMap[subtype]; ok {
		return ext
	}
	if plusIdx := strings.LastIndexByte(subtype, '+'); plusIdx >= 0 && plusIdx < len(subtype)-1 {
		suffix := subtype[plusIdx+1:]
		if ext, ok := mimetypeToExtMap[suffix]; ok {
			return ext
		}
	}
	// Reject generic non-media text/octet types from turning into arbitrary extensions
	if mt == "application/octet-stream" || mt == "binary/octet-stream" || strings.HasPrefix(mt, "text/") {
		return ""
	}
	return strings.ReplaceAll(subtype, "+", ".")
}

// ExtToMimetype maps an extension or URL to its MIME type.
func ExtToMimetype(extOrURL string) string {
	ext := DetermineExt(extOrURL, "")
	if ext == "" {
		ext = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(extOrURL)), ".")
	}
	if ext == "" {
		return ""
	}
	if mt, ok := extToMimetypeMap[ext]; ok {
		return mt
	}
	if mt := mime.TypeByExtension("." + ext); mt != "" {
		return strings.SplitN(mt, ";", 2)[0]
	}
	return ""
}

// DetermineExt extracts a file extension (without leading dot) from a URL or filename,
// matching yt-dlp's determine_ext (including trailing slash stripping for known extensions).
func DetermineExt(rawURL string, defaultExt string) string {
	if rawURL == "" {
		return defaultExt
	}
	beforeQuery := rawURL
	if qIdx := strings.IndexAny(beforeQuery, "?#"); qIdx >= 0 {
		beforeQuery = beforeQuery[:qIdx]
	}
	dotIdx := strings.LastIndexByte(beforeQuery, '.')
	if dotIdx < 0 || dotIdx == len(beforeQuery)-1 {
		return defaultExt
	}
	guess := strings.ToLower(beforeQuery[dotIdx+1:])
	if isAlphaNumericASCII(guess) {
		return guess
	}
	trimmed := strings.TrimRight(guess, "/")
	if knownMediaExtSet[trimmed] || imageExtSet[trimmed] {
		return trimmed
	}
	return defaultExt
}

// SanitizeExtension enforces yt-dlp's GHSA-79w7-vh3h-8g4j extension allowlist.
// Returns a clean extension with a leading dot (e.g. ".mp4").
func SanitizeExtension(ext string, fallback string) string {
	fallbackClean := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(fallback)), ".")
	if fallbackClean == "" {
		fallbackClean = "mp4"
	}
	clean := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(ext)), ".")
	if clean == "" || strings.ContainsAny(clean, `/\`) {
		return "." + fallbackClean
	}
	if dotIdx := strings.LastIndexByte(clean, '.'); dotIdx >= 0 {
		clean = clean[dotIdx+1:]
	}
	if clean == "bin" || !allowedExtSet[clean] {
		return "." + fallbackClean
	}
	return "." + clean
}

// IsKnownMediaExtension reports whether ext (with or without leading dot) is a known video, audio, or manifest extension.
func IsKnownMediaExtension(ext string) bool {
	clean := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(ext)), ".")
	return knownMediaExtSet[clean]
}

// IsDirectMediaExtension reports whether ext (with or without leading dot) is a direct video or audio container (not a manifest).
func IsDirectMediaExtension(ext string) bool {
	clean := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(ext)), ".")
	return directMediaExtSet[clean]
}

// IsManifestExtension reports whether ext (with or without leading dot) is a streaming manifest extension.
func IsManifestExtension(ext string) bool {
	clean := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(ext)), ".")
	return manifestExtSet[clean]
}

// IsImageExtension reports whether ext (with or without leading dot) is a known image extension.
func IsImageExtension(ext string) bool {
	clean := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(ext)), ".")
	return imageExtSet[clean]
}

// DispositionFilename extracts the filename parameter from a Content-Disposition header.
func DispositionFilename(headers http.Header) string {
	if headers == nil {
		return ""
	}
	cd := headers.Get("Content-Disposition")
	if cd == "" {
		return ""
	}
	_, params, err := mime.ParseMediaType(cd)
	if err != nil {
		return ""
	}
	if fn := strings.TrimSpace(params["filename*"]); fn != "" {
		return fn
	}
	return strings.TrimSpace(params["filename"])
}

// DetectExtFromHeaders implements yt-dlp's urlhandle_detect_ext priority chain:
// 1. Content-Disposition filename extension
// 2. x-amz-meta-name extension
// 3. x-amz-meta-file-type
// 4. Content-Type via MimetypeToExt
func DetectExtFromHeaders(headers http.Header, defaultExt string) string {
	if headers == nil {
		return defaultExt
	}
	if fn := DispositionFilename(headers); fn != "" {
		if ext := DetermineExt(fn, ""); ext != "" && allowedExtSet[ext] {
			return ext
		}
	}
	if amzName := strings.TrimSpace(headers.Get("X-Amz-Meta-Name")); amzName != "" {
		if ext := DetermineExt(amzName, ""); ext != "" && allowedExtSet[ext] {
			return ext
		}
	}
	if amzType := strings.TrimSpace(strings.ToLower(headers.Get("X-Amz-Meta-File-Type"))); amzType != "" {
		clean := strings.TrimPrefix(amzType, ".")
		if allowedExtSet[clean] {
			return clean
		}
	}
	if ct := headers.Get("Content-Type"); ct != "" {
		if ext := MimetypeToExt(ct); ext != "" && allowedExtSet[ext] {
			return ext
		}
	}
	return defaultExt
}

// IsDirectMediaContentType reports whether an HTTP Content-Type header indicates a direct audio/video/manifest response,
// matching yt-dlp's GenericIE Content-Type regex:
// (?P<type>audio|video|application(?=/(?:ogg$|(?:vnd\.apple\.|x-)?mpegurl)))/(?P<format_id>[^;\s]+)
func IsDirectMediaContentType(contentType string) bool {
	mt := strings.TrimSpace(strings.ToLower(strings.SplitN(contentType, ";", 2)[0]))
	if strings.HasPrefix(mt, "video/") || strings.HasPrefix(mt, "audio/") {
		return true
	}
	switch mt {
	case "application/ogg",
		"application/mpegurl",
		"application/x-mpegurl",
		"application/vnd.apple.mpegurl",
		"application/dash+xml",
		"application/f4m+xml":
		return true
	}
	return false
}

// IsDirectMediaURL reports whether u's path ends with a known direct video or audio extension.
func IsDirectMediaURL(u *url.URL) bool {
	if u == nil {
		return false
	}
	ext := DetermineExt(u.Path, "")
	if IsDirectMediaExtension(ext) {
		return true
	}
	host := strings.ToLower(u.Host)
	return strings.Contains(host, ".okcdn.ru")
}

func isAlphaNumericASCII(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}
