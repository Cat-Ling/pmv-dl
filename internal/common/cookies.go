package common

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	httpOnlyPrefix     = "#HttpOnly_"
	windowsEpochOffset = 11644473600
)

type BrowserSpec struct {
	Browser   string
	Keyring   string
	Profile   string
	Container string
}

var reBrowserSpec = regexp.MustCompile(`^(?P<name>[a-zA-Z0-9_-]+)(?:\+(?P<keyring>[a-zA-Z0-9_-]+))?(?::(?P<profile>[^:]+?))?(?:::(?P<container>.+))?$`)

func ParseBrowserSpec(spec string) (BrowserSpec, error) {
	spec = strings.TrimSpace(spec)
	m := reBrowserSpec.FindStringSubmatch(spec)
	if m == nil {
		return BrowserSpec{}, fmt.Errorf("invalid browser specification %q", spec)
	}
	return BrowserSpec{
		Browser:   strings.ToLower(m[1]),
		Keyring:   strings.ToLower(m[2]),
		Profile:   m[3],
		Container: m[4],
	}, nil
}

// DomainMatches implements RFC 6265 section 5.1.3 domain matching.
func DomainMatches(cookieDomain, host string) bool {
	cd := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(cookieDomain), "."))
	h := strings.ToLower(strings.TrimSpace(host))
	if cd == "" || h == "" {
		return false
	}
	if cd == h {
		return true
	}
	if net.ParseIP(h) != nil {
		return false
	}
	return strings.HasSuffix(h, "."+cd)
}

var multiPartTLDs = map[string]bool{
	"co.uk": true, "org.uk": true, "ac.uk": true, "gov.uk": true,
	"com.au": true, "net.au": true, "org.au": true,
	"co.jp": true, "ne.jp": true, "or.jp": true,
	"co.nz": true, "com.br": true, "com.mx": true,
	"co.in": true, "co.kr": true, "co.za": true,
}

func RegistrableDomain(host string) string {
	h := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(host), "."))
	if h == "" || net.ParseIP(h) != nil {
		return h
	}
	parts := strings.Split(h, ".")
	if len(parts) <= 2 {
		return h
	}
	lastTwo := parts[len(parts)-2] + "." + parts[len(parts)-1]
	if multiPartTLDs[lastTwo] && len(parts) >= 3 {
		return parts[len(parts)-3] + "." + lastTwo
	}
	return lastTwo
}

func SameRegistrableDomain(hostA, hostB string) bool {
	a := RegistrableDomain(hostA)
	b := RegistrableDomain(hostB)
	return a != "" && a == b
}

// NormalizeCookieExpiry converts Chrome (Windows FILETIME microseconds), Firefox (milliseconds),
// or Unix second timestamps into a UTC time.Time and reports whether the cookie is still valid.
func NormalizeCookieExpiry(raw int64, now time.Time) (time.Time, bool) {
	if raw <= 0 {
		return time.Time{}, true
	}
	sec := raw
	if sec > 10_000_000_000_000 {
		sec = (sec / 1_000_000) - windowsEpochOffset
	} else if sec > 100_000_000_000 {
		sec = sec / 1000
	}
	if sec <= 0 {
		return time.Time{}, false
	}
	exp := time.Unix(sec, 0).UTC()
	if !now.IsZero() && exp.Before(now) {
		return exp, false
	}
	return exp, true
}

func sanitizeRFC6265CookieValue(val string) string {
	val = strings.TrimSpace(val)
	if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
		val = val[1 : len(val)-1]
	}
	var b strings.Builder
	b.Grow(len(val))
	for i := 0; i < len(val); i++ {
		c := val[i]
		if c >= 0x20 && c < 0x7f && c != '"' && c != ';' && c != '\\' {
			b.WriteByte(c)
		}
	}
	return b.String()
}

func setIsolatedCookie(jar http.CookieJar, c *http.Cookie, domain string, targetURL *url.URL) {
	if jar == nil || c == nil || c.Name == "" {
		return
	}
	cleanDomain := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(domain), "."))
	if cleanDomain == "" {
		if targetURL == nil {
			return
		}
		cleanDomain = strings.ToLower(targetURL.Hostname())
	}

	if targetURL != nil {
		targetHost := targetURL.Hostname()
		if !DomainMatches(cleanDomain, targetHost) && !SameRegistrableDomain(cleanDomain, targetHost) {
			return
		}
	}

	scheme := "http"
	if c.Secure || (targetURL != nil && strings.EqualFold(targetURL.Scheme, "https")) {
		scheme = "https"
	}

	cookieCopy := *c
	cookieCopy.Value = sanitizeRFC6265CookieValue(cookieCopy.Value)
	if strings.HasPrefix(strings.TrimSpace(domain), ".") || cookieCopy.Domain != "" {
		cookieCopy.Domain = cleanDomain
	}
	if cookieCopy.Path == "" {
		cookieCopy.Path = "/"
	}

	originURL := &url.URL{
		Scheme: scheme,
		Host:   cleanDomain,
		Path:   cookieCopy.Path,
	}
	jar.SetCookies(originURL, []*http.Cookie{&cookieCopy})
}

