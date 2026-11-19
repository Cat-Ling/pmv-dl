package downloader

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"pmv-dl/internal/extractor"
)

// SelectDownloader chooses an appropriate downloader ("native", "ffmpeg", "aria2c", "curl", "wget")
// based on the stream protocol ("m3u8", "http") and the user's --downloader specification.
// Format can be a simple name like "aria2c" or protocol mapping like "m3u8:native,http:aria2c".
func SelectDownloader(protocol string, spec string) string {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "native"
	}

	proto := strings.ToLower(strings.TrimSpace(protocol))
	if proto == "https" {
		proto = "http"
	}

	parts := strings.Split(spec, ",")
	defaultDownloader := ""

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if idx := strings.Index(part, ":"); idx != -1 {
			keys := strings.Split(strings.ToLower(part[:idx]), "+")
			downloaderName := strings.TrimSpace(strings.ToLower(part[idx+1:]))
			for _, k := range keys {
				k = strings.TrimSpace(k)
				if k == proto || (k == "hls" && proto == "m3u8") {
					return downloaderName
				}
				if k == "default" {
					defaultDownloader = downloaderName
				}
			}
		} else {
			defaultDownloader = strings.ToLower(part)
		}
	}

	if defaultDownloader != "" {
		return defaultDownloader
	}
	return "native"
}

// ParseDownloaderArgs parses --downloader-args specification into a map of downloader name to arguments.
// Example: "ffmpeg:-loglevel warning -stats" or "aria2c:-j8 -s8".
func ParseDownloaderArgs(argsSpec string) map[string][]string {
	result := make(map[string][]string)
	argsSpec = strings.TrimSpace(argsSpec)
	if argsSpec == "" {
		return result
	}

	if idx := strings.Index(argsSpec, ":"); idx != -1 {
		name := strings.TrimSpace(strings.ToLower(argsSpec[:idx]))
		rawArgs := strings.TrimSpace(argsSpec[idx+1:])
		if rawArgs != "" {
			result[name] = strings.Fields(rawArgs)
		}
	} else {
		result["default"] = strings.Fields(argsSpec)
	}
	return result
}

// FindExecutable locates a binary by checking an optional custom path or looking in PATH.
func FindExecutable(name string, customPath string) (string, error) {
	if customPath != "" {
		if fi, err := os.Stat(customPath); err == nil && !fi.IsDir() {
			return customPath, nil
		}
		// Also check if directory was given
		binPath := filepath.Join(customPath, name)
		if fi, err := os.Stat(binPath); err == nil && !fi.IsDir() {
			return binPath, nil
		}
	}

	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%s executable not found in PATH: %w", name, err)
	}
	return path, nil
}

// RunFFmpegDownloader invokes ffmpeg to download or stream media.
func RunFFmpegDownloader(ctx context.Context, vr *extractor.VideoResult, streamURL string, outputFile string, toStdout bool, extraArgs []string, customFFmpeg string) error {
	bin, err := FindExecutable("ffmpeg", customFFmpeg)
	if err != nil {
		return err
	}

	var args []string
	args = append(args, "-y", "-hide_banner")

	// Pass HTTP headers if present
	var headerList []string
	if vr != nil {
		if vr.Referer != "" {
			headerList = append(headerList, fmt.Sprintf("Referer: %s\r\n", vr.Referer))
		}
		for k, v := range vr.Headers {
			headerList = append(headerList, fmt.Sprintf("%s: %s\r\n", k, v))
		}
	}
	if len(headerList) > 0 {
		args = append(args, "-headers", strings.Join(headerList, ""))
	}

	// Extra input arguments
	args = append(args, "-i", streamURL, "-c", "copy")
	args = append(args, extraArgs...)

	var cmd *exec.Cmd
	if toStdout {
		// Output streamable mpegts to pipe:1 (standard output)
		args = append(args, "-f", "mpegts", "pipe:1")
		cmd = exec.CommandContext(ctx, bin, args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	} else {
		args = append(args, outputFile)
		cmd = exec.CommandContext(ctx, bin, args...)
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
	}

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg downloader failed: %w", err)
	}
	return nil
}

// RunAria2cDownloader invokes aria2c to download direct progressive media streams.
func RunAria2cDownloader(ctx context.Context, vr *extractor.VideoResult, streamURL string, outputFile string, extraArgs []string) error {
	bin, err := FindExecutable("aria2c", "")
	if err != nil {
		return err
	}

	outDir := filepath.Dir(outputFile)
	outBase := filepath.Base(outputFile)

	args := []string{
		"-x16", "-s16", "-j16", "--min-split-size=1M",
		"--auto-file-renaming=false", "--allow-overwrite=true",
		"-d", outDir, "-o", outBase,
	}

	if vr != nil {
		if vr.Referer != "" {
			args = append(args, fmt.Sprintf("--header=Referer: %s", vr.Referer))
		}
		for k, v := range vr.Headers {
			args = append(args, fmt.Sprintf("--header=%s: %s", k, v))
		}
	}

	args = append(args, extraArgs...)
	args = append(args, streamURL)

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("aria2c downloader failed: %w", err)
	}
	return nil
}

// RunCurlDownloader invokes curl to download media.
func RunCurlDownloader(ctx context.Context, vr *extractor.VideoResult, streamURL string, outputFile string, toStdout bool, extraArgs []string) error {
	bin, err := FindExecutable("curl", "")
	if err != nil {
		return err
	}

	args := []string{"-L"}
	if toStdout {
		args = append(args, "-s", "-o", "-")
	} else {
		args = append(args, "--progress-bar", "-o", outputFile)
	}

	if vr != nil {
		if vr.Referer != "" {
			args = append(args, "-e", vr.Referer)
		}
		for k, v := range vr.Headers {
			args = append(args, "-H", fmt.Sprintf("%s: %s", k, v))
		}
	}

	args = append(args, extraArgs...)
	args = append(args, streamURL)

	cmd := exec.CommandContext(ctx, bin, args...)
	if toStdout {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
	}

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("curl downloader failed: %w", err)
	}
	return nil
}

// RunWgetDownloader invokes wget to download media.
func RunWgetDownloader(ctx context.Context, vr *extractor.VideoResult, streamURL string, outputFile string, toStdout bool, extraArgs []string) error {
	bin, err := FindExecutable("wget", "")
	if err != nil {
		return err
	}

	args := []string{}
	if toStdout {
		args = append(args, "-q", "-O", "-")
	} else {
		args = append(args, "--show-progress", "-O", outputFile)
	}

	if vr != nil {
		if vr.Referer != "" {
			args = append(args, fmt.Sprintf("--header=Referer: %s", vr.Referer))
		}
		for k, v := range vr.Headers {
			args = append(args, fmt.Sprintf("--header=%s: %s", k, v))
		}
	}

	args = append(args, extraArgs...)
	args = append(args, streamURL)

	cmd := exec.CommandContext(ctx, bin, args...)
	if toStdout {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
	}

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("wget downloader failed: %w", err)
	}
	return nil
}
