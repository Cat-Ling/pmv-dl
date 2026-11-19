package postprocessor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"pmv-dl/internal/common"
)

// MediaInfo contains metadata and current file state for postprocessing.
type MediaInfo struct {
	FilePath     string
	Title        string
	ThumbnailURL string
	Referer      string
}

// PostProcessor defines the interface for running post-processing steps.
type PostProcessor interface {
	Name() string
	Run(ctx context.Context, info *MediaInfo, opts common.DownloadOptions) error
}

// RunPipeline executes enabled postprocessors sequentially on the downloaded media.
func RunPipeline(ctx context.Context, info *MediaInfo, opts common.DownloadOptions) error {
	if info == nil || info.FilePath == "" || info.FilePath == "-" || opts.ToStdout {
		return nil
	}

	pipeline := []PostProcessor{
		&EmbedMetadataPP{},
		&EmbedThumbnailPP{},
		&RemuxVideoPP{},
		&RecodeVideoPP{},
		&ExtractAudioPP{},
		&ExecPP{},
	}

	for _, pp := range pipeline {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := pp.Run(ctx, info, opts); err != nil {
			return fmt.Errorf("[%s] postprocessing failed: %w", pp.Name(), err)
		}
	}

	return nil
}

// ExtractAudioPP converts a video into an audio-only file.
type ExtractAudioPP struct{}

func (pp *ExtractAudioPP) Name() string { return "ExtractAudio" }

func (pp *ExtractAudioPP) Run(ctx context.Context, info *MediaInfo, opts common.DownloadOptions) error {
	if !opts.ExtractAudio {
		return nil
	}

	ffmpegBin, err := findFFmpeg(opts.FFmpegLocation)
	if err != nil {
		return err
	}

	targetExt := strings.ToLower(strings.TrimSpace(opts.AudioFormat))
	if targetExt == "" || targetExt == "best" {
		targetExt = "mp3"
	}

	dir := filepath.Dir(info.FilePath)
	base := strings.TrimSuffix(filepath.Base(info.FilePath), filepath.Ext(info.FilePath))
	outAudio := filepath.Join(dir, base+"."+targetExt)

	fmt.Fprintf(os.Stderr, "[ExtractAudio] Converting %s to %s audio: %s\n", filepath.Base(info.FilePath), targetExt, outAudio)

	args := []string{"-y", "-hide_banner", "-i", info.FilePath, "-vn"}

	switch targetExt {
	case "mp3":
		args = append(args, "-acodec", "libmp3lame")
	case "aac", "m4a":
		args = append(args, "-acodec", "aac")
	case "opus":
		args = append(args, "-acodec", "libopus")
	case "flac":
		args = append(args, "-acodec", "flac")
	case "wav":
		args = append(args, "-acodec", "pcm_s16le")
	default:
		args = append(args, "-acodec", "libmp3lame")
	}

	// Audio quality handling
	if q := strings.TrimSpace(opts.AudioQuality); q != "" {
		if strings.HasSuffix(strings.ToLower(q), "k") {
			args = append(args, "-b:a", q)
		} else {
			args = append(args, "-q:a", q)
		}
	}

	if extra := parsePPA("ExtractAudio", opts.PostProcArgs); len(extra) > 0 {
		args = append(args, extra...)
	}

	args = append(args, outAudio)

	cmd := exec.CommandContext(ctx, ffmpegBin, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return err
	}

	if !opts.KeepVideo {
		_ = os.Remove(info.FilePath)
	}

	info.FilePath = outAudio
	return nil
}

// RemuxVideoPP remuxes the video stream into another container format without re-encoding.
type RemuxVideoPP struct{}

func (pp *RemuxVideoPP) Name() string { return "VideoRemuxer" }

func (pp *RemuxVideoPP) Run(ctx context.Context, info *MediaInfo, opts common.DownloadOptions) error {
	targetExt := strings.ToLower(strings.TrimSpace(opts.RemuxVideo))
	if targetExt == "" {
		return nil
	}

	currentExt := strings.ToLower(strings.TrimPrefix(filepath.Ext(info.FilePath), "."))
	if currentExt == targetExt {
		return nil
	}

	ffmpegBin, err := findFFmpeg(opts.FFmpegLocation)
	if err != nil {
		return err
	}

	dir := filepath.Dir(info.FilePath)
	base := strings.TrimSuffix(filepath.Base(info.FilePath), filepath.Ext(info.FilePath))
	outRemux := filepath.Join(dir, base+"."+targetExt)

	fmt.Fprintf(os.Stderr, "[VideoRemuxer] Remuxing %s to %s: %s\n", currentExt, targetExt, outRemux)

	args := []string{"-y", "-hide_banner", "-i", info.FilePath, "-c", "copy"}
	if extra := parsePPA("VideoRemuxer", opts.PostProcArgs); len(extra) > 0 {
		args = append(args, extra...)
	}
	args = append(args, outRemux)

	cmd := exec.CommandContext(ctx, ffmpegBin, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return err
	}

	if !opts.KeepVideo {
		_ = os.Remove(info.FilePath)
	}

	info.FilePath = outRemux
	return nil
}

