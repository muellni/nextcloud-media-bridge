package handlers

import (
	"context"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"path"
	"reflect"
	"strings"
	"time"

	"maunium.net/go/mautrix/appservice"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"nextcloud-media-bridge/src/config"
	"nextcloud-media-bridge/src/utils"
)

type MediaHandler struct {
	config        *config.Config
	nextcloud     *NextcloudClient
	mediaIDSecret []byte
	as            *appservice.AppService
	cryptoHelper  *CryptoHelper
}

var nextcloudMediaStateEvent = event.Type{Type: "com.nextcloud-media-bridge.media", Class: event.StateEventType}

func init() {
	event.TypeMap[nextcloudMediaStateEvent] = reflect.TypeOf(mediaState{})
	gob.Register(&mediaState{})
}

func NewMediaHandler(cfg *config.Config, nextcloud *NextcloudClient, mediaIDSecret []byte, as *appservice.AppService, cryptoHelper *CryptoHelper) *MediaHandler {
	return &MediaHandler{config: cfg, nextcloud: nextcloud, mediaIDSecret: mediaIDSecret, as: as, cryptoHelper: cryptoHelper}
}

func (h *MediaHandler) HandleMatrixEvent(ctx context.Context, as *appservice.AppService, evt *event.Event) error {
	if evt.Type == event.EventRedaction {
		return h.handleRedactionEvent(ctx, as, evt)
	}
	if evt.Type != event.EventMessage {
		return nil
	}
	if evt.Sender == as.BotMXID() {
		return nil
	}
	// Only process rooms that have a path template configured
	roomIDStr := evt.RoomID.String()
	pathTemplate, hasTemplate := h.config.Matrix.RoomPathTemplate[roomIDStr]
	if !hasTemplate {
		log.Printf("Skipping event in room %s (no path template configured)", roomIDStr)
		return nil
	}

	if err := evt.Content.ParseRaw(evt.Type); err != nil {
		if err != event.ErrContentAlreadyParsed {
			log.Printf("ParseRaw failed for event %s in room %s: %v", evt.ID.String(), evt.RoomID.String(), err)
			return nil
		}
	}
	msg := evt.Content.AsMessage()
	if msg != nil {
		log.Printf("Message details: msgtype=%s url=%s hasFile=%v filename=%s", msg.MsgType, msg.URL, msg.File != nil, msg.GetFileName())
	}
	if msg == nil || !msg.MsgType.IsMedia() {
		log.Printf("Skipping non-media message %s in room %s", evt.ID.String(), evt.RoomID.String())
		return nil
	}
	if msg.RelatesTo != nil && msg.RelatesTo.Type == event.RelReplace {
		log.Printf("Skipping edit event %s in room %s", evt.ID.String(), evt.RoomID.String())
		return nil
	}
	// Handle encrypted vs unencrypted media
	var mediaData []byte
	var parsedURL id.ContentURI
	var mimeType string

	if msg.File != nil {
		// Encrypted media detected
		if h.cryptoHelper == nil {
			log.Printf("Skipping encrypted media (E2EE not enabled)")
			return nil
		}

		log.Printf("Processing encrypted media from %s", evt.Sender.String())

		// Parse the encrypted file URL
		var err error
		parsedURL, err = msg.File.URL.Parse()
		if err != nil {
			return fmt.Errorf("failed to parse encrypted media URL: %w", err)
		}

		// Download the encrypted file
		client := h.as.BotClient()
		encryptedResp, err := client.Download(ctx, parsedURL)
		if err != nil {
			return fmt.Errorf("failed to download encrypted media: %w", err)
		}
		defer encryptedResp.Body.Close()

		// Read encrypted data
		encryptedData, err := io.ReadAll(encryptedResp.Body)
		if err != nil {
			return fmt.Errorf("failed to read encrypted media: %w", err)
		}

		// Prepare encryption info for decryption
		if err := msg.File.PrepareForDecryption(); err != nil {
			return fmt.Errorf("failed to prepare for decryption: %w", err)
		}

		// Decrypt the file
		mediaData, err = msg.File.Decrypt(encryptedData)
		if err != nil {
			return fmt.Errorf("failed to decrypt media file: %w", err)
		}

		log.Printf("Successfully decrypted media %s (%d bytes)", evt.ID.String(), len(mediaData))

		// Get MIME type from message info
		if msg.Info != nil && msg.Info.MimeType != "" {
			mimeType = msg.Info.MimeType
		}
	} else {
		// Unencrypted media
		if msg.URL == "" {
			log.Printf("Skipping media message without URL %s", evt.ID.String())
			return nil
		}

		var err error
		parsedURL, err = msg.URL.Parse()
		if err != nil {
			return fmt.Errorf("failed to parse media URL: %w", err)
		}

		// Skip already-proxied media
		if parsedURL.Homeserver == h.config.MediaProxy.ServerName {
			if _, err := utils.DecodeMediaID(h.mediaIDSecret, parsedURL.FileID); err == nil {
				log.Printf("Skipping already-proxied media %s (homeserver=%s)", evt.ID.String(), parsedURL.Homeserver)
				return nil
			}
			log.Printf("Media URL homeserver matches proxy but media ID is not ours; continuing")
		}

		// Download unencrypted media
		client := as.BotClient()
		log.Printf("Downloading media %s from %s", evt.ID.String(), msg.URL)
		resp, err := client.Download(ctx, parsedURL)
		if err != nil {
			return fmt.Errorf("failed to download media: %w", err)
		}
		defer resp.Body.Close()

		// Read media data
		mediaData, err = io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("failed to read media: %w", err)
		}

		// Get MIME type from response or message
		mimeType = resp.Header.Get("Content-Type")
		if msg.Info != nil && msg.Info.MimeType != "" {
			mimeType = msg.Info.MimeType
		}
	}

	filename := msg.GetFileName()
	if filename == "" {
		filename = fmt.Sprintf("file_%d", time.Now().Unix())
		log.Printf("Warning: No filename in message, using generated name: %s", filename)
	}
	roomName := h.getRoomName(evt.RoomID)
	roomSegment := utils.SanitizePathSegment(roomName)
	userSegment := utils.SanitizePathSegment(utils.MatrixUserLocalpart(evt.Sender.String()))
	fileSegment := utils.SanitizePathSegment(filename)

	log.Printf("Template variables: room=%s, user=%s, file=%s", roomSegment, userSegment, fileSegment)

	// Render the path template with sanitized values
	eventTime := time.UnixMilli(evt.Timestamp).UTC()
	nextcloudPath := utils.RenderPathTemplate(pathTemplate, roomSegment, userSegment, fileSegment, eventTime)
	log.Printf("Uploading to Nextcloud path %s", nextcloudPath)
	if err := h.nextcloud.EnsureDirectories(nextcloudPath); err != nil {
		return fmt.Errorf("failed to create directories: %w", err)
	}

	finalPath := nextcloudPath
	finalFilename := filename
	uploadNeeded := true
	contentLength := int64(len(mediaData))

	if exists, size, err := h.nextcloud.Stat(nextcloudPath); err != nil {
		return fmt.Errorf("failed to check existing file: %w", err)
	} else if exists {
		if contentLength > 0 && size == contentLength {
			log.Printf("Nextcloud file already exists with matching size, reusing path %s", nextcloudPath)
			uploadNeeded = false
		} else {
			for i := 1; i <= 1000; i++ {
				candidatePath, candidateName := addCounterSuffix(nextcloudPath, i)
				exists, _, err := h.nextcloud.Stat(candidatePath)
				if err != nil {
					return fmt.Errorf("failed to check existing file: %w", err)
				}
				if !exists {
					finalPath = candidatePath
					finalFilename = candidateName
					log.Printf("Nextcloud file exists, using new path %s", finalPath)
					break
				}
			}
			if finalPath == nextcloudPath {
				return fmt.Errorf("failed to find available filename for %s", nextcloudPath)
			}
		}
	}
	if uploadNeeded {
		// Upload from mediaData byte array
		mediaReader := strings.NewReader(string(mediaData))
		if err := h.nextcloud.UploadReader(finalPath, mediaReader, contentLength); err != nil {
			return fmt.Errorf("failed to upload to nextcloud: %w", err)
		}
	}

	mediaID, err := utils.EncodeMediaID(h.mediaIDSecret, utils.MediaRef{
		Path:     strings.TrimLeft(finalPath, "/"),
		FileName: finalFilename,
		MimeType: mimeType,
	})
	if err != nil {
		return fmt.Errorf("failed to create media id: %w", err)
	}

	mxc := id.ContentURI{Homeserver: h.config.MediaProxy.ServerName, FileID: mediaID}.String()

	newInfo := msg.Info
	if newInfo == nil {
		newInfo = &event.FileInfo{}
	}
	newInfo.MimeType = mimeType
	if contentLength > 0 {
		newInfo.Size = int(contentLength)
	}

	// Generate Nextcloud web link if configured
	var nextcloudLink string
	if h.config.Nextcloud.WebURL != "" && !h.config.Nextcloud.DisableWebLink {
		nextcloudLink = utils.GenerateNextcloudWebLink(h.config.Nextcloud.WebURL, finalPath)
	}

	// Try to edit the original message to replace the mxc:// URL
	log.Printf("Attempting to edit original event %s to replace media URL", evt.ID.String())

	// Prepare message bodies with optional Nextcloud link
	editedBody := msg.Body
	if editedBody == filename {
		editedBody = ""
	}
	if nextcloudLink != "" {
		editedBody = editedBody + "\n\nView in Nextcloud: " + nextcloudLink
	}

	editContent := &event.MessageEventContent{
		MsgType:  msg.MsgType,
		Body:     "* " + editedBody, // Fallback for clients that don't support edits
		FileName: finalFilename,
		URL:      id.ContentURIString(mxc),
		Info:     newInfo,
		RelatesTo: &event.RelatesTo{
			Type:    event.RelReplace,
			EventID: evt.ID,
		},
		NewContent: &event.MessageEventContent{
			MsgType:  msg.MsgType,
			Body:     editedBody,
			FileName: finalFilename,
			URL:      id.ContentURIString(mxc),
			Info:     newInfo,
		},
	}

	// Try to send as the original user (this might fail if we don't have permission)
	intent := h.as.Intent(evt.Sender)
	_, err = intent.SendMessageEvent(ctx, evt.RoomID, event.EventMessage, editContent)
	if err != nil {
		log.Printf("Failed to edit original message as user: %v", err)
		return nil // Don't delete media if we couldn't edit the message
	}

	log.Printf("Successfully edited original message %s", evt.ID.String())
	h.storeMediaState(evt.RoomID, evt.ID, finalPath, finalFilename, mxc)

	// Delete the original media from the Matrix homeserver to save disk space
	// This happens after successful upload to Nextcloud and message replacement
	if h.config.Matrix.Admin.Enabled {
		log.Printf("Deleting original media %s from homeserver %s", parsedURL.FileID, parsedURL.Homeserver)
		if err := h.deleteLocalMedia(ctx, parsedURL.Homeserver, parsedURL.FileID); err != nil {
			// Log error but don't fail the entire operation
			// The media is already in Nextcloud and the message was edited successfully
			log.Printf("Warning: failed to delete media from homeserver: %v", err)
		}
	}

	return nil
}

