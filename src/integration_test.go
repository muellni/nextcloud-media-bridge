package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/federation"

	"nextcloud-media-bridge/src/config"
	"nextcloud-media-bridge/src/handlers"
	"nextcloud-media-bridge/src/utils"
)

func TestMediaProxyDownload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("proxy-data"))
	}))
	defer server.Close()

	cfg := &config.Config{}
	cfg.MediaProxy.ServerName = "media.example.com"
	cfg.MediaProxy.ServerKey = federation.GenerateSigningKey().SynapseString()
	cfg.MediaProxy.HMACSecret = "secret"
	secret := []byte(cfg.MediaProxy.HMACSecret)

	nextcloud := handlers.NewNextcloudClient(server.URL, "user", "pass")
	proxy, err := handlers.NewMediaProxy(cfg, nextcloud, secret)
	if err != nil {
		t.Fatalf("failed to create media proxy: %v", err)
	}

	mediaID, err := utils.EncodeMediaID(secret, utils.MediaRef{Path: "media/file.txt", FileName: "file.txt", MimeType: "text/plain"})
	if err != nil {
		t.Fatalf("failed to encode media id: %v", err)
	}

	router := http.NewServeMux()
	proxy.RegisterRoutes(router, zerolog.Nop())

	request := httptest.NewRequest(http.MethodGet, "http://example.com/_matrix/client/v1/media/download/media.example.com/"+mediaID, nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", response.Code)
	}
}