// LoadCookiesIntoJar parses cookies from a Netscape cookie file path, Netscape text content,
// or raw cookie header string and loads them into jar with RFC 6265 domain isolation.
func LoadCookiesIntoJar(jar http.CookieJar, raw string, targetURL *url.URL) error {
	if jar == nil || strings.TrimSpace(raw) == "" {
		return nil
	}

	raw = strings.TrimSpace(raw)
	if info, err := os.Stat(raw); err == nil && !info.IsDir() {
		data, readErr := os.ReadFile(raw)
		if readErr != nil {
			return readErr
		}
		raw = string(data)
	}

	now := time.Now().UTC()
	lines := strings.Split(raw, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" {
			continue
		}

		isHTTPOnly := false
		if strings.HasPrefix(line, httpOnlyPrefix) {
			isHTTPOnly = true
			line = strings.TrimPrefix(line, httpOnlyPrefix)
		} else if strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.Split(line, "\t")
		if len(parts) >= 7 {
			domain := strings.TrimSpace(parts[0])
			includeSubdomains := strings.EqualFold(strings.TrimSpace(parts[1]), "TRUE")
			path := strings.TrimSpace(parts[2])
			isSecure := strings.EqualFold(strings.TrimSpace(parts[3]), "TRUE")
			expRaw, _ := strconv.ParseInt(strings.TrimSpace(parts[4]), 10, 64)
			name := parts[5]
			value := parts[6]

			if name == "" && value != "" {
				continue
			}

			expTime, valid := NormalizeCookieExpiry(expRaw, now)
			if !valid {
				continue
			}

			c := &http.Cookie{
				Name:     name,
				Value:    value,
				Path:     path,
				Secure:   isSecure,
				HttpOnly: isHTTPOnly,
				Expires:  expTime,
			}
			if includeSubdomains || strings.HasPrefix(domain, ".") {
				c.Domain = strings.TrimPrefix(domain, ".")
			}
			setIsolatedCookie(jar, c, domain, targetURL)
			continue
		}

		if targetURL == nil {
			continue
		}
		subparts := strings.Split(line, ";")
		for _, sp := range subparts {
			sp = strings.TrimSpace(sp)
			if idx := strings.Index(sp, "="); idx > 0 {
				c := &http.Cookie{
					Name:     strings.TrimSpace(sp[:idx]),
					Value:    strings.TrimSpace(sp[idx+1:]),
					Path:     "/",
					Secure:   strings.EqualFold(targetURL.Scheme, "https"),
					HttpOnly: isHTTPOnly,
				}
				setIsolatedCookie(jar, c, targetURL.Hostname(), targetURL)
			}
		}
	}

	return nil
}

// LoadCookiesFromBrowser extracts cookies from the specified browser profile and loads
// them into jar scoped to targetURL (if non-nil).
func LoadCookiesFromBrowser(jar http.CookieJar, specStr string, targetURL *url.URL) error {
	if jar == nil || strings.TrimSpace(specStr) == "" {
		return nil
	}

	spec, err := ParseBrowserSpec(specStr)
	if err != nil {
		return err
	}

	var extractErr error
	if spec.Browser == "firefox" {
		extractErr = extractFirefoxCookies(jar, spec, targetURL)
	} else if isChromiumBrowser(spec.Browser) {
		extractErr = extractChromiumCookies(jar, spec, targetURL)
	} else if spec.Browser != "safari" {
		return fmt.Errorf("unsupported browser %q", spec.Browser)
	}

	if extractErr == nil && spec.Browser != "safari" {
		return nil
	}
	if extractErr != nil && strings.Contains(extractErr.Error(), "expired") {
		return extractErr
	}

	if fallbackErr := extractViaYTDLP(jar, specStr, targetURL); fallbackErr == nil {
		return nil
	}

	if extractErr != nil {
		return extractErr
	}
	return fmt.Errorf("failed to extract cookies from browser %q", spec.Browser)
}

func isChromiumBrowser(name string) bool {
	switch name {
	case "chrome", "chromium", "brave", "edge", "opera", "vivaldi", "whale":
		return true
	default:
		return false
	}
}

func configHomeDir() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return xdg
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config")
}