func (h *MediaHandler) handleRedactionEvent(ctx context.Context, as *appservice.AppService, evt *event.Event) error {
	log.Printf("Received redaction event %s in room %s", evt.ID.String(), evt.RoomID.String())
	if err := evt.Content.ParseRaw(evt.Type); err != nil {
		if err != event.ErrContentAlreadyParsed {
			log.Printf("ParseRaw failed for redaction event %s in room %s: %v", evt.ID.String(), evt.RoomID.String(), err)
			return nil
		}
	}

	redacts := evt.Redacts
	if content := evt.Content.AsRedaction(); content != nil && content.Redacts != "" {
		redacts = content.Redacts
	}
	if redacts == "" {
		log.Printf("Redaction event %s missing redacts field", evt.ID.String())
		return nil
	}
	log.Printf("Redaction event %s targets %s", evt.ID.String(), redacts.String())

	return h.deleteFromStateEvent(as, evt.RoomID, redacts)
}

type mediaState struct {
	Path      string `json:"path"`
	FileName  string `json:"filename,omitempty"`
	MXC       string `json:"mxc,omitempty"`
	Signature string `json:"signature"`
}

func (h *MediaHandler) storeMediaState(roomID id.RoomID, eventID id.EventID, filePath, filename, mxc string) {
	content := mediaState{Path: filePath, FileName: filename, MXC: mxc}
	if signature, err := h.signMediaState(content); err != nil {
		log.Printf("Failed to sign Nextcloud state for event %s: %v", eventID.String(), err)
		return
	} else {
		content.Signature = signature
	}
	if _, err := h.as.BotClient().SendStateEvent(context.Background(), roomID, nextcloudMediaStateEvent, eventID.String(), content); err != nil {
		log.Printf("Failed to store Nextcloud state for event %s: %v", eventID.String(), err)
	}
}

