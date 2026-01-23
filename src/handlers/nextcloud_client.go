package handlers

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

type NextcloudClient struct {
	BaseURL  string
	Username string
	Password string
	client   *http.Client
	dirCache sync.Map // Cache for created directories to avoid redundant MKCOL requests
}

func NewNextcloudClient(baseURL, username, password string) *NextcloudClient {
	// Configure HTTP client with connection pooling for better performance
	client := &http.Client{
		Timeout: 10 * time.Minute, // Long timeout for large file uploads
		Transport: &http.Transport{
			MaxIdleConns:        100,              // Total max idle connections
			MaxIdleConnsPerHost: 10,               // Max idle connections per host
			IdleConnTimeout:     90 * time.Second, // Keep connections alive
			DisableCompression:  false,            // Enable compression
			ForceAttemptHTTP2:   true,             // Prefer HTTP/2
		},
	}

	return &NextcloudClient{
		BaseURL:  baseURL,
		Username: username,
		Password: password,
		client:   client,
	}
}

func (c *NextcloudClient) UploadFile(remotePath, localPath string) error {
	file, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	return c.UploadReader(remotePath, file, 0)
}

func (c *NextcloudClient) CreateDirectory(remotePath string) error {
	// Check cache first to avoid redundant MKCOL requests
	if _, cached := c.dirCache.Load(remotePath); cached {
		return nil
	}

	start := time.Now()

	req, err := http.NewRequest("MKCOL", c.buildURL(remotePath), nil)
	if err != nil {
		return fmt.Errorf("failed to create directory request: %w", err)
	}
	req.SetBasicAuth(c.Username, c.Password)

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}
	defer resp.Body.Close()

	log.Printf("Nextcloud MKCOL path=%s status=%d duration=%s", remotePath, resp.StatusCode, time.Since(start))

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusConflict && resp.StatusCode != http.StatusMethodNotAllowed {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Cache successful directory creation
	c.dirCache.Store(remotePath, true)

	return nil
}

func (c *NextcloudClient) UploadReader(remotePath string, reader io.Reader, contentLength int64) error {
	start := time.Now()
	log.Printf("Nextcloud upload start path=%s content_length=%d", remotePath, contentLength)

	req, err := http.NewRequest("PUT", c.buildURL(remotePath), reader)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	if contentLength > 0 {
		req.ContentLength = contentLength
	}
	req.SetBasicAuth(c.Username, c.Password)

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to upload file: %w", err)
	}
	defer resp.Body.Close()

	log.Printf("Nextcloud upload finished path=%s status=%d duration=%s", remotePath, resp.StatusCode, time.Since(start))

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return nil
}

func (c *NextcloudClient) DownloadFile(remotePath string) (*http.Response, error) {
	req, err := http.NewRequest("GET", c.buildURL(remotePath), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.SetBasicAuth(c.Username, c.Password)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download file: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
	return resp, nil
}

func (c *NextcloudClient) Stat(remotePath string) (bool, int64, error) {
	req, err := http.NewRequest("HEAD", c.buildURL(remotePath), nil)
	if err != nil {
		return false, 0, fmt.Errorf("failed to create stat request: %w", err)
	}
	req.SetBasicAuth(c.Username, c.Password)

	resp, err := c.client.Do(req)
	if err != nil {
		return false, 0, fmt.Errorf("failed to stat file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return false, 0, nil
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return false, 0, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	contentLength := int64(-1)
	if lengthHeader := resp.Header.Get("Content-Length"); lengthHeader != "" {
		if parsed, err := strconv.ParseInt(lengthHeader, 10, 64); err == nil {
			contentLength = parsed
		}
	}

	return true, contentLength, nil
}

func (c *NextcloudClient) DeleteFile(remotePath string) error {
	req, err := http.NewRequest("DELETE", c.buildURL(remotePath), nil)
	if err != nil {
		return fmt.Errorf("failed to create delete request: %w", err)
	}
	req.SetBasicAuth(c.Username, c.Password)

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to delete file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return nil
}

func (c *NextcloudClient) buildURL(remotePath string) string {
	base := strings.TrimRight(c.BaseURL, "/")
	trimmed := strings.TrimLeft(remotePath, "/")
	if trimmed == "" {
		return base
	}
	parts := strings.Split(trimmed, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return base + "/" + strings.Join(parts, "/")
}

func (c *NextcloudClient) EnsureDirectories(remotePath string) error {
	trimmed := strings.TrimLeft(remotePath, "/")
	dirs := strings.Split(path.Dir(trimmed), "/")
	current := ""
	for _, dir := range dirs {
		if dir == "." || dir == "" {
			continue
		}
		if current == "" {
			current = dir
		} else {
			current = current + "/" + dir
		}
		if err := c.CreateDirectory(current); err != nil {
			return err
		}
	}
	return nil
}