func chromiumBrowserConfig(browser string) (rootDir, keyringName string, supportsProfiles bool) {
	supportsProfiles = browser != "opera"
	home, _ := os.UserHomeDir()

	switch runtime.GOOS {
	case "windows":
		localAppData := os.Getenv("LOCALAPPDATA")
		roamingAppData := os.Getenv("APPDATA")
		dirs := map[string]string{
			"brave":    filepath.Join(localAppData, `BraveSoftware\Brave-Browser\User Data`),
			"chrome":   filepath.Join(localAppData, `Google\Chrome\User Data`),
			"chromium": filepath.Join(localAppData, `Chromium\User Data`),
			"edge":     filepath.Join(localAppData, `Microsoft\Edge\User Data`),
			"opera":    filepath.Join(roamingAppData, `Opera Software\Opera Stable`),
			"vivaldi":  filepath.Join(localAppData, `Vivaldi\User Data`),
			"whale":    filepath.Join(localAppData, `Naver\Naver Whale\User Data`),
		}
		rootDir = dirs[browser]
	case "darwin":
		appSupport := filepath.Join(home, "Library", "Application Support")
		dirs := map[string]string{
			"brave":    filepath.Join(appSupport, "BraveSoftware/Brave-Browser"),
			"chrome":   filepath.Join(appSupport, "Google/Chrome"),
			"chromium": filepath.Join(appSupport, "Chromium"),
			"edge":     filepath.Join(appSupport, "Microsoft Edge"),
			"opera":    filepath.Join(appSupport, "com.operasoftware.Opera"),
			"vivaldi":  filepath.Join(appSupport, "Vivaldi"),
			"whale":    filepath.Join(appSupport, "Naver/Whale"),
		}
		rootDir = dirs[browser]
	default:
		cfg := configHomeDir()
		dirs := map[string]string{
			"brave":    filepath.Join(cfg, "BraveSoftware/Brave-Browser"),
			"chrome":   filepath.Join(cfg, "google-chrome"),
			"chromium": filepath.Join(cfg, "chromium"),
			"edge":     filepath.Join(cfg, "microsoft-edge"),
			"opera":    filepath.Join(cfg, "opera"),
			"vivaldi":  filepath.Join(cfg, "vivaldi"),
			"whale":    filepath.Join(cfg, "naver-whale"),
		}
		rootDir = dirs[browser]
	}

	keyringMap := map[string]string{
		"brave":    "Brave",
		"chrome":   "Chrome",
		"chromium": "Chromium",
		"edge":     "Chromium",
		"opera":    "Chromium",
		"vivaldi":  "Chrome",
		"whale":    "Whale",
	}
	if runtime.GOOS == "darwin" {
		keyringMap["edge"] = "Microsoft Edge"
		keyringMap["opera"] = "Opera"
		keyringMap["vivaldi"] = "Vivaldi"
	}
	keyringName = keyringMap[browser]
	return rootDir, keyringName, supportsProfiles
}

func firefoxRootDirs() []string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows":
		return []string{
			filepath.Join(os.Getenv("APPDATA"), `Mozilla\Firefox\Profiles`),
			filepath.Join(os.Getenv("LOCALAPPDATA"), `Packages\Mozilla.Firefox_n80bbvh6b1yt2\LocalCache\Roaming\Mozilla\Firefox\Profiles`),
		}
	case "darwin":
		return []string{
			filepath.Join(home, "Library/Application Support/Firefox/Profiles"),
		}
	default:
		return []string{
			filepath.Join(configHomeDir(), "mozilla/firefox"),
			filepath.Join(home, ".mozilla/firefox"),
			filepath.Join(home, ".var/app/org.mozilla.firefox/config/mozilla/firefox"),
			filepath.Join(home, ".var/app/org.mozilla.firefox/.mozilla/firefox"),
			filepath.Join(home, "snap/firefox/common/.mozilla/firefox"),
		}
	}
}

func findNewestFile(roots []string, targetName string) (string, error) {
	type candidate struct {
		path    string
		modTime time.Time
	}
	var matches []candidate
	for _, root := range roots {
		if root == "" {
			continue
		}
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if !d.IsDir() && d.Name() == targetName {
				if info, statErr := d.Info(); statErr == nil {
					matches = append(matches, candidate{path: path, modTime: info.ModTime()})
				}
			}
			return nil
		})
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("cookie database %q not found", targetName)
	}
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].modTime.After(matches[j].modTime)
	})
	return matches[0].path, nil
}

func copyDatabaseToTemp(srcPath string) (string, error) {
	src, err := os.Open(srcPath)
	if err != nil {
		return "", err
	}
	defer src.Close()

	tmpFile, err := os.CreateTemp("", "pmvdl-cookies-*.sqlite")
	if err != nil {
		return "", err
	}
	tmpPath := tmpFile.Name()
	if err := os.Chmod(tmpPath, 0600); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return "", err
	}
	if _, err := io.Copy(tmpFile, src); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return "", err
	}
	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpPath)
		return "", err
	}

	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if sideData, readErr := os.ReadFile(srcPath + suffix); readErr == nil && len(sideData) > 0 {
			_ = os.WriteFile(tmpPath+suffix, sideData, 0600)
		}
	}
	return tmpPath, nil
}

