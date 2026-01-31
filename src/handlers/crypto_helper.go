package handlers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/zerolog"
	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/appservice"
	"maunium.net/go/mautrix/crypto"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"nextcloud-media-bridge/src/config"
)

// stateStore wraps the crypto store to implement the crypto.StateStore interface
type stateStore struct {
	*crypto.SQLCryptoStore
}

// IsEncrypted checks if a room is encrypted (assume all rooms are for simplicity)
func (s *stateStore) IsEncrypted(ctx context.Context, roomID id.RoomID) (bool, error) {
	// For a bot that only decrypts, we can assume rooms with encrypted messages are encrypted
	// This is a simplified implementation - in production you'd query the state
	return true, nil
}

// GetEncryptionEvent returns the encryption event content for a room
func (s *stateStore) GetEncryptionEvent(ctx context.Context, roomID id.RoomID) (*event.EncryptionEventContent, error) {
	// Return default encryption settings for Megolm
	return &event.EncryptionEventContent{
		Algorithm:              id.AlgorithmMegolmV1,
		RotationPeriodMillis:   604800000, // 7 days
		RotationPeriodMessages: 100,
	}, nil
}

// FindSharedRooms returns encrypted rooms shared with a user
func (s *stateStore) FindSharedRooms(ctx context.Context, userID id.UserID) ([]id.RoomID, error) {
	// Simple implementation - return empty list since we don't need this for decryption
	return []id.RoomID{}, nil
}

type CryptoHelper struct {
	config *config.Config
	as     *appservice.AppService
	client *mautrix.Client
	mach   *crypto.OlmMachine
	store  *crypto.SQLCryptoStore
	log    zerolog.Logger
}

func NewCryptoHelper(cfg *config.Config, as *appservice.AppService) (*CryptoHelper, error) {
	if !cfg.Matrix.Encryption.Enabled {
		return nil, nil
	}

	if cfg.Matrix.Encryption.PickleKey == "" {
		return nil, fmt.Errorf("encryption enabled but pickle_key not set")
	}

	if cfg.Matrix.Encryption.DatabasePath == "" {
		return nil, fmt.Errorf("encryption enabled but database_path not set")
	}

	log := as.Log.With().Str("component", "crypto").Logger()

	return &CryptoHelper{
		config: cfg,
		as:     as,
		log:    log,
	}, nil
}

func (h *CryptoHelper) Init(ctx context.Context) error {
	h.log.Info().Msg("Initializing end-to-end encryption support")

	// Initialize SQLite database for crypto store
	db, err := dbutil.NewWithDialect(h.config.Matrix.Encryption.DatabasePath, "sqlite3")
	if err != nil {
		return fmt.Errorf("failed to open crypto database: %w", err)
	}

	h.log.Debug().Str("database_path", h.config.Matrix.Encryption.DatabasePath).Msg("Crypto database opened")

	// Create crypto store
	h.store = crypto.NewSQLCryptoStore(
		db,
		dbutil.ZeroLogger(h.log.With().Str("db_section", "crypto").Logger()),
		"nextcloud-media-bridge",
		"",
		[]byte(h.config.Matrix.Encryption.PickleKey),
	)

	// Upgrade database schema (creates tables if needed)
	err = h.store.DB.Upgrade(ctx)
	if err != nil {
		return fmt.Errorf("failed to upgrade crypto database: %w", err)
	}

	h.log.Debug().Msg("Crypto database schema upgraded")

	// Find existing device ID or create new one
	deviceID, err := h.store.FindDeviceID(ctx)
	if err != nil {
		return fmt.Errorf("failed to find existing device ID: %w", err)
	}

	var isExistingDevice bool
	if len(deviceID) > 0 {
		h.log.Debug().Stringer("device_id", deviceID).Msg("Found existing device ID in database")
		isExistingDevice = true
	}

	// Create Matrix client for the bot
	h.client = h.as.NewMautrixClient(h.as.BotMXID())

	// Create or reuse device using MSC4190 (PUT /devices/{deviceID})
	// This works with latest Synapse and doesn't require appservice login flow
	if deviceID == "" {
		// No existing device, create one
		h.log.Debug().Msg("Creating new device for bot using MSC4190")
		err = h.client.CreateDeviceMSC4190(ctx, "", "Nextcloud Media Bridge")
		if err != nil {
			return fmt.Errorf("failed to create device for bot: %w", err)
		}
		h.log.Info().Stringer("device_id", h.client.DeviceID).Msg("Created new device for bot")
	} else {
		// Reuse existing device
		h.log.Debug().Stringer("device_id", deviceID).Msg("Reusing existing device ID")
		h.client.DeviceID = deviceID
		h.client.SetAppServiceDeviceID = true
	}

	h.store.DeviceID = h.client.DeviceID

	// Initialize Olm machine with wrapped state store
	wrappedStore := &stateStore{SQLCryptoStore: h.store}
	h.mach = crypto.NewOlmMachine(h.client, &h.log, h.store, wrappedStore)

	// Configure the Olm machine for a bot (simplified settings)
	h.mach.AllowKeyShare = h.rejectAllKeySharing // Never share keys
	h.mach.DisableSharedGroupSessionTracking = true

	// Set the crypto store and syncer on the client
	h.client.Store = h.store
	h.client.Syncer = &cryptoSyncer{h.mach}

	// Load existing crypto state
	err = h.mach.Load(ctx)
	if err != nil {
		return fmt.Errorf("failed to load crypto machine: %w", err)
	}

	// Verify keys are still on server if this is an existing device
	if isExistingDevice {
		if !h.verifyKeysOnServer(ctx) {
			h.log.Warn().Msg("Existing device keys not found on server, will generate new ones")
		}
	} else {
		// Share device keys with the server
		err = h.mach.ShareKeys(ctx, -1)
		if err != nil {
			return fmt.Errorf("failed to share device keys: %w", err)
		}
		h.log.Info().Msg("Shared new device keys with server")
	}

	h.log.Info().Msg("End-to-end encryption initialized successfully")
	return nil
}

