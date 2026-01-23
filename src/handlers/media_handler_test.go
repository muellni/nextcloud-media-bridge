package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"maunium.net/go/mautrix/appservice"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"nextcloud-media-bridge/src/config"
	"nextcloud-media-bridge/src/utils"
)

func TestHandleMatrixEvent(t *testing.T) {
	const (
		mediaBody = "file-bytes"
		roomID    = "!roomid:example.com"
	)

	var uploadedPath string
	var sentContent event.MessageEventContent

	// Fake Nextcloud server
	nt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "MKCOL":
			w.WriteHeader(http.StatusCreated)
		case "PUT":
			uploadedPath = r.URL.Path
			body, _ := io.ReadAll(r.Body)
			_ = r.Body.Close()
			if string(body) != mediaBody {
				t.Fatalf("unexpected upload body: %s", string(body))
			}
			w.WriteHeader(http.StatusCreated)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer nt.Close()

	// Fake Matrix homeserver
	mt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/register") {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		if strings.Contains(r.URL.Path, "/joined_rooms") {
			_, _ = w.Write([]byte(`{"joined_rooms":["` + roomID + `"]}`))
			return
		}
		if strings.Contains(r.URL.Path, "/joined_members/") {
			_, _ = w.Write([]byte(`{"joined":{}}`))
			return
		}
		if strings.Contains(r.URL.Path, "/rooms/") && strings.HasSuffix(r.URL.Path, "/join") {
			_, _ = w.Write([]byte(`{"room_id":"` + roomID + `"}`))
			return
		}
		if strings.Contains(r.URL.Path, "/media/download/") {
			w.Header().Set("Content-Type", "image/jpeg")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(mediaBody))
			return
		}
		if strings.Contains(r.URL.Path, "/send/m.room.message/") {
			payload, _ := io.ReadAll(r.Body)
			_ = r.Body.Close()
			var content event.MessageEventContent
			if err := json.Unmarshal(payload, &content); err != nil {
				t.Fatalf("failed to parse sent content: %v", err)
			}
			sentContent = content
			_, _ = w.Write([]byte(`{"event_id":"$proxy"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer mt.Close()

	registration := &appservice.Registration{
		ID:              "nextcloud-media-bridge",
		URL:             "http://localhost",
		AppToken:        "app_token",
		ServerToken:     "server_token",
		SenderLocalpart: "bridge",
	}

	as, err := appservice.CreateFull(appservice.CreateOpts{
		Registration:     registration,
		HomeserverDomain: "example.com",
		HomeserverURL:    mt.URL,
		HostConfig:       appservice.HostConfig{Hostname: "127.0.0.1", Port: 0},
	})
	if err != nil {
		t.Fatalf("failed to create appservice: %v", err)
	}

	cfg := &config.Config{}
	cfg.Nextcloud.BaseURL = nt.URL
	cfg.Nextcloud.Username = "testuser"
	cfg.Nextcloud.Password = "testpass"
	cfg.Matrix.RoomPathTemplate = map[string]string{roomID: "/media/${year}/ostsee/${user}/${file}"}
	cfg.MediaProxy.ServerName = "media.example.com"
	secret := []byte("secret")

	handler := NewMediaHandler(cfg, NewNextcloudClient(cfg.Nextcloud.BaseURL, cfg.Nextcloud.Username, cfg.Nextcloud.Password), secret, as)

	content := event.MessageEventContent{
		MsgType: event.MsgImage,
		Body:    "image.jpg",
		URL:     id.ContentURIString("mxc://example.com/abc"),
		Info:    &event.FileInfo{MimeType: "image/jpeg"},
	}
	raw, _ := json.Marshal(content)
	eventTime := time.Date(2024, 5, 20, 12, 0, 0, 0, time.UTC)

	evt := &event.Event{
		Type:      event.EventMessage,
		RoomID:    id.RoomID(roomID),
		Sender:    id.UserID("@alice:example.com"),
		Timestamp: eventTime.UnixMilli(),
		Content: event.Content{
			VeryRaw: raw,
		},
	}

	if err := handler.HandleMatrixEvent(context.Background(), as, evt); err != nil {
		t.Fatalf("HandleMatrixEvent failed: %v", err)
	}

	if sentContent.URL == "" {
		t.Fatalf("expected proxy URL to be sent")
	}
	if !strings.HasPrefix(string(sentContent.URL), "mxc://media.example.com/") {
		t.Fatalf("unexpected proxy url: %s", sentContent.URL)
	}

	decoded, err := utils.DecodeMediaID(secret, strings.TrimPrefix(string(sentContent.URL), "mxc://media.example.com/"))
	if err != nil {
		t.Fatalf("failed to decode proxy url: %v", err)
	}
	if decoded.FileName != "image.jpg" {
		t.Fatalf("unexpected decoded filename: %s", decoded.FileName)
	}

	if !strings.Contains(uploadedPath, fmt.Sprintf("/media/%d/ostsee/alice/image.jpg", eventTime.Year())) {
		t.Fatalf("unexpected upload path: %s", uploadedPath)
	}
}