func removeTempDatabase(tmpPath string) {
	if tmpPath == "" {
		return
	}
	_ = os.Remove(tmpPath)
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		_ = os.Remove(tmpPath + suffix)
	}
}

func runSQLiteQuery(dbPath, query string) (string, error) {
	if sqliteBin, err := exec.LookPath("sqlite3"); err == nil {
		cmd := exec.Command(sqliteBin, "-batch", "-noheader", "-separator", "\t", dbPath, query)
		out, cmdErr := cmd.Output()
		if cmdErr == nil {
			return string(out), nil
		}
	}
	if pyBin, err := exec.LookPath("python3"); err == nil {
		pyScript := `import sqlite3, sys
con = sqlite3.connect(sys.argv[1])
cur = con.cursor()
for row in cur.execute(sys.argv[2]):
    print("\t".join("" if v is None else str(v) for v in row))
con.close()`
		cmd := exec.Command(pyBin, "-c", pyScript, dbPath, query)
		out, cmdErr := cmd.Output()
		if cmdErr == nil {
			return string(out), nil
		}
	}
	return "", errors.New("neither sqlite3 CLI nor python3 sqlite3 available")
}

func buildDomainWhereClause(colName string, targetURL *url.URL) string {
	if targetURL == nil {
		return ""
	}
	base := RegistrableDomain(targetURL.Hostname())
	if base == "" || strings.ContainsAny(base, "'\"\\;%_") {
		return ""
	}
	return fmt.Sprintf(" WHERE (%s = '%s' OR %s = '.%s' OR %s LIKE '%%.%s')", colName, base, colName, base, colName, base)
}

func extractFirefoxCookies(jar http.CookieJar, spec BrowserSpec, targetURL *url.URL) error {
	roots := firefoxRootDirs()
	if spec.Profile != "" {
		if filepath.IsAbs(spec.Profile) || strings.ContainsRune(spec.Profile, filepath.Separator) {
			roots = []string{spec.Profile}
		} else {
			var filtered []string
			for _, r := range roots {
				filtered = append(filtered, filepath.Join(r, spec.Profile))
				if matches, err := filepath.Glob(filepath.Join(r, "*"+spec.Profile+"*")); err == nil {
					filtered = append(filtered, matches...)
				}
			}
			roots = filtered
		}
	}

	dbPath, err := findNewestFile(roots, "cookies.sqlite")
	if err != nil {
		return err
	}

	tmpDB, err := copyDatabaseToTemp(dbPath)
	if err != nil {
		return err
	}
	defer removeTempDatabase(tmpDB)

	whereClause := buildDomainWhereClause("host", targetURL)
	query := "SELECT host, name, value, path, expiry, isSecure, isHttpOnly FROM moz_cookies" + whereClause
	out, err := runSQLiteQuery(tmpDB, query)
	if err != nil {
		queryFallback := "SELECT host, name, value, path, expiry, isSecure, 0 FROM moz_cookies" + whereClause
		out, err = runSQLiteQuery(tmpDB, queryFallback)
		if err != nil {
			return err
		}
	}

	now := time.Now().UTC()
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 7 {
			continue
		}
		host := parts[0]
		name := parts[1]
		value := parts[2]
		path := parts[3]
		expRaw, _ := strconv.ParseInt(parts[4], 10, 64)
		isSecure := parts[5] == "1" || strings.EqualFold(parts[5], "true")
		isHTTPOnly := parts[6] == "1" || strings.EqualFold(parts[6], "true")

		expTime, valid := NormalizeCookieExpiry(expRaw, now)
		if !valid {
			continue
		}

		c := &http.Cookie{
			Name:     name,
			Value:    value,
			Path:     path,
			Secure:   isSecure,
			HttpOnly: isHTTPOnly,
			Expires:  expTime,
		}
		if strings.HasPrefix(host, ".") {
			c.Domain = strings.TrimPrefix(host, ".")
		}
		setIsolatedCookie(jar, c, host, targetURL)
	}
	return nil
}

