package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"pmv-dl/internal/common"
	"pmv-dl/internal/downloader"
	"pmv-dl/internal/extractor"
)

var (
	version = "v2.0.0"
)

type headerSlice []string

func (h *headerSlice) String() string {
	return strings.Join(*h, ", ")
}

func (h *headerSlice) Set(val string) error {
	*h = append(*h, val)
	return nil
}

type boolFlagValue interface {
	flag.Value
	IsBoolFlag() bool
}

// permuteArgs reorders CLI arguments so flags placed after positional URLs
// (e.g. "./pmv-dl <url> --cookies-from-browser chrome") are parsed by flag.Parse().
func permuteArgs(args []string) []string {
	var flags []string
	var positional []string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positional = append(positional, arg)
			continue
		}

		flags = append(flags, arg)
		trimmed := strings.TrimLeft(arg, "-")
		if strings.Contains(trimmed, "=") {
			continue
		}

		f := flag.CommandLine.Lookup(trimmed)
		if f == nil {
			continue
		}
		if bf, ok := f.Value.(boolFlagValue); ok && bf.IsBoolFlag() {
			continue
		}
		if i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}

	return append(flags, positional...)
}

func main() {
	var outDir string
	flag.StringVar(&outDir, "output", "./downloads", "Directory to save downloads, or '-' for stdout piping")
	flag.StringVar(&outDir, "o", "./downloads", "Alias for -output")

	quality := flag.String("quality", "1080", "Video quality (e.g., 1080, 720, 480, 360, best, low)")
	threads := flag.Int("threads", common.DefaultThreads, "Number of concurrent segment downloads (1-16)")
	showVersion := flag.Bool("version", false, "Print version and exit")

	// Authentication & Network flags
	cookies := flag.String("cookies", "", "Path to Netscape cookies file or raw cookie string")
	cookiesFromBrowser := flag.String("cookies-from-browser", "", "Load cookies from browser (e.g. chrome, firefox, brave, edge, chrome:Default)")
	var addHeaders headerSlice
	flag.Var(&addHeaders, "add-header", "Custom HTTP header in 'Name: Value' format (can be repeated)")
	flag.Var(&addHeaders, "header", "Alias for -add-header")

	// Downloader flags
	var downloaderName string
	flag.StringVar(&downloaderName, "downloader", "native", "Downloader to use: native, ffmpeg, aria2c, curl, wget (or protocol spec like m3u8:native,http:aria2c)")
	flag.StringVar(&downloaderName, "external-downloader", "native", "Alias for -downloader")

	var downloaderArgs string
	flag.StringVar(&downloaderArgs, "downloader-args", "", "Arguments to give to external downloader (e.g., 'ffmpeg:-loglevel warning')")
	flag.StringVar(&downloaderArgs, "external-downloader-args", "", "Alias for -downloader-args")

	// Postprocessing flags
	var extractAudio bool
	flag.BoolVar(&extractAudio, "x", false, "Convert video files to audio-only files (requires ffmpeg)")
	flag.BoolVar(&extractAudio, "extract-audio", false, "Alias for -x")

	audioFormat := flag.String("audio-format", "best", "Audio format when -x is used: best, mp3, aac, m4a, opus, flac, wav")
	audioQuality := flag.String("audio-quality", "5", "FFmpeg audio quality: 0-9 for VBR, or bitrate like 128k, 192k, 320k")
	remuxVideo := flag.String("remux-video", "", "Remux video into container format without re-encoding: mp4, mkv, mov, webm, ts")
	recodeVideo := flag.String("recode-video", "", "Re-encode video into format: mp4, mkv, webm, avi")

	var embedMetadata bool
	flag.BoolVar(&embedMetadata, "embed-metadata", false, "Embed metadata into the video file")
	flag.BoolVar(&embedMetadata, "add-metadata", false, "Alias for -embed-metadata")

	embedThumbnail := flag.Bool("embed-thumbnail", false, "Embed thumbnail in the video as cover art (mp4/mkv)")

	var keepVideo bool
	flag.BoolVar(&keepVideo, "k", false, "Keep the intermediate video file on disk after post-processing")
	flag.BoolVar(&keepVideo, "keep-video", false, "Alias for -k")

	execCmd := flag.String("exec", "", "Execute a command on the downloaded file; {} is replaced by the file path")

	var postProcArgs string
	flag.StringVar(&postProcArgs, "postprocessor-args", "", "Arguments to pass to postprocessors (e.g., 'ExtractAudio:-b:a 192k')")
	flag.StringVar(&postProcArgs, "ppa", "", "Alias for -postprocessor-args")

	ffmpegLocation := flag.String("ffmpeg-location", "", "Location of the ffmpeg binary or containing directory")

	// yt-dlp style playlist flags
	playlistItems := flag.String("playlist-items", "", "Playlist/gallery items to download (e.g., 1,2,5-8 or 1-5 or :5 or 3:)")
	playlistStart := flag.Int("playlist-start", 1, "Playlist/gallery item to start at (default 1)")
	playlistEnd := flag.Int("playlist-end", 0, "Playlist/gallery item to end at (default 0, unlimited)")
	noPlaylist := flag.Bool("no-playlist", false, "Download only the single item if URL contains both item and playlist/gallery context")
	yesPlaylist := flag.Bool("yes-playlist", false, "Download the playlist/gallery if the URL refers to a playlist/gallery")
	noImages := flag.Bool("no-images", false, "Skip downloading images in galleries")
	imagesOnly := flag.Bool("images-only", false, "Download only images in galleries (skip videos)")

	_ = flag.CommandLine.Parse(permuteArgs(os.Args[1:]))

	if *showVersion {
		fmt.Printf("pmv-dl %s\n", version)
		return
	}

	args := flag.Args()
	if len(args) < 1 {
		log.Fatalf("Usage: pmv-dl [flags] <url> (version %s)", version)
	}

	toStdout := strings.TrimSpace(outDir) == "-"
	if toStdout {
		log.SetOutput(os.Stderr)
	}

	rawURL := strings.TrimSpace(args[0])
	targetURL, err := common.ValidateRemoteURL(rawURL)
	if err != nil {
		log.Fatalf("Invalid URL: %v", err)
	}

	workerCount := *threads
	if workerCount < 1 {
		workerCount = 1
	} else if workerCount > common.MaxThreads {
		workerCount = common.MaxThreads
	}

	var cleanOutDir string
	if toStdout {
		cleanOutDir = "-"
	} else {
		cleanOutDir = filepath.Clean(outDir)
		if err := os.MkdirAll(cleanOutDir, 0755); err != nil {
			log.Fatalf("Failed to create output directory: %v", err)
		}
	}

	opts := common.DownloadOptions{
		OutputDir:      cleanOutDir,
		Quality:        *quality,
		Threads:        workerCount,
		PlaylistItems:  *playlistItems,
		PlaylistStart:  *playlistStart,
		PlaylistEnd:    *playlistEnd,
		NoPlaylist:     *noPlaylist,
		YesPlaylist:    *yesPlaylist,
		NoImages:           *noImages,
		ImagesOnly:         *imagesOnly,
		Cookies:            *cookies,
		CookiesFromBrowser: *cookiesFromBrowser,
		Headers:            addHeaders,
		Downloader:         downloaderName,
		DownloaderArgs: downloaderArgs,
		ToStdout:       toStdout,
		ExtractAudio:   extractAudio,
		AudioFormat:    *audioFormat,
		AudioQuality:   *audioQuality,
		RemuxVideo:     *remuxVideo,
		RecodeVideo:    *recodeVideo,
		EmbedMetadata:  embedMetadata,
		EmbedThumbnail: *embedThumbnail,
		KeepVideo:      keepVideo,
		ExecCmd:        *execCmd,
		PostProcArgs:   postProcArgs,
		FFmpegLocation: *ffmpegLocation,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	go func() {
		select {
		case <-sigChan:
			fmt.Fprintln(os.Stderr, "\nAborting download gracefully...")
			cancel()
		case <-ctx.Done():
		}
	}()

	client, err := common.NewHTTPClient()
	if err != nil {
		log.Fatalf("Failed to initialize HTTP client: %v", err)
	}

	if err := common.ApplyOptionsToClient(client, opts, targetURL); err != nil {
		log.Printf("Warning: %v", err)
	}

	extr := extractor.Find(targetURL)
	if extr == nil {
		var supported []string
		for _, e := range extractor.All() {
			supported = append(supported, fmt.Sprintf("%s (%s)", e.Name(), e.Description()))
		}
		log.Fatalf("No supported extractor found for URL %q.\nAvailable extractors:\n- %s", rawURL, strings.Join(supported, "\n- "))
	}

	res, err := extr.Extract(ctx, client, targetURL, opts)
	if err != nil {
		log.Fatalf("Extraction failed: %v", err)
	}

	if err := downloader.Download(ctx, client, res, opts); err != nil {
		if ctx.Err() != nil {
			log.Fatalf("\nDownload aborted.")
		}
		log.Fatalf("\nDownload failed: %v", err)
	}
}
