package handlers

import (
	"context"
	"log"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/appservice"
	"maunium.net/go/mautrix/id"

	"nextcloud-media-bridge/src/config"
)

type RoomManager struct {
	config *config.Config
	as     *appservice.AppService
}

func NewRoomManager(cfg *config.Config, as *appservice.AppService) *RoomManager {
	return &RoomManager{config: cfg, as: as}
}

// JoinConfiguredRooms attempts to join all configured rooms at startup
func (rm *RoomManager) JoinConfiguredRooms(ctx context.Context) {
	if len(rm.config.Matrix.RoomPathTemplate) == 0 {
		log.Printf("No rooms configured in room_path_template")
		return
	}

	intent := rm.as.BotIntent()
	botMXID := rm.as.BotMXID()

	log.Printf("Attempting to join %d configured room(s)", len(rm.config.Matrix.RoomPathTemplate))

	for roomID := range rm.config.Matrix.RoomPathTemplate {
		parsedRoomID := id.RoomID(roomID)

		// Check if already joined
		joined, err := rm.isJoined(ctx, intent, parsedRoomID)
		if err != nil {
			log.Printf("Warning: Failed to check membership for room %s: %v", roomID, err)
			continue
		}

		if joined {
			log.Printf("Already in room %s", roomID)
			continue
		}

		// Try to join the room
		log.Printf("Attempting to join room %s", roomID)
		_, err = intent.JoinRoomByID(ctx, parsedRoomID)
		if err != nil {
			// Check if it's a permission error (room is private/invite-only)
			if httpErr, ok := err.(mautrix.HTTPError); ok {
				if httpErr.RespError != nil {
					errCode := httpErr.RespError.ErrCode
					if errCode == "M_FORBIDDEN" || errCode == "M_NOT_FOUND" {
						log.Printf("Warning: Cannot join room %s - room is private or doesn't exist. Bot user %s needs to be invited manually.",
							roomID, botMXID.String())
						continue
					}
				}
			}
			log.Printf("Warning: Failed to join room %s: %v - Bot may need to be invited.", roomID, err)
		} else {
			log.Printf("Successfully joined room %s", roomID)
		}
	}
}

// StartRoomMonitor periodically checks if the bot is still in configured rooms
func (rm *RoomManager) StartRoomMonitor(ctx context.Context, checkInterval time.Duration) {
	if len(rm.config.Matrix.RoomPathTemplate) == 0 {
		return
	}

	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rm.checkRoomMembership(ctx)
		}
	}
}

func (rm *RoomManager) checkRoomMembership(ctx context.Context) {
	intent := rm.as.BotIntent()

	for roomID := range rm.config.Matrix.RoomPathTemplate {
		parsedRoomID := id.RoomID(roomID)

		joined, err := rm.isJoined(ctx, intent, parsedRoomID)
		if err != nil {
			log.Printf("Warning: Failed to check membership for room %s: %v", roomID, err)
			continue
		}

		if !joined {
			log.Printf("Warning: Bridge is NOT in configured room %s! Media uploads will be skipped. Invite bot user @%s to this room.",
				roomID, rm.as.BotMXID().String())
		}
	}
}

func (rm *RoomManager) isJoined(ctx context.Context, intent *appservice.IntentAPI, roomID id.RoomID) (bool, error) {
	// Get joined rooms
	joinedResp, err := intent.JoinedRooms(ctx)
	if err != nil {
		return false, err
	}

	// Check if our room is in the list
	for _, joined := range joinedResp.JoinedRooms {
		if joined == roomID {
			return true, nil
		}
	}

	return false, nil
}