func (h *MediaHandler) deleteFromStateEvent(as *appservice.AppService, roomID id.RoomID, eventID id.EventID) error {
	client := as.BotClient()
	var state mediaState
	if err := client.StateEvent(context.Background(), roomID, nextcloudMediaStateEvent, eventID.String(), &state); err != nil {
		log.Printf("No stored Nextcloud state for event %s: %v", eventID.String(), err)
		return nil
	}
	if state.Path == "" {
		log.Printf("Stored Nextcloud state for event %s missing path", eventID.String())
		return nil
	}
	if state.Signature == "" {
		log.Printf("Stored Nextcloud state for event %s missing signature", eventID.String())
		return nil
	}
	if ok, err := h.verifyMediaState(state); err != nil {
		log.Printf("Failed to verify Nextcloud state for event %s: %v", eventID.String(), err)
		return nil
	} else if !ok {
		log.Printf("Invalid Nextcloud state signature for event %s", eventID.String())
		return nil
	}
	if err := h.nextcloud.DeleteFile(state.Path); err != nil {
		return fmt.Errorf("failed to delete Nextcloud file %s: %w", state.Path, err)
	}
	log.Printf("Deleted Nextcloud file %s for redacted event %s using stored state", state.Path, eventID.String())
	return nil
}

func (h *MediaHandler) signMediaState(state mediaState) (string, error) {
	state.Signature = ""
	payload, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	return utils.SignBytes(h.mediaIDSecret, payload), nil
}

