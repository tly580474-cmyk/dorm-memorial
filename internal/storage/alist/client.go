package alist

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	pathpkg "path"
	"strings"
	"sync"
	"time"

	"dorm-memorial/internal/storage"
)

type Config struct {
	BaseURL  string
	Token    string
	Username string
	Password string
	Root     string
	Client   *http.Client
}

type Client struct {
	baseURL  string
	tokenMu  sync.RWMutex
	token    string
	username string
	password string
	root     string
	http     *http.Client
	urlMu    sync.Mutex
	urlCache map[string]resolvedURL
}

type resolvedURL struct {
	value     string
	expiresAt time.Time
}

const resolvedURLTTL = 45 * time.Second

type apiEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type fileData struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	IsDir    bool   `json:"is_dir"`
	Modified string `json:"modified"`
	RawURL   string `json:"raw_url"`
	HashInfo struct {
		SHA256 string `json:"sha256"`
		SHA1   string `json:"sha1"`
		MD5    string `json:"md5"`
	} `json:"hash_info"`
}

type loginData struct {
	Token string `json:"token"`
}

func New(cfg Config) (*Client, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("alist base URL: %w", storage.ErrUnavailable)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("alist base URL scheme: %w", storage.ErrUnavailable)
	}

	root, err := cleanAbsolutePath(cfg.Root)
	if err != nil {
		return nil, fmt.Errorf("alist root: %w", err)
	}

	httpClient := cfg.Client
	if httpClient == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.DialContext = (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext
		transport.TLSHandshakeTimeout = 10 * time.Second
		// AList does not return response headers until the remote provider has
		// accepted the upload. Overseas routes can legitimately need tens of
		// minutes for the configured 500 MiB maximum video size.
		transport.ResponseHeaderTimeout = 45 * time.Minute
		transport.IdleConnTimeout = 90 * time.Second
		// Do not set http.Client.Timeout: it includes the entire response body
		// and would abort legitimate multi-gigabyte uploads and downloads.
		httpClient = &http.Client{Transport: transport}
	}

	return &Client{
		baseURL:  baseURL,
		token:    strings.TrimSpace(cfg.Token),
		username: strings.TrimSpace(cfg.Username),
		password: cfg.Password,
		root:     root,
		http:     httpClient,
		urlCache: make(map[string]resolvedURL),
	}, nil
}

