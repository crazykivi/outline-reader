package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

const updateRepo = "crazykivi/outline-reader"
const updateAPIURL = "https://api.github.com/repos/" + updateRepo + "/releases/latest"
const maxUpdateSize = 256 << 20

type UpdateInfo struct {
	Available   bool   `json:"available"`
	Version     string `json:"version"`
	DownloadURL string `json:"downloadUrl"`
	SHA256      string `json:"sha256"`
	Note        string `json:"note"`
}

type ghAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

type ghRelease struct {
	TagName string    `json:"tag_name"`
	Assets  []ghAsset `json:"assets"`
}

func (a *App) CheckForUpdate() (UpdateInfo, error) {
	a.updMu.Lock()
	defer a.updMu.Unlock()

	rel, err := a.fetchLatestRelease()
	if err != nil {
		return UpdateInfo{}, err
	}
	info := UpdateInfo{Version: strings.TrimPrefix(rel.TagName, "v")}
	current := a.AppVersion()

	if _, _, ok := parseVersion(current); !ok {
		a.lastUpdate = nil
		info.Note = "dev-сборка: сравнение версий недоступно"
		return info, nil
	}
	if !newerVersion(rel.TagName, current) {
		a.lastUpdate = nil
		return info, nil
	}

	asset := pickWindowsAsset(rel)
	if asset == nil {
		return UpdateInfo{}, fmt.Errorf("в релизе %s нет сборки для windows/%s", rel.TagName, runtime.GOARCH)
	}
	sum, err := fetchChecksum(rel, asset.Name)
	if err != nil {
		return UpdateInfo{}, fmt.Errorf("контрольная сумма релиза: %w", err)
	}
	info.DownloadURL = asset.URL
	info.SHA256 = sum
	info.Available = true
	a.lastUpdate = &info
	log.Printf("[UPDATE] доступна версия %s, текущая %s", info.Version, current)
	return info, nil
}

func (a *App) DownloadAndInstallUpdate(rawURL string) error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("автообновление доступно только на Windows")
	}
	a.updMu.Lock()
	info := a.lastUpdate
	a.updMu.Unlock()

	if info == nil || !info.Available || rawURL != info.DownloadURL {
		return fmt.Errorf("ссылка не подтверждена проверкой обновлений")
	}
	if info.SHA256 == "" {
		return fmt.Errorf("нет контрольной суммы релиза")
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}

	tmp, err := a.downloadUpdate(info)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp) }()

	old, err := replaceExecutable(exe, tmp)
	if err != nil {
		return err
	}
	log.Printf("[UPDATE] бинарник заменён на версию %s", info.Version)

	if err := a.restartAfterUpdate(exe, old); err != nil {
		return fmt.Errorf("перезапуск: %w", err)
	}
	go func() {
		time.Sleep(500 * time.Millisecond)
		if a.ctx != nil {
			wailsruntime.Quit(a.ctx)
		}
	}()
	return nil
}

func (a *App) fetchLatestRelease() (*ghRelease, error) {
	url := a.updateAPI
	if url == "" {
		url = updateAPIURL
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "outline-reader/"+a.AppVersion())

	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github: %s", resp.Status)
	}
	var rel ghRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil {
		return nil, err
	}
	if rel.TagName == "" {
		return nil, fmt.Errorf("github: пустой ответ релиза")
	}
	return &rel, nil
}

func pickWindowsAsset(rel *ghRelease) *ghAsset {
	want := fmt.Sprintf("outline-reader-%s-windows-%s.exe", rel.TagName, runtime.GOARCH)
	for i := range rel.Assets {
		if rel.Assets[i].Name == want {
			return &rel.Assets[i]
		}
	}
	suffix := fmt.Sprintf("-windows-%s.exe", runtime.GOARCH)
	for i := range rel.Assets {
		n := rel.Assets[i].Name
		if strings.HasSuffix(n, suffix) && !strings.Contains(n, "portable") {
			return &rel.Assets[i]
		}
	}
	return nil
}

func fetchChecksum(rel *ghRelease, name string) (string, error) {
	var url string
	for _, as := range rel.Assets {
		if as.Name == "sha256sums.txt" {
			url = as.URL
			break
		}
	}
	if url == "" {
		return "", fmt.Errorf("в релизе нет sha256sums.txt")
	}
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Get(url)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("sha256sums.txt: %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(body), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && strings.EqualFold(f[1], name) && len(f[0]) == 64 {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("в sha256sums.txt нет записи для %s", name)
}

func (a *App) downloadUpdate(info *UpdateInfo) (string, error) {
	f, err := os.CreateTemp(exeDir(), ".update-*")
	if err != nil {
		return "", fmt.Errorf("нет прав на запись в каталог программы, скачайте версию вручную")
	}
	tmp := f.Name()
	drop := func() { _ = f.Close(); _ = os.Remove(tmp) }

	resp, err := (&http.Client{Timeout: 10 * time.Minute}).Get(info.DownloadURL)
	if err != nil {
		drop()
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		drop()
		return "", fmt.Errorf("скачивание: %s", resp.Status)
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, maxUpdateSize+1))
	_ = f.Close()
	if err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if n > maxUpdateSize {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("файл обновления слишком большой")
	}
	sum, err := fileSHA256(tmp)
	if err != nil || !strings.EqualFold(sum, info.SHA256) {
		_ = os.Remove(tmp)
		log.Printf("[UPDATE] checksum mismatch: получено %s", sum)
		return "", fmt.Errorf("контрольная сумма обновления не совпала")
	}
	if !isPE(tmp) {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("скачанный файл не является exe")
	}
	log.Printf("[UPDATE] загружено %d байт, sha256 подтверждён", n)
	return tmp, nil
}

func replaceExecutable(exe, tmp string) (string, error) {
	old := exe + ".old"
	_ = os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		return "", fmt.Errorf("не удалось освободить имя файла: %w", err)
	}
	if err := os.Rename(tmp, exe); err != nil {
		_ = os.Rename(old, exe)
		return "", fmt.Errorf("не удалось установить обновление: %w", err)
	}
	return old, nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func isPE(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	var hdr [2]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		return false
	}
	return hdr[0] == 'M' && hdr[1] == 'Z'
}

func parseVersion(s string) (nums [3]int, pre string, ok bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.Index(s, "+"); i >= 0 {
		s = s[:i]
	}
	if i := strings.Index(s, "-"); i >= 0 {
		pre, s = s[i+1:], s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return nums, pre, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nums, pre, false
		}
		nums[i] = n
	}
	return nums, pre, true
}

func newerVersion(latest, current string) bool {
	l, lp, lok := parseVersion(latest)
	c, cp, cok := parseVersion(current)
	if !lok || !cok {
		return false
	}
	for i := range l {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	if lp == cp {
		return false
	}
	if lp == "" {
		return true
	}
	if cp == "" {
		return false
	}
	return lp > cp
}
