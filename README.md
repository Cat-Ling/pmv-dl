# pmv-dl

A command-line media downloader written in Go supporting videos, galleries, playlists, and direct streams.

Supported services include PMVHaven, PMVClub / GoonChan, PMVHero, FapTap, PMVTube, Gig.sex, NoodleMagazine / Mat6tube, Spreee, ProstoPorno, StarWanks, Ebalovo, Ruvideos, PornHao, Eporner, HeavyFetish, Yandex Video, and generic web/HLS streams.

## Usage

```bash
./pmv-dl [flags] <url>
```

### Flags

| Flag | Default | Description |
| :--- | :--- | :--- |
| `-output`, `-o` | `./downloads` | Output directory, or `-` to stream to stdout |
| `-quality` | `1080` | Target video quality (`1080`, `720`, `480`, `360`, `best`, `low`) |
| `-threads` | `4` | Concurrent segment/image download workers (`1`–`16`) |
| `-cookies` | `""` | Path to Netscape cookies file or raw cookie string |
| `-cookies-from-browser` | `""` | Load cookies from browser (e.g. `chrome`, `firefox`, `brave`, `edge`, `chrome:Default`) |
| `-add-header`, `-header` | `""` | Custom HTTP header in `Name: Value` format (repeatable) |
| `-downloader`, `-external-downloader` | `native` | Downloader: `native`, `ffmpeg`, `aria2c`, `curl`, `wget`, or protocol map (`m3u8:native,http:aria2c`) |
| `-downloader-args`, `-external-downloader-args` | `""` | Extra arguments for external downloaders (e.g. `ffmpeg:-loglevel warning`) |
| `-x`, `-extract-audio` | `false` | Extract audio only (requires `ffmpeg`) |
| `-audio-format` | `best` | Target audio format (`best`, `mp3`, `aac`, `m4a`, `opus`, `flac`, `wav`) |
| `-audio-quality` | `5` | FFmpeg audio quality (`0`–`9` VBR or bitrate like `192k`) |
| `-remux-video` | `""` | Remux container without re-encoding (`mp4`, `mkv`, `mov`, `webm`, `ts`) |
| `-recode-video` | `""` | Re-encode video (`mp4`, `mkv`, `webm`, `avi`) |
| `-embed-metadata`, `-add-metadata` | `false` | Embed metadata into the output file |
| `-embed-thumbnail` | `false` | Embed thumbnail as cover art (`mp4`, `mkv`) |
| `-k`, `-keep-video` | `false` | Keep intermediate video file after post-processing |
| `-exec` | `""` | Run a command on the downloaded file (`{}` replaced by file path) |
| `-postprocessor-args`, `-ppa` | `""` | Extra arguments for postprocessors (e.g. `ExtractAudio:-b:a 192k`) |
| `-ffmpeg-location` | `""` | Path to `ffmpeg` binary or containing directory |
| `-playlist-items` | `""` | Playlist indices/ranges to download (e.g. `1,2,5-8`, `:5`, `3:`) |
| `-playlist-start` | `1` | Playlist index to start at (1-based) |
| `-playlist-end` | `0` | Playlist index to stop at (`0` for unlimited) |
| `-no-playlist` | `false` | Download only the single item if URL references both item and playlist |
| `-yes-playlist` | `false` | Download the full playlist/gallery |
| `-no-images` | `false` | Skip images in galleries |
| `-images-only` | `false` | Download only images in galleries |
| `-version` | `false` | Print version and exit |

### Examples

```bash
# Download a video at default quality (up to 1080p)
./pmv-dl "https://example.com/video/123"

# Stream directly to stdout and play in mpv
./pmv-dl -o - "https://example.com/video/123" | mpv -

# Select quality, threads, and custom output directory
./pmv-dl -quality 720 -threads 8 -o ./media "https://example.com/video/123"

# Extract audio as MP3
./pmv-dl -x --audio-format mp3 --audio-quality 192k "https://example.com/video/123"

# Download specific playlist items using browser cookies
./pmv-dl --cookies-from-browser chrome -playlist-items 1,3-5 "https://example.com/playlist/123"
```

## Building

Requires Go 1.22+.

```bash
./build.sh
```

Or build directly:

```bash
CGO_ENABLED=0 go build -ldflags="-s -w" -trimpath -o pmv-dl main.go
```

## License

Licensed under the [MIT License](LICENSE).