// RecodeVideoPP re-encodes the video into another container format.
type RecodeVideoPP struct{}

func (pp *RecodeVideoPP) Name() string { return "VideoConvertor" }

func (pp *RecodeVideoPP) Run(ctx context.Context, info *MediaInfo, opts common.DownloadOptions) error {
	targetExt := strings.ToLower(strings.TrimSpace(opts.RecodeVideo))
	if targetExt == "" {
		return nil
	}

	currentExt := strings.ToLower(strings.TrimPrefix(filepath.Ext(info.FilePath), "."))
	if currentExt == targetExt {
		return nil
	}

	ffmpegBin, err := findFFmpeg(opts.FFmpegLocation)
	if err != nil {
		return err
	}

	dir := filepath.Dir(info.FilePath)
	base := strings.TrimSuffix(filepath.Base(info.FilePath), filepath.Ext(info.FilePath))
	outRecode := filepath.Join(dir, base+"."+targetExt)

	fmt.Fprintf(os.Stderr, "[VideoConvertor] Re-encoding %s to %s: %s\n", currentExt, targetExt, outRecode)

	args := []string{"-y", "-hide_banner", "-i", info.FilePath}

	switch targetExt {
	case "webm":
		args = append(args, "-c:v", "libvpx-vp9", "-c:a", "libopus")
	case "avi":
		args = append(args, "-c:v", "libxvid", "-c:a", "mp3")
	default:
		args = append(args, "-c:v", "libx264", "-c:a", "aac")
	}

	if extra := parsePPA("VideoConvertor", opts.PostProcArgs); len(extra) > 0 {
		args = append(args, extra...)
	}
	args = append(args, outRecode)

	cmd := exec.CommandContext(ctx, ffmpegBin, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return err
	}

	if !opts.KeepVideo {
		_ = os.Remove(info.FilePath)
	}

	info.FilePath = outRecode
	return nil
}

// EmbedMetadataPP embeds video title and artist metadata into the media file.
type EmbedMetadataPP struct{}

func (pp *EmbedMetadataPP) Name() string { return "EmbedMetadata" }

func (pp *EmbedMetadataPP) Run(ctx context.Context, info *MediaInfo, opts common.DownloadOptions) error {
	if !opts.EmbedMetadata || info.Title == "" {
		return nil
	}

	ffmpegBin, err := findFFmpeg(opts.FFmpegLocation)
	if err != nil {
		return err
	}

	tempFile := info.FilePath + ".meta.tmp" + filepath.Ext(info.FilePath)
	args := []string{
		"-y", "-hide_banner", "-i", info.FilePath,
		"-c", "copy",
		"-metadata", fmt.Sprintf("title=%s", info.Title),
	}

	if extra := parsePPA("EmbedMetadata", opts.PostProcArgs); len(extra) > 0 {
		args = append(args, extra...)
	}
	args = append(args, tempFile)

	cmd := exec.CommandContext(ctx, ffmpegBin, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		_ = os.Remove(tempFile)
		return err
	}

	if err := os.Rename(tempFile, info.FilePath); err != nil {
		_ = os.Remove(tempFile)
		return err
	}

	fmt.Fprintf(os.Stderr, "[EmbedMetadata] Embedded metadata into %s\n", filepath.Base(info.FilePath))
	return nil
}

// EmbedThumbnailPP embeds cover art image into supported video containers (mp4, mkv).
type EmbedThumbnailPP struct{}

func (pp *EmbedThumbnailPP) Name() string { return "EmbedThumbnail" }