func extractChromiumCookies(jar http.CookieJar, spec BrowserSpec, targetURL *url.URL) error {
	if runtime.GOOS == "windows" {
		return errors.New("windows DPAPI requires platform decryptor")
	}

	rootDir, keyringName, supportsProfiles := chromiumBrowserConfig(spec.Browser)
	searchRoot := rootDir
	if spec.Profile != "" {
		if filepath.IsAbs(spec.Profile) || strings.ContainsRune(spec.Profile, filepath.Separator) {
			searchRoot = spec.Profile
		} else if supportsProfiles {
			searchRoot = filepath.Join(rootDir, spec.Profile)
		}
	}

	dbPath, err := findNewestFile([]string{searchRoot}, "Cookies")
	if err != nil {
		return err
	}

	tmpDB, err := copyDatabaseToTemp(dbPath)
	if err != nil {
		return err
	}
	defer removeTempDatabase(tmpDB)

	metaVersion := 0
	if metaOut, metaErr := runSQLiteQuery(tmpDB, "SELECT value FROM meta WHERE key = 'version'"); metaErr == nil {
		metaVersion, _ = strconv.Atoi(strings.TrimSpace(metaOut))
	}

	keys := deriveChromiumKeys(keyringName)
	hashPrefix := metaVersion >= 24

	whereClause := buildDomainWhereClause("host_key", targetURL)
	query := "SELECT host_key, name, value, hex(encrypted_value), path, expires_utc, is_secure, is_httponly FROM cookies" + whereClause
	out, err := runSQLiteQuery(tmpDB, query)
	if err != nil {
		queryFallback := "SELECT host_key, name, value, hex(encrypted_value), path, expires_utc, secure, httponly FROM cookies" + whereClause
		out, err = runSQLiteQuery(tmpDB, queryFallback)
		if err != nil {
			return err
		}
	}

	now := time.Now().UTC()
	var expiredShieldCookie string
	var expiredAgo time.Duration
	loadedValidShield := false
	encryptedRows := 0
	decryptedRows := 0

	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 8 {
			continue
		}
		hostKey := parts[0]
		name := parts[1]
		val := parts[2]
		encHex := parts[3]
		path := parts[4]
		expRaw, _ := strconv.ParseInt(parts[5], 10, 64)
		isSecure := parts[6] == "1" || strings.EqualFold(parts[6], "true")
		isHTTPOnly := parts[7] == "1" || strings.EqualFold(parts[7], "true")

		if val == "" && encHex != "" {
			encryptedRows++
			encBytes, hexErr := hex.DecodeString(encHex)
			if hexErr != nil {
				continue
			}
			decrypted, ok := DecryptChromiumCookieValue(encBytes, hostKey, keys, hashPrefix)
			if !ok {
				continue
			}
			decryptedRows++
			val = decrypted
		}

		isShield := strings.Contains(strings.ToLower(name), "bunny_shield") || strings.Contains(strings.ToLower(name), "cf_clearance")
		expTime, valid := NormalizeCookieExpiry(expRaw, now)
		if !valid {
			if isShield && !expTime.IsZero() {
				expiredShieldCookie = name
				expiredAgo = now.Sub(expTime).Round(time.Second)
			}
			continue
		}
		if isShield {
			loadedValidShield = true
		}

		c := &http.Cookie{
			Name:     name,
			Value:    val,
			Path:     path,
			Secure:   isSecure,
			HttpOnly: isHTTPOnly,
			Expires:  expTime,
		}
		if strings.HasPrefix(hostKey, ".") {
			c.Domain = strings.TrimPrefix(hostKey, ".")
		}
		setIsolatedCookie(jar, c, hostKey, targetURL)
	}

	if encryptedRows > 0 && decryptedRows == 0 {
		return errors.New("failed to decrypt chromium cookies with native keyring")
	}
	if expiredShieldCookie != "" && !loadedValidShield {
		return fmt.Errorf("browser clearance cookie %q in %s expired %v ago (BunnyCDN Shield cookies expire after 20 minutes); refresh the page in %s and retry", expiredShieldCookie, spec.Browser, expiredAgo, spec.Browser)
	}

	return nil
}

// PBKDF2SHA1 derives a key of keyLen bytes using PBKDF2-HMAC-SHA1 (RFC 2898).
func PBKDF2SHA1(password, salt []byte, iterations, keyLen int) []byte {
	prf := hmac.New(sha1.New, password)
	hashLen := prf.Size()
	numBlocks := (keyLen + hashLen - 1) / hashLen
	dk := make([]byte, 0, numBlocks*hashLen)
	u := make([]byte, hashLen)
	t := make([]byte, hashLen)
	blockBytes := make([]byte, 4)

	for block := 1; block <= numBlocks; block++ {
		prf.Reset()
		prf.Write(salt)
		blockBytes[0] = byte(block >> 24)
		blockBytes[1] = byte(block >> 16)
		blockBytes[2] = byte(block >> 8)
		blockBytes[3] = byte(block)
		prf.Write(blockBytes)
		u = prf.Sum(u[:0])
		copy(t, u)

		for i := 2; i <= iterations; i++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(u[:0])
			for j := range u {
				t[j] ^= u[j]
			}
		}
		dk = append(dk, t...)
	}
	return dk[:keyLen]
}

func deriveChromiumKeys(keyringName string) [][]byte {
	salt := []byte("saltysalt")
	iterations := 1
	if runtime.GOOS == "darwin" {
		iterations = 1003
	}

	var keys [][]byte
	if pw := queryOSKeyringPassword(keyringName); len(pw) > 0 {
		keys = append(keys, PBKDF2SHA1(pw, salt, iterations, 16))
	}
	keys = append(keys,
		PBKDF2SHA1([]byte("peanuts"), salt, 1, 16),
		PBKDF2SHA1([]byte(""), salt, 1, 16),
	)
	return keys
}