func (h *CryptoHelper) Start() {
	h.log.Info().Msg("Starting crypto syncer for to-device messages")
	go func() {
		ctx := context.Background()
		err := h.client.SyncWithContext(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			h.log.Error().Err(err).Msg("Crypto syncer stopped with error")
			os.Exit(51)
		}
		h.log.Info().Msg("Crypto syncer stopped")
	}()
}

func (h *CryptoHelper) Stop() {
	h.log.Info().Msg("Stopping crypto syncer")
	h.client.StopSync()
}

func (h *CryptoHelper) Decrypt(ctx context.Context, evt *event.Event) (*event.Event, error) {
	if evt.Type != event.EventEncrypted {
		return evt, nil // Not encrypted, return as-is
	}

	decrypted, err := h.mach.DecryptMegolmEvent(ctx, evt)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt event: %w", err)
	}

	return decrypted, nil
}

// rejectAllKeySharing rejects all key share requests (bot doesn't share keys)
func (h *CryptoHelper) rejectAllKeySharing(ctx context.Context, device *id.Device, info event.RequestedKeyInfo) *crypto.KeyShareRejection {
	return &crypto.KeyShareRejectNoResponse
}

func (h *CryptoHelper) verifyKeysOnServer(ctx context.Context) bool {
	h.log.Debug().Msg("Verifying keys are still on server")
	resp, err := h.client.QueryKeys(ctx, &mautrix.ReqQueryKeys{
		DeviceKeys: map[id.UserID]mautrix.DeviceIDList{
			h.client.UserID: {h.client.DeviceID},
		},
	})
	if err != nil {
		h.log.Error().Err(err).Msg("Failed to query own keys")
		return false
	}

	device, ok := resp.DeviceKeys[h.client.UserID][h.client.DeviceID]
	if ok && len(device.Keys) > 0 {
		h.log.Debug().Msg("Keys verified on server")
		return true
	}

	return false
}

// cryptoSyncer implements the mautrix.Syncer interface for crypto sync
type cryptoSyncer struct {
	*crypto.OlmMachine
}

func (s *cryptoSyncer) ProcessResponse(ctx context.Context, resp *mautrix.RespSync, since string) error {
	s.Log.Trace().Str("since", since).Msg("Processing crypto sync response")
	s.ProcessSyncResponse(ctx, resp, since)
	return nil
}

func (s *cryptoSyncer) OnFailedSync(_ *mautrix.RespSync, err error) (time.Duration, error) {
	if errors.Is(err, mautrix.MUnknownToken) {
		return 0, err
	}
	s.Log.Error().Err(err).Msg("Crypto sync failed, retrying in 10 seconds")
	return 10 * time.Second, nil
}

func (s *cryptoSyncer) GetFilterJSON(_ id.UserID) *mautrix.Filter {
	everything := []event.Type{{Type: "*"}}
	return &mautrix.Filter{
		Presence:    &mautrix.FilterPart{NotTypes: everything},
		AccountData: &mautrix.FilterPart{NotTypes: everything},
		Room: &mautrix.RoomFilter{
			IncludeLeave: false,
			Ephemeral:    &mautrix.FilterPart{NotTypes: everything},
			AccountData:  &mautrix.FilterPart{NotTypes: everything},
			State:        &mautrix.FilterPart{NotTypes: everything},
			Timeline:     &mautrix.FilterPart{NotTypes: everything},
		},
	}
}