func (pp *EmbedThumbnailPP) Run(ctx context.Context, info *MediaInfo, opts common.DownloadOptions) error {
	if !opts.EmbedThumbnail || info.ThumbnailURL == "" {
		return nil
	}

	ext := strings.ToLower(filepath.Ext(info.FilePath))
	if ext != ".mp4" && ext != ".mkv" {
		return nil
	}

	ffmpegBin, err := findFFmpeg(opts.FFmpegLocation)
	if err != nil {
		return err
	}

	// Fetch thumbnail
	thumbReq, err := http.NewRequestWithContext(ctx, "GET", info.ThumbnailURL, nil)
	if err != nil {
		return nil
	}
	thumbReq.Header.Set("User-Agent", common.UserAgent)
	if info.Referer != "" {
		thumbReq.Header.Set("Referer", info.Referer)
	}

	client, err := common.NewHTTPClient()
	if err != nil {
		return nil
	}
	resp, err := client.Do(thumbReq)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return nil
	}
	defer resp.Body.Close()

	thumbFile := info.FilePath + ".thumb.jpg"
	tf, err := os.OpenFile(thumbFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return nil
	}
	_, copyErr := io.Copy(tf, resp.Body)
	_ = tf.Close()
	if copyErr != nil {
		_ = os.Remove(thumbFile)
		return nil
	}
	defer os.Remove(thumbFile)

	tempFile := info.FilePath + ".thumb.tmp" + ext
	var args []string
	if ext == ".mp4" {
		args = []string{
			"-y", "-hide_banner", "-i", info.FilePath, "-i", thumbFile,
			"-map", "0", "-map", "1",
			"-c", "copy",
			"-disposition:v:1", "attached_pic",
		}
	} else {
		args = []string{
			"-y", "-hide_banner", "-i", info.FilePath,
			"-attach", thumbFile,
			"-metadata:s:t", "mimetype=image/jpeg",
			"-c", "copy",
		}
	}

	args = append(args, tempFile)
	cmd := exec.CommandContext(ctx, ffmpegBin, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		_ = os.Remove(tempFile)
		return nil
	}

	if err := os.Rename(tempFile, info.FilePath); err != nil {
		_ = os.Remove(tempFile)
		return nil
	}

	fmt.Fprintf(os.Stderr, "[EmbedThumbnail] Embedded thumbnail into %s\n", filepath.Base(info.FilePath))
	return nil
}

// ExecPP executes an arbitrary shell command with `{}` replaced by the media file path.
type ExecPP struct{}

func (pp *ExecPP) Name() string { return "Exec" }

func (pp *ExecPP) Run(ctx context.Context, info *MediaInfo, opts common.DownloadOptions) error {
	rawCmd := strings.TrimSpace(opts.ExecCmd)
	if rawCmd == "" {
		return nil
	}

	var parsedCmd string
	if strings.Contains(rawCmd, "{}") {
		parsedCmd = strings.ReplaceAll(rawCmd, "{}", quoteShell(info.FilePath))
	} else {
		parsedCmd = rawCmd + " " + quoteShell(info.FilePath)
	}

	fmt.Fprintf(os.Stderr, "[Exec] Executing: %s\n", parsedCmd)

	cmd := exec.CommandContext(ctx, "sh", "-c", parsedCmd)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

func findFFmpeg(customPath string) (string, error) {
	if customPath != "" {
		if fi, err := os.Stat(customPath); err == nil && !fi.IsDir() {
			return customPath, nil
		}
		bin := filepath.Join(customPath, "ffmpeg")
		if fi, err := os.Stat(bin); err == nil && !fi.IsDir() {
			return bin, nil
		}
	}

	p, err := exec.LookPath("ffmpeg")
	if err != nil {
		return "", fmt.Errorf("ffmpeg not found in PATH: %w", err)
	}
	return p, nil
}

func parsePPA(ppName string, ppaSpec string) []string {
	ppaSpec = strings.TrimSpace(ppaSpec)
	if ppaSpec == "" {
		return nil
	}

	if idx := strings.Index(ppaSpec, ":"); idx != -1 {
		prefix := strings.TrimSpace(ppaSpec[:idx])
		if isPPKey(prefix) {
			if strings.EqualFold(prefix, ppName) || strings.EqualFold(prefix, "ffmpeg") || strings.EqualFold(prefix, "default") {
				return strings.Fields(ppaSpec[idx+1:])
			}
			return nil
		}
	}
	return strings.Fields(ppaSpec)
}

func isPPKey(s string) bool {
	if s == "" || strings.HasPrefix(s, "-") {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '+') {
			return false
		}
	}
	return true
}

func quoteShell(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