// Authenticate obtains a fresh AList token for a dedicated service user.
// This avoids depending on a browser session token that may be invalidated
// when the user logs out or changes account settings.
func (c *Client) Authenticate(ctx context.Context) error {
	if c.username == "" || c.password == "" {
		return fmt.Errorf("alist username and password are required: %w", storage.ErrUnauthorized)
	}
	body, err := json.Marshal(map[string]string{
		"username": c.username,
		"password": c.password,
		"otp_code": "",
	})
	if err != nil {
		return fmt.Errorf("encode alist login request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/auth/login", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create alist login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	var data loginData
	if err := c.doEnvelope(req, &data); err != nil {
		return fmt.Errorf("alist login: %w", err)
	}
	if strings.TrimSpace(data.Token) == "" {
		return fmt.Errorf("alist login returned no token: %w", storage.ErrUnauthorized)
	}
	c.tokenMu.Lock()
	c.token = data.Token
	c.tokenMu.Unlock()
	return nil
}

func (c *Client) Put(ctx context.Context, objectPath string, body io.Reader, size int64) error {
	if body == nil || size < 0 {
		return fmt.Errorf("body is nil or content length is negative: %w", storage.ErrInvalidPath)
	}
	remotePath, err := c.remotePath(objectPath)
	if err != nil {
		return err
	}

	// Hide any io.Closer implemented by the caller. net/http closes request
	// bodies after sending, but ownership of the source reader remains with the
	// caller because probes may need to seek and verify it afterward.
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.baseURL+"/api/fs/put", io.NopCloser(body))
	if err != nil {
		return fmt.Errorf("create alist upload request: %w", err)
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("File-Path", url.PathEscape(remotePath))
	c.authorize(req)

	err = c.doEnvelope(req, nil)
	if err == nil {
		c.invalidateResolvedURL(objectPath)
	}
	return err
}

func (c *Client) Open(ctx context.Context, objectPath string) (io.ReadCloser, error) {
	resp, err := c.OpenRange(ctx, objectPath, "")
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (c *Client) OpenRange(ctx context.Context, objectPath, byteRange string) (*http.Response, error) {
	resp, err := c.openRange(ctx, objectPath, byteRange, false)
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.Body == nil {
		return nil, fmt.Errorf("download object returned an empty response: %w", storage.ErrUnavailable)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone || resp.StatusCode >= 500 {
		resp.Body.Close()
		c.invalidateResolvedURL(objectPath)
		resp, err = c.openRange(ctx, objectPath, byteRange, true)
		if err != nil {
			return nil, err
		}
		if resp == nil || resp.Body == nil {
			return nil, fmt.Errorf("download object returned an empty response: %w", storage.ErrUnavailable)
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, mapHTTPError(resp.StatusCode, "download object")
	}
	return resp, nil
}

func (c *Client) openRange(ctx context.Context, objectPath, byteRange string, refresh bool) (*http.Response, error) {
	rawURL, err := c.resolveURL(ctx, objectPath, refresh)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create alist download request: %w", err)
	}
	if byteRange != "" {
		req.Header.Set("Range", byteRange)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download object: %w", errors.Join(storage.ErrUnavailable, err))
	}
	return resp, nil
}

func (c *Client) Stat(ctx context.Context, objectPath string) (storage.ObjectInfo, error) {
	remotePath, err := c.remotePath(objectPath)
	if err != nil {
		return storage.ObjectInfo{}, err
	}
	var data fileData
	if err := c.postJSON(ctx, "/api/fs/get", map[string]any{"path": remotePath, "password": "", "refresh": true}, &data); err != nil {
		return storage.ObjectInfo{}, err
	}
	modified, _ := time.Parse(time.RFC3339Nano, data.Modified)
	return storage.ObjectInfo{
		Path:     objectPath,
		Name:     data.Name,
		Size:     data.Size,
		IsDir:    data.IsDir,
		Modified: modified,
		Hash:     firstNonEmpty(data.HashInfo.SHA256, data.HashInfo.SHA1, data.HashInfo.MD5),
	}, nil
}

// RefreshDirectory forces AList to refresh a directory from its backing driver.
// Some remote drivers acknowledge uploads before the new object is present in
// AList's directory cache, so callers should refresh the parent before Stat.
func (c *Client) RefreshDirectory(ctx context.Context, objectPath string) error {
	remotePath, err := c.remotePath(objectPath)
	if err != nil {
		return err
	}
	return c.postJSON(ctx, "/api/fs/list", map[string]any{
		"path":     remotePath,
		"password": "",
		"page":     1,
		"per_page": 1,
		"refresh":  true,
	}, nil)
}

func (c *Client) Delete(ctx context.Context, objectPath string) error {
	remotePath, err := c.remotePath(objectPath)
	if err != nil {
		return err
	}
	if remotePath == c.root {
		return fmt.Errorf("refusing to delete storage root: %w", storage.ErrInvalidPath)
	}
	err = c.postJSON(ctx, "/api/fs/remove", map[string]any{
		"dir":   pathpkg.Dir(remotePath),
		"names": []string{pathpkg.Base(remotePath)},
	}, nil)
	if err == nil {
		c.invalidateResolvedURL(objectPath)
	}
	return err
}

func (c *Client) Move(ctx context.Context, from, to string) error {
	remoteFrom, err := c.remotePath(from)
	if err != nil {
		return err
	}
	remoteTo, err := c.remotePath(to)
	if err != nil {
		return err
	}
	if remoteFrom == c.root || remoteTo == c.root {
		return fmt.Errorf("storage root cannot be moved: %w", storage.ErrInvalidPath)
	}

	srcDir, srcName := pathpkg.Dir(remoteFrom), pathpkg.Base(remoteFrom)
	dstDir, dstName := pathpkg.Dir(remoteTo), pathpkg.Base(remoteTo)
	if srcDir != dstDir {
		if err := c.postJSON(ctx, "/api/fs/move", map[string]any{
			"src_dir": srcDir,
			"dst_dir": dstDir,
			"names":   []string{srcName},
		}, nil); err != nil {
			return err
		}
	}
	if srcName != dstName {
		currentPath := pathpkg.Join(dstDir, srcName)
		if err := c.postJSON(ctx, "/api/fs/rename", map[string]any{
			"path": currentPath,
			"name": dstName,
		}, nil); err != nil {
			return fmt.Errorf("file moved but rename failed: %w", err)
		}
	}
	c.invalidateResolvedURL(from)
	c.invalidateResolvedURL(to)
	return nil
}

func (c *Client) ResolveURL(ctx context.Context, objectPath string) (string, error) {
	return c.resolveURL(ctx, objectPath, false)
}

func (c *Client) resolveURL(ctx context.Context, objectPath string, refresh bool) (string, error) {
	if !refresh {
		c.urlMu.Lock()
		cached, ok := c.urlCache[objectPath]
		c.urlMu.Unlock()
		if ok && time.Now().Before(cached.expiresAt) {
			storage.Describe(ctx, "alist_url", "hit")
			return cached.value, nil
		}
	}
	storage.Describe(ctx, "alist_url", "miss")
	defer storage.Time(ctx, "alist_resolve")()
	remotePath, err := c.remotePath(objectPath)
	if err != nil {
		return "", err
	}
	var data fileData
	if err := c.postJSON(ctx, "/api/fs/get", map[string]any{"path": remotePath, "password": "", "refresh": refresh}, &data); err != nil {
		return "", err
	}
	if strings.TrimSpace(data.RawURL) == "" {
		return "", fmt.Errorf("alist returned no raw URL: %w", storage.ErrUnavailable)
	}
	resolved, err := url.Parse(data.RawURL)
	if err != nil {
		return "", fmt.Errorf("invalid raw URL: %w", storage.ErrUnavailable)
	}
	if !resolved.IsAbs() {
		base, _ := url.Parse(c.baseURL + "/")
		resolved = base.ResolveReference(resolved)
	}
	if (resolved.Scheme != "http" && resolved.Scheme != "https") || resolved.Host == "" || resolved.User != nil {
		return "", fmt.Errorf("alist returned an unsafe raw URL: %w", storage.ErrUnavailable)
	}
	value := resolved.String()
	c.urlMu.Lock()
	c.urlCache[objectPath] = resolvedURL{value: value, expiresAt: time.Now().Add(resolvedURLTTL)}
	c.urlMu.Unlock()
	return value, nil
}

func (c *Client) invalidateResolvedURL(objectPath string) {
	c.urlMu.Lock()
	delete(c.urlCache, objectPath)
	c.urlMu.Unlock()
}

func (c *Client) postJSON(ctx context.Context, endpoint string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode alist request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create alist request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.authorize(req)
	return c.doEnvelope(req, out)
}

func (c *Client) doEnvelope(req *http.Request, out any) error {
	// API calls carry credentials and some of them carry replayable JSON or
	// upload bodies. Keep the caller's transport and redirect policy, but add a
	// second guard that refuses every API redirect. Raw object downloads use
	// c.http directly so a provider CDN can still redirect those GET requests.
	httpClient := *c.http
	checkRedirect := httpClient.CheckRedirect
	httpClient.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if checkRedirect != nil {
			if err := checkRedirect(next, via); err != nil {
				return err
			}
		}
		return fmt.Errorf("alist API redirect refused: %w", storage.ErrUnavailable)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("alist request: %w", errors.Join(storage.ErrUnavailable, err))
	}
	if resp == nil || resp.Body == nil {
		return fmt.Errorf("alist request returned an empty response: %w", storage.ErrUnavailable)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return mapHTTPError(resp.StatusCode, "alist request")
	}

	limited := io.LimitReader(resp.Body, 1<<20)
	var envelope apiEnvelope
	if err := json.NewDecoder(limited).Decode(&envelope); err != nil {
		return fmt.Errorf("decode alist response: %w", errors.Join(storage.ErrUnavailable, err))
	}
	if envelope.Code != 200 {
		return mapAPIError(envelope.Code, envelope.Message)
	}
	if out != nil && len(envelope.Data) > 0 && string(envelope.Data) != "null" {
		if err := json.Unmarshal(envelope.Data, out); err != nil {
			return fmt.Errorf("decode alist response data: %w", errors.Join(storage.ErrUnavailable, err))
		}
	}
	return nil
}

func (c *Client) authorize(req *http.Request) {
	c.tokenMu.RLock()
	token := c.token
	c.tokenMu.RUnlock()
	if token != "" {
		req.Header.Set("Authorization", token)
	}
}

func (c *Client) remotePath(objectPath string) (string, error) {
	cleaned, err := cleanAbsolutePath(objectPath)
	if err != nil {
		return "", err
	}
	if cleaned == "/" {
		return c.root, nil
	}
	return pathpkg.Join(c.root, strings.TrimPrefix(cleaned, "/")), nil
}

func cleanAbsolutePath(value string) (string, error) {
	if value == "" {
		return "/", nil
	}
	if strings.ContainsRune(value, '\x00') || strings.Contains(value, "\\") {
		return "", storage.ErrInvalidPath
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "." || segment == ".." {
			return "", storage.ErrInvalidPath
		}
	}
	cleaned := pathpkg.Clean("/" + strings.TrimPrefix(value, "/"))
	if cleaned == "/." {
		return "/", nil
	}
	return cleaned, nil
}

func mapHTTPError(status int, operation string) error {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%s: %w", operation, storage.ErrUnauthorized)
	case http.StatusNotFound:
		return fmt.Errorf("%s: %w", operation, storage.ErrNotFound)
	default:
		return fmt.Errorf("%s returned HTTP %d: %w", operation, status, storage.ErrUnavailable)
	}
}

func mapAPIError(code int, message string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		message = "unknown error"
	}
	switch code {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("alist: %s: %w", message, storage.ErrUnauthorized)
	case http.StatusNotFound:
		return fmt.Errorf("alist: %s: %w", message, storage.ErrNotFound)
	default:
		return fmt.Errorf("alist code %d: %s: %w", code, message, storage.ErrUnavailable)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

var _ storage.ObjectStorage = (*Client)(nil)
