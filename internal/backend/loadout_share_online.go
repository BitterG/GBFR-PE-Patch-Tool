package backend

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	loadoutShareServiceURL         = "https://share.whitelinker.top"
	loadoutShareOnlineMaxFrameSize = 8 * 1024
)

var loadoutShareShortCodePattern = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{16,24}$`)

type LoadoutPublishedShare struct {
	Code        string `json:"code"`
	CompactCode string `json:"compactCode"`
	URL         string `json:"url"`
	DownloadURL string `json:"downloadUrl"`
	Bytes       int    `json:"bytes"`
	Reused      bool   `json:"reused"`
}

type loadoutShareOnlineError struct {
	Error string `json:"error"`
}

func loadoutShareHTTPClient() *http.Client {
	return &http.Client{Timeout: 15 * time.Second}
}

func loadoutShareRequestContext(ctx context.Context) context.Context {
	if ctx != nil {
		return ctx
	}
	return context.Background()
}

func loadoutShareFrameFromCompatibilityCode(code string) ([]byte, error) {
	code = normalizeLoadoutShareCode(code)
	if !strings.HasPrefix(code, loadoutShareCodePrefix) {
		return nil, fmt.Errorf("本地配装帧前缀无效")
	}
	frame, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(code, loadoutShareCodePrefix))
	if err != nil {
		return nil, fmt.Errorf("解析本地配装帧失败: %w", err)
	}
	if len(frame) == 0 || len(frame) > loadoutShareOnlineMaxFrameSize {
		return nil, fmt.Errorf("线上分享只接受不超过 %d KB 的配装帧", loadoutShareOnlineMaxFrameSize/1024)
	}
	return frame, nil
}

func normalizeLoadoutShareShortCode(input string) (string, error) {
	value := strings.TrimSpace(input)
	if parsed, err := url.Parse(value); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		path := strings.Trim(parsed.EscapedPath(), "/")
		switch {
		case strings.HasPrefix(path, "s/"):
			value = strings.TrimPrefix(path, "s/")
		case strings.HasPrefix(path, "api/v1/loadouts/"):
			value = strings.TrimPrefix(path, "api/v1/loadouts/")
		case strings.HasPrefix(path, "download/") && strings.HasSuffix(strings.ToLower(path), ".gbfr-loadout"):
			value = strings.TrimSuffix(strings.TrimPrefix(path, "download/"), ".gbfr-loadout")
		default:
			return "", fmt.Errorf("链接中没有可识别的配装短码")
		}
		if decoded, decodeErr := url.PathUnescape(value); decodeErr == nil {
			value = decoded
		}
	}
	value = strings.ToUpper(value)
	value = strings.NewReplacer("-", "", " ", "", "\t", "", "\r", "", "\n", "").Replace(value)
	if !loadoutShareShortCodePattern.MatchString(value) {
		return "", fmt.Errorf("短码格式无效；请输入 16 位短码或完整分享链接")
	}
	return value, nil
}

func displayLoadoutShareShortCode(code string) string {
	var groups []string
	for len(code) > 0 {
		size := min(4, len(code))
		groups = append(groups, code[:size])
		code = code[size:]
	}
	return strings.Join(groups, "-")
}

func loadoutShareOnlineErrorMessage(response *http.Response) string {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 4*1024))
	var payload loadoutShareOnlineError
	if json.Unmarshal(body, &payload) == nil && strings.TrimSpace(payload.Error) != "" {
		return strings.TrimSpace(payload.Error)
	}
	return http.StatusText(response.StatusCode)
}

func publishLoadoutShareFrame(ctx context.Context, client *http.Client, endpoint string, frame []byte) (*LoadoutPublishedShare, error) {
	if len(frame) == 0 || len(frame) > loadoutShareOnlineMaxFrameSize {
		return nil, fmt.Errorf("线上分享只接受不超过 %d KB 的配装帧", loadoutShareOnlineMaxFrameSize/1024)
	}
	endpoint = strings.TrimRight(endpoint, "/")
	request, err := http.NewRequestWithContext(loadoutShareRequestContext(ctx), http.MethodPost, endpoint+"/api/v1/loadouts", bytes.NewReader(frame))
	if err != nil {
		return nil, fmt.Errorf("创建配装发布请求失败: %w", err)
	}
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", repoName+"/"+appVersion)
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("连接配装分享服务失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("配装分享服务返回 %d: %s", response.StatusCode, loadoutShareOnlineErrorMessage(response))
	}
	var received LoadoutPublishedShare
	if err := json.NewDecoder(io.LimitReader(response.Body, 16*1024)).Decode(&received); err != nil {
		return nil, fmt.Errorf("解析配装分享服务响应失败: %w", err)
	}
	code, err := normalizeLoadoutShareShortCode(received.CompactCode)
	if err != nil {
		code, err = normalizeLoadoutShareShortCode(received.Code)
	}
	if err != nil {
		return nil, fmt.Errorf("配装分享服务返回了无效短码")
	}
	received.CompactCode = code
	received.Code = displayLoadoutShareShortCode(code)
	received.URL = endpoint + "/s/" + code
	received.DownloadURL = endpoint + "/download/" + code + ".gbfr-loadout"
	received.Bytes = len(frame)
	return &received, nil
}

func fetchLoadoutShareFrame(ctx context.Context, client *http.Client, endpoint, input string) ([]byte, error) {
	code, err := normalizeLoadoutShareShortCode(input)
	if err != nil {
		return nil, err
	}
	endpoint = strings.TrimRight(endpoint, "/")
	request, err := http.NewRequestWithContext(loadoutShareRequestContext(ctx), http.MethodGet, endpoint+"/api/v1/loadouts/"+code, nil)
	if err != nil {
		return nil, fmt.Errorf("创建配装下载请求失败: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.gbfr.loadout")
	request.Header.Set("User-Agent", repoName+"/"+appVersion)
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("连接配装分享服务失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("读取配装短码失败 (%d): %s", response.StatusCode, loadoutShareOnlineErrorMessage(response))
	}
	frame, err := io.ReadAll(io.LimitReader(response.Body, loadoutShareOnlineMaxFrameSize+1))
	if err != nil {
		return nil, fmt.Errorf("读取线上配装失败: %w", err)
	}
	if len(frame) == 0 || len(frame) > loadoutShareOnlineMaxFrameSize {
		return nil, fmt.Errorf("线上配装帧大小无效")
	}
	return frame, nil
}

func decodeLoadoutShareFrame(frame []byte) (*LoadoutShare, error) {
	code := loadoutShareCodePrefix + base64.RawURLEncoding.EncodeToString(frame)
	return decodeLoadoutShareCode(code)
}

func (a *App) PublishLoadoutShare(savePath string, unitID uint32) (*LoadoutPublishedShare, error) {
	encoded, err := a.LoadoutShareCode(savePath, unitID)
	if err != nil {
		return nil, err
	}
	frame, err := loadoutShareFrameFromCompatibilityCode(encoded.CompatibilityCode)
	if err != nil {
		return nil, err
	}
	return publishLoadoutShareFrame(a.ctx, loadoutShareHTTPClient(), loadoutShareServiceURL, frame)
}

func (a *App) LoadoutImportShortCode(savePath, expectCharaHash, input string) (*LoadoutImportDraft, error) {
	frame, err := fetchLoadoutShareFrame(a.ctx, loadoutShareHTTPClient(), loadoutShareServiceURL, input)
	if err != nil {
		return nil, err
	}
	share, err := decodeLoadoutShareFrame(frame)
	if err != nil {
		return nil, fmt.Errorf("线上配装完整性校验失败: %w", err)
	}
	return resolveLoadoutShare(savePath, expectCharaHash, share)
}