func queryOSKeyringPassword(keyringName string) []byte {
	if runtime.GOOS == "darwin" {
		if secBin, err := exec.LookPath("security"); err == nil {
			out, cmdErr := exec.Command(secBin, "find-generic-password", "-w", "-a", keyringName, "-s", keyringName+" Safe Storage").Output()
			if cmdErr == nil {
				return bytes.TrimRight(out, "\r\n")
			}
		}
		return nil
	}

	label := keyringName + " Safe Storage"
	if stBin, err := exec.LookPath("secret-tool"); err == nil {
		for _, app := range []string{strings.ToLower(keyringName), keyringName} {
			if out, cmdErr := exec.Command(stBin, "lookup", "application", app).Output(); cmdErr == nil && len(out) > 0 {
				return bytes.TrimRight(out, "\r\n")
			}
		}
	}

	if pyBin, err := exec.LookPath("python3"); err == nil {
		pyScript := `import sys, os, glob, contextlib
for p in glob.glob(os.path.expanduser('~/.local/lib/python*/site-packages')):
    if p not in sys.path:
        sys.path.append(p)
import secretstorage
with contextlib.closing(secretstorage.dbus_init()) as con:
    col = secretstorage.get_default_collection(con)
    for item in col.get_all_items():
        if item.get_label() == sys.argv[1]:
            sys.stdout.buffer.write(item.get_secret())
            break`
		if out, cmdErr := exec.Command(pyBin, "-c", pyScript, label).Output(); cmdErr == nil && len(out) > 0 {
			return out
		}
	}

	return nil
}

// DecryptChromiumCookieValue decrypts a v10/v11 Chromium AES-128-CBC encrypted cookie value.
// When hashPrefix is true (Chromium DB meta version >= 24), it verifies or strips the 32-byte
// SHA-256 domain-binding prefix.
func DecryptChromiumCookieValue(encrypted []byte, hostKey string, keys [][]byte, hashPrefix bool) (string, bool) {
	if len(encrypted) <= 3 {
		return "", false
	}
	version := string(encrypted[:3])
	if version != "v10" && version != "v11" {
		return "", false
	}
	ciphertext := encrypted[3:]
	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return "", false
	}

	iv := bytes.Repeat([]byte{' '}, aes.BlockSize)
	expectedDomainHash := sha256.Sum256([]byte(hostKey))

	for _, key := range keys {
		if len(key) != aes.BlockSize {
			continue
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			continue
		}
		pt := make([]byte, len(ciphertext))
		mode := cipher.NewCBCDecrypter(block, iv)
		mode.CryptBlocks(pt, ciphertext)

		unpadded, ok := unpadPKCS7(pt, aes.BlockSize)
		if !ok {
			continue
		}

		if hashPrefix {
			if len(unpadded) < 32 {
				continue
			}
			if !bytes.Equal(unpadded[:32], expectedDomainHash[:]) {
				continue
			}
			valBytes := unpadded[32:]
			if !utf8.Valid(valBytes) {
				continue
			}
			return string(valBytes), true
		}

		if utf8.Valid(unpadded) {
			return string(unpadded), true
		}
	}
	return "", false
}

func unpadPKCS7(data []byte, blockSize int) ([]byte, bool) {
	if len(data) == 0 || len(data)%blockSize != 0 {
		return nil, false
	}
	padLen := int(data[len(data)-1])
	if padLen == 0 || padLen > blockSize || padLen > len(data) {
		return nil, false
	}
	for i := len(data) - padLen; i < len(data); i++ {
		if int(data[i]) != padLen {
			return nil, false
		}
	}
	return data[:len(data)-padLen], true
}

func extractViaYTDLP(jar http.CookieJar, specStr string, targetURL *url.URL) error {
	pyBin, err := exec.LookPath("python3")
	if err != nil {
		return err
	}
	pyScript := `import sys, os, glob
for p in glob.glob(os.path.expanduser('~/.local/lib/python*/site-packages')):
    if p not in sys.path:
        sys.path.append(p)
from yt_dlp.cookies import extract_cookies_from_browser, _parse_browser_specification
b, prof, kr, cont = _parse_browser_specification(sys.argv[1])
jar = extract_cookies_from_browser(b, profile=prof, keyring=kr, container=cont)
jar._really_save(sys.stdout, ignore_discard=True, ignore_expires=True)`

	cmd := exec.Command(pyBin, "-c", pyScript, specStr)
	out, err := cmd.Output()
	if err != nil {
		return err
	}
	return LoadCookiesIntoJar(jar, string(out), targetURL)
}

