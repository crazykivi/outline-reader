package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
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
	Signature   string `json:"signature"`
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
	signature, err := fetchSignature(rel, asset.Name+".sig")
	if err != nil {
		return UpdateInfo{}, fmt.Errorf("подпись релиза: %w", err)
	}
	info.DownloadURL = asset.URL
	info.SHA256 = sum
	info.Signature = signature
	info.Available = true
	a.lastUpdate = &info
	log.Printf("[UPDATE] доступна версия %s, текущая %s", info.Version, current)
	return info, nil
}

func (a *App) DownloadAndInstallUpdate(rawURL string) (retErr error) {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("автообновление доступно только на Windows")
	}

	a.updMu.Lock()
	if a.updateInProgress {
		a.updMu.Unlock()
		return fmt.Errorf("установка обновления уже выполняется")
	}
	if a.lastUpdate == nil || !a.lastUpdate.Available || rawURL != a.lastUpdate.DownloadURL {
		a.updMu.Unlock()
		return fmt.Errorf("ссылка не подтверждена проверкой обновлений")
	}
	info := *a.lastUpdate
	if info.SHA256 == "" {
		a.updMu.Unlock()
		return fmt.Errorf("нет контрольной суммы релиза")
	}
	a.updateInProgress = true
	a.updMu.Unlock()

	accepted := false
	defer func() {
		if !accepted {
			a.updMu.Lock()
			a.updateInProgress = false
			a.updMu.Unlock()
		}
	}()
	defer func() {
		if retErr != nil {
			a.emit("update:progress", map[string]any{"state": "error", "message": retErr.Error()})
		}
	}()

	if a.ctx == nil {
		return fmt.Errorf("приложение ещё не запущено")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	old := exe + ".old"
	if _, err := os.Lstat(old); err == nil {
		return fmt.Errorf("найдена резервная копия %s; автоматическое обновление остановлено, чтобы не перезаписать её", old)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("проверка резервной копии: %w", err)
	}

	tmp, err := a.downloadUpdate(&info)
	if err != nil {
		return err
	}
	keepTemp := false
	defer func() {
		if !keepTemp {
			_ = os.Remove(tmp)
		}
	}()

	readyFile, err := os.CreateTemp(os.TempDir(), "outline-reader-ready-*.flag")
	if err != nil {
		return fmt.Errorf("создание маркера готовности: %w", err)
	}
	readyPath := readyFile.Name()
	if err := readyFile.Close(); err != nil {
		_ = os.Remove(readyPath)
		return err
	}
	if err := os.Remove(readyPath); err != nil {
		return err
	}

	a.emit("update:progress", map[string]any{"state": "installing", "version": info.Version})
	if err := a.restartAfterUpdate(exe, tmp, old, readyPath); err != nil {
		_ = os.Remove(readyPath)
		return fmt.Errorf("запуск помощника обновления: %w", err)
	}
	keepTemp = true
	accepted = true
	a.emit("update:progress", map[string]any{"state": "restarting", "version": info.Version})
	log.Printf("[UPDATE] помощник обновления запущен, ожидается закрытие pid=%d", os.Getpid())
	wailsruntime.Quit(a.ctx)
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

func fetchSignature(rel *ghRelease, name string) (string, error) {
	var assetURL string
	for _, asset := range rel.Assets {
		if asset.Name == name {
			assetURL = asset.URL
			break
		}
	}
	if assetURL == "" {
		return "", fmt.Errorf("в релизе нет %s", name)
	}
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Get(assetURL)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %s", name, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return "", err
	}
	encoded := strings.TrimSpace(string(body))
	signature, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return "", fmt.Errorf("%s содержит некорректную подпись", name)
	}
	return encoded, nil
}

var updateSigningPublicKey = ed25519.PublicKey{
	17, 252, 232, 40, 181, 21, 242, 185, 147, 93, 18, 201, 120, 14, 54, 61,
	160, 243, 153, 142, 172, 143, 205, 223, 187, 201, 234, 18, 157, 173, 91, 155,
}

func verifyUpdateSignature(path, encodedSignature string) error {
	return verifySignatureWithKey(path, updateSigningPublicKey, encodedSignature)
}

func verifySignatureWithKey(path string, publicKey ed25519.PublicKey, encodedSignature string) error {
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedSignature))
	if err != nil || len(signature) != ed25519.SignatureSize {
		return fmt.Errorf("некорректный формат подписи")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if !ed25519.Verify(publicKey, h.Sum(nil), signature) {
		return fmt.Errorf("подпись не соответствует встроенному ключу")
	}
	return nil
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
	total := resp.ContentLength
	a.emit("update:progress", map[string]any{"state": "downloading", "downloaded": int64(0), "total": total, "percent": 0, "version": info.Version})
	buf := make([]byte, 64*1024)
	var n int64
	lastEmit := time.Now()
	lastPercent := -1
	for {
		read, readErr := resp.Body.Read(buf)
		if read > 0 {
			written, writeErr := f.Write(buf[:read])
			n += int64(written)
			if writeErr != nil {
				drop()
				return "", writeErr
			}
			if written != read {
				drop()
				return "", io.ErrShortWrite
			}
			if n > maxUpdateSize {
				drop()
				return "", fmt.Errorf("файл обновления слишком большой")
			}
			percent := 0
			if total > 0 {
				percent = int(n * 100 / total)
				if percent > 100 {
					percent = 100
				}
			}
			if percent != lastPercent || time.Since(lastEmit) >= 150*time.Millisecond {
				a.emit("update:progress", map[string]any{"state": "downloading", "downloaded": n, "total": total, "percent": percent, "version": info.Version})
				lastPercent, lastEmit = percent, time.Now()
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				drop()
				return "", readErr
			}
			break
		}
	}
	if err := f.Sync(); err != nil {
		drop()
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if total > 0 && n != total {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("загрузка прервана: получено %d из %d байт", n, total)
	}
	a.emit("update:progress", map[string]any{"state": "verifying", "downloaded": n, "total": total, "percent": 100, "version": info.Version})
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
	if err := verifyUpdateSignature(tmp, info.Signature); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("подпись обновления не прошла проверку: %w", err)
	}
	log.Printf("[UPDATE] загружено %d байт, sha256 подтверждён", n)
	return tmp, nil
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
