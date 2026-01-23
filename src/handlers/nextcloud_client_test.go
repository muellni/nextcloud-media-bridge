package handlers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestNextcloudClientUploadAndDownload(t *testing.T) {
	var uploadedBody string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "PUT":
			body, _ := io.ReadAll(r.Body)
			_ = r.Body.Close()
			uploadedBody = string(body)
			w.WriteHeader(http.StatusCreated)
		case "GET":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("downloaded"))
		case "MKCOL":
			w.WriteHeader(http.StatusCreated)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	tmp, err := os.CreateTemp("", "nextcloud-upload-*.txt")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	_, _ = tmp.Write([]byte("uploaded"))
	_ = tmp.Close()
	defer os.Remove(tmp.Name())

	client := NewNextcloudClient(server.URL, "user", "pass")
	if err := client.EnsureDirectories("media/2026/room/user/file.txt"); err != nil {
		t.Fatalf("EnsureDirectories failed: %v", err)
	}
	if err := client.UploadFile("media/2026/room/user/file.txt", tmp.Name()); err != nil {
		t.Fatalf("UploadFile failed: %v", err)
	}
	if uploadedBody != "uploaded" {
		t.Fatalf("unexpected upload body: %s", uploadedBody)
	}
	resp, err := client.DownloadFile("media/2026/room/user/file.txt")
	if err != nil {
		t.Fatalf("DownloadFile failed: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if string(data) != "downloaded" {
		t.Fatalf("unexpected download body: %s", string(data))
	}
}
