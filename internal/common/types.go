package common

import "time"

const (
	MaxHTMLBytes     = 16 << 20  // 16 MiB
	MaxPlaylistBytes = 8 << 20   // 8 MiB
	MaxSegmentBytes  = 512 << 20 // 512 MiB
	ProbeSampleSize  = 16384     // 16 KiB
	MPEGTSPacketSize = 188
	MPEGTSSyncByte   = 0x47
	DefaultThreads   = 4
	MaxThreads       = 16
	MaxRetries       = 3
	UserAgent        = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	DefaultTimeout   = 30 * time.Second
)

// DownloadOptions holds CLI flags and runtime parameters.
type DownloadOptions struct {
	OutputDir     string
	Quality       string
	Threads       int
	PlaylistItems string
	PlaylistStart int
	PlaylistEnd   int
	NoPlaylist    bool
	YesPlaylist   bool
	NoImages      bool
	ImagesOnly    bool

	// Network & Authentication
	Cookies            string   // path to cookie file or raw cookie string
	CookiesFromBrowser string   // browser spec (e.g. "chrome", "firefox", "brave:Default")
	Headers            []string // custom headers (e.g. "Header: Value")

	// Downloader selection & arguments
	Downloader     string // e.g. "native", "ffmpeg", "aria2c", or "m3u8:native,http:aria2c"
	DownloaderArgs string // e.g. "ffmpeg:-loglevel warning"

	// Piping
	ToStdout bool // true when output is "-" or -o -

	// Postprocessing options
	ExtractAudio   bool   // -x, --extract-audio
	AudioFormat    string // --audio-format (best, mp3, aac, m4a, opus, flac, wav)
	AudioQuality   string // --audio-quality (0-9 or 128k, 192k, 320k)
	RemuxVideo     string // --remux-video (mp4, mkv, mov, webm, ts)
	RecodeVideo    string // --recode-video (mp4, mkv, webm, avi)
	EmbedMetadata  bool   // --embed-metadata, --add-metadata
	EmbedThumbnail bool   // --embed-thumbnail
	KeepVideo      bool   // -k, --keep-video
	ExecCmd        string // --exec CMD
	PostProcArgs   string // --postprocessor-args, --ppa
	FFmpegLocation string // --ffmpeg-location
}