// CookieHeaderForURL returns an RFC 6265 domain- and scheme-verified Cookie header string
// for u from jar, or an empty string if no cookies match u.
func CookieHeaderForURL(jar http.CookieJar, u *url.URL) string {
	if jar == nil || u == nil {
		return ""
	}
	cookies := jar.Cookies(u)
	if len(cookies) == 0 {
		return ""
	}
	parts := make([]string, 0, len(cookies))
	for _, c := range cookies {
		if c.Name != "" {
			parts = append(parts, c.Name+"="+c.Value)
		}
	}
	return strings.Join(parts, "; ")
}

type scopedHeaderEntry struct {
	name      string
	value     string
	sensitive bool
}

type scopedHeaderTransport struct {
	base      http.RoundTripper
	targetURL *url.URL
	headers   []scopedHeaderEntry
}

func isSensitiveHeaderName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "cookie", "authorization", "proxy-authorization", "x-auth-token", "token", "x-api-key", "api-key":
		return true
	default:
		return false
	}
}

func (t *scopedHeaderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	if len(t.headers) == 0 || req.URL == nil {
		return base.RoundTrip(req)
	}

	sameOrigin := t.targetURL != nil &&
		SameRegistrableDomain(req.URL.Hostname(), t.targetURL.Hostname()) &&
		!(strings.EqualFold(t.targetURL.Scheme, "https") && strings.EqualFold(req.URL.Scheme, "http"))

	cloned := req.Clone(req.Context())
	for _, h := range t.headers {
		if h.sensitive && !sameOrigin {
			continue
		}
		if h.value == "" {
			cloned.Header.Del(h.name)
			continue
		}
		cloned.Header.Set(h.name, h.value)
	}
	return base.RoundTrip(cloned)
}

var (
	reMajorVer   = regexp.MustCompile(`(\d+)\.\d+`)
	reFFCompat   = regexp.MustCompile(`(?m)^LastVersion=(\d+)\.`)
)

