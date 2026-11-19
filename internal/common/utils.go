package common

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxFilenameBytes = 200

var (
	reInvalidChr = regexp.MustCompile(`[<>:"/\\|?*]`)
	reWhitespace = regexp.MustCompile(`\s+`)
)

func SanitizeFilename(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == 0 {
			return -1
		}
		return r
	}, name)

	name = reInvalidChr.ReplaceAllString(name, "_")
	name = reWhitespace.ReplaceAllString(name, " ")
	name = strings.Trim(name, " .")

	if runes := []rune(name); len(runes) > maxFilenameBytes {
		name = string(runes[:maxFilenameBytes])
	}
	for len(name) > maxFilenameBytes {
		_, size := utf8.DecodeLastRuneInString(name)
		if size <= 0 {
			name = name[:maxFilenameBytes]
			break
		}
		name = name[:len(name)-size]
	}
	name = strings.Trim(name, " .")

	if name == "" || name == "." || name == ".." {
		return "video"
	}

	stem := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	switch stem {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		name = "_" + name
	}

	return name
}

func NormalizeString(s string) string {
	return strings.NewReplacer(
		`\u002F`, "/",
		`\u002f`, "/",
		`\/`, "/",
		`\u0026`, "&",
		`&amp;`, "&",
	).Replace(s)
}

func FormatBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit && exp < 5; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func FormatProgressBar(percent float64, length int) string {
	if percent < 0 {
		percent = 0
	}
	filled := int((percent / 100.0) * float64(length))
	if filled > length {
		filled = length
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", length-filled)
}

// ParsePlaylistIndices parses yt-dlp style playlist item selectors (e.g., "1,2,5-8", "1-5", ":5", "3:").
func ParsePlaylistIndices(spec string, total int, startFlag int, endFlag int) map[int]bool {
	selected := make(map[int]bool)

	if strings.TrimSpace(spec) == "" {
		s := startFlag
		if s < 1 {
			s = 1
		}
		e := total
		if endFlag > 0 && endFlag < total {
			e = endFlag
		}
		for i := s; i <= e; i++ {
			selected[i] = true
		}
		return selected
	}

	parts := strings.Split(spec, ",")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.Contains(p, "-") {
			rangeParts := strings.SplitN(p, "-", 2)
			startStr, endStr := strings.TrimSpace(rangeParts[0]), strings.TrimSpace(rangeParts[1])
			startVal, err1 := strconv.Atoi(startStr)
			endVal, err2 := strconv.Atoi(endStr)
			if err1 == nil && err2 == nil {
				for i := startVal; i <= endVal; i++ {
					if i >= 1 && i <= total {
						selected[i] = true
					}
				}
			}
		} else if strings.Contains(p, ":") {
			rangeParts := strings.SplitN(p, ":", 2)
			startStr, endStr := strings.TrimSpace(rangeParts[0]), strings.TrimSpace(rangeParts[1])
			sVal := 1
			if startStr != "" {
				if v, err := strconv.Atoi(startStr); err == nil {
					sVal = v
				}
			}
			eVal := total
			if endStr != "" {
				if v, err := strconv.Atoi(endStr); err == nil {
					eVal = v
				}
			}
			for i := sVal; i <= eVal; i++ {
				if i >= 1 && i <= total {
					selected[i] = true
				}
			}
		} else {
			if v, err := strconv.Atoi(p); err == nil {
				if v >= 1 && v <= total {
					selected[v] = true
				}
			}
		}
	}

	if startFlag > 1 || endFlag > 0 {
		for i := range selected {
			if startFlag > 1 && i < startFlag {
				delete(selected, i)
			}
			if endFlag > 0 && i > endFlag {
				delete(selected, i)
			}
		}
	}

	return selected
}