func (h *MediaHandler) verifyMediaState(state mediaState) (bool, error) {
	if state.Signature == "" {
		return false, nil
	}
	signature := state.Signature
	state.Signature = ""
	payload, err := json.Marshal(state)
	if err != nil {
		return false, err
	}
	return utils.VerifyBytes(h.mediaIDSecret, payload, signature), nil
}

func (h *MediaHandler) getRoomName(roomID id.RoomID) string {
	return strings.TrimPrefix(roomID.String(), "!")
}

func addCounterSuffix(remotePath string, counter int) (string, string) {
	dir := path.Dir(remotePath)
	base := path.Base(remotePath)
	ext := path.Ext(base)
	name := strings.TrimSuffix(base, ext)
	newBase := fmt.Sprintf("%s_%d%s", name, counter, ext)
	if dir == "." {
		return newBase, newBase
	}
	newPath := path.Join(dir, newBase)
	if strings.HasPrefix(remotePath, "/") && !strings.HasPrefix(newPath, "/") {
		newPath = "/" + newPath
	}
	return newPath, newBase
}

// deleteLocalMedia deletes media from the Matrix homeserver using Synapse admin API
func (h *MediaHandler) deleteLocalMedia(ctx context.Context, serverName, mediaID string) error {
	if !h.config.Matrix.Admin.Enabled {
		log.Printf("Skipping media deletion: admin API not enabled")
		return nil
	}

	if h.config.Matrix.Admin.AccessToken == "" {
		log.Printf("Skipping media deletion: no admin access token configured")
		return nil
	}

	// Build the Synapse admin API URL
	// DELETE /_synapse/admin/v1/media/<server_name>/<media_id>
	deleteURL := fmt.Sprintf("%s/_synapse/admin/v1/media/%s/%s",
		strings.TrimRight(h.config.Matrix.HomeserverURL, "/"),
		serverName,
		mediaID)

	req, err := http.NewRequestWithContext(ctx, "DELETE", deleteURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create delete request: %w", err)
	}

	// Set authorization header with admin access token
	req.Header.Set("Authorization", "Bearer "+h.config.Matrix.Admin.AccessToken)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to delete media: %w", err)
	}
	defer resp.Body.Close()

	// Read response body for logging
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	// Parse response to log deleted media count
	var deleteResp struct {
		DeletedMedia []string `json:"deleted_media"`
		Total        int      `json:"total"`
	}
	if err := json.Unmarshal(body, &deleteResp); err != nil {
		log.Printf("Warning: failed to parse delete response: %v", err)
	} else {
		log.Printf("Successfully deleted %d media item(s) from homeserver: %v", deleteResp.Total, deleteResp.DeletedMedia)
	}

	return nil
}