func detectBrowserClientHeaders(specStr string) []scopedHeaderEntry {
	spec, err := ParseBrowserSpec(specStr)
	if err != nil {
		return nil
	}

	osToken := "X11; Linux x86_64"
	secPlatform := `"Linux"`
	switch runtime.GOOS {
	case "windows":
		osToken = "Windows NT 10.0; Win64; x64"
		secPlatform = `"Windows"`
	case "darwin":
		osToken = "Macintosh; Intel Mac OS X 10_15_7"
		secPlatform = `"macOS"`
	}

	if spec.Browser == "firefox" {
		ffVer := "135"
		if dbPath, findErr := findNewestFile(firefoxRootDirs(), "compatibility.ini"); findErr == nil {
			if data, readErr := os.ReadFile(dbPath); readErr == nil {
				if m := reFFCompat.FindSubmatch(data); len(m) >= 2 {
					ffVer = string(m[1])
				}
			}
		} else if binPath, lookErr := exec.LookPath("firefox"); lookErr == nil {
			if out, cmdErr := exec.Command(binPath, "--version").Output(); cmdErr == nil {
				if m := reMajorVer.FindSubmatch(out); len(m) >= 2 {
					ffVer = string(m[1])
				}
			}
		}
		ua := fmt.Sprintf("Mozilla/5.0 (%s; rv:%s.0) Gecko/20100101 Firefox/%s.0", osToken, ffVer, ffVer)
		return []scopedHeaderEntry{
			{name: "User-Agent", value: ua, sensitive: false},
			{name: "Accept", value: "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8", sensitive: false},
			{name: "Accept-Language", value: "en-US,en;q=0.5", sensitive: false},
			{name: "Upgrade-Insecure-Requests", value: "1", sensitive: false},
			{name: "Sec-Ch-Ua", value: "", sensitive: false},
			{name: "Sec-Ch-Ua-Mobile", value: "", sensitive: false},
			{name: "Sec-Ch-Ua-Platform", value: "", sensitive: false},
			{name: "Sec-Fetch-Site", value: "none", sensitive: false},
			{name: "Sec-Fetch-Mode", value: "navigate", sensitive: false},
			{name: "Sec-Fetch-User", value: "?1", sensitive: false},
			{name: "Sec-Fetch-Dest", value: "document", sensitive: false},
		}
	}

	if spec.Browser == "safari" {
		return []scopedHeaderEntry{
			{name: "User-Agent", value: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15", sensitive: false},
			{name: "Accept", value: "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8", sensitive: false},
			{name: "Accept-Language", value: "en-US,en;q=0.9", sensitive: false},
			{name: "Sec-Ch-Ua", value: "", sensitive: false},
			{name: "Sec-Ch-Ua-Mobile", value: "", sensitive: false},
			{name: "Sec-Ch-Ua-Platform", value: "", sensitive: false},
		}
	}

	if !isChromiumBrowser(spec.Browser) {
		return nil
	}

	major := "131"
	rootDir, _, _ := chromiumBrowserConfig(spec.Browser)
	if rootDir != "" {
		if lvData, readErr := os.ReadFile(filepath.Join(rootDir, "Last Version")); readErr == nil {
			if m := reMajorVer.FindSubmatch(lvData); len(m) >= 2 {
				major = string(m[1])
			}
		}
	}

	if major == "131" {
		var candidates []string
		switch spec.Browser {
		case "chrome":
			candidates = []string{"google-chrome", "google-chrome-stable"}
		case "chromium":
			candidates = []string{"chromium", "chromium-browser"}
		case "brave":
			candidates = []string{"brave-browser", "brave"}
		case "edge":
			candidates = []string{"microsoft-edge", "microsoft-edge-stable"}
		case "vivaldi":
			candidates = []string{"vivaldi", "vivaldi-stable"}
		case "opera":
			candidates = []string{"opera", "opera-stable"}
		}
		for _, binName := range candidates {
			if binPath, lookErr := exec.LookPath(binName); lookErr == nil {
				if out, cmdErr := exec.Command(binPath, "--version").Output(); cmdErr == nil {
					if m := reMajorVer.FindSubmatch(out); len(m) >= 2 {
						major = string(m[1])
						break
					}
				}
			}
		}
	}

	ua := fmt.Sprintf("Mozilla/5.0 (%s) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%s.0.0.0 Safari/537.36", osToken, major)
	secUA := fmt.Sprintf(`"Not-A.Brand";v="99", "Google Chrome";v=%q, "Chromium";v=%q`, major, major)
	switch spec.Browser {
	case "brave":
		secUA = fmt.Sprintf(`"Not-A.Brand";v="99", "Brave";v=%q, "Chromium";v=%q`, major, major)
	case "edge":
		ua += fmt.Sprintf(" Edg/%s.0.0.0", major)
		secUA = fmt.Sprintf(`"Not-A.Brand";v="99", "Microsoft Edge";v=%q, "Chromium";v=%q`, major, major)
	case "chromium", "vivaldi", "opera", "whale":
		secUA = fmt.Sprintf(`"Not-A.Brand";v="99", "Chromium";v=%q`, major)
	}

	return []scopedHeaderEntry{
		{name: "User-Agent", value: ua, sensitive: false},
		{name: "Accept", value: "text/html,application/xhtml+xml,application/xml;q=0.9,image/jxl,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7", sensitive: false},
		{name: "Accept-Language", value: "en-US,en;q=0.9", sensitive: false},
		{name: "Upgrade-Insecure-Requests", value: "1", sensitive: false},
		{name: "Sec-Ch-Ua", value: secUA, sensitive: false},
		{name: "Sec-Ch-Ua-Mobile", value: "?0", sensitive: false},
		{name: "Sec-Ch-Ua-Platform", value: secPlatform, sensitive: false},
		{name: "Sec-Fetch-Site", value: "none", sensitive: false},
		{name: "Sec-Fetch-Mode", value: "navigate", sensitive: false},
		{name: "Sec-Fetch-User", value: "?1", sensitive: false},
		{name: "Sec-Fetch-Dest", value: "document", sensitive: false},
	}
}

// ApplyOptionsToClient loads cookies from browser, Netscape/raw cookie flags, and custom headers
// into client while enforcing domain scoping and GHSA-v8mc-9377-rwjj mitigation for Cookie headers.
func ApplyOptionsToClient(client *http.Client, opts DownloadOptions, targetURL *url.URL) error {
	if client == nil {
		return nil
	}

	var entries []scopedHeaderEntry
	var firstErr error
	if opts.CookiesFromBrowser != "" {
		entries = append(entries, detectBrowserClientHeaders(opts.CookiesFromBrowser)...)
		if err := LoadCookiesFromBrowser(client.Jar, opts.CookiesFromBrowser, targetURL); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("cookies-from-browser: %w", err)
		}
	}

	if opts.Cookies != "" {
		if err := LoadCookiesIntoJar(client.Jar, opts.Cookies, targetURL); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("cookies: %w", err)
		}
	}

	for _, rawHdr := range opts.Headers {
		idx := strings.Index(rawHdr, ":")
		if idx <= 0 {
			continue
		}
		name := strings.TrimSpace(rawHdr[:idx])
		val := strings.TrimSpace(rawHdr[idx+1:])
		if name == "" {
			continue
		}
		if strings.EqualFold(name, "Cookie") {
			_ = LoadCookiesIntoJar(client.Jar, val, targetURL)
			continue
		}
		entries = append(entries, scopedHeaderEntry{
			name:      name,
			value:     val,
			sensitive: isSensitiveHeaderName(name),
		})
	}

	if len(entries) > 0 {
		client.Transport = &scopedHeaderTransport{
			base:      client.Transport,
			targetURL: targetURL,
			headers:   entries,
		}
	}

	return firstErr
}
