package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/appservice"
	"maunium.net/go/mautrix/event"

	"nextcloud-media-bridge/src/config"
	"nextcloud-media-bridge/src/handlers"
)

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}
	if cfg.Matrix.Appservice.RegistrationPath == "" {
		log.Fatal("Missing Matrix appservice registration path")
	}
	if cfg.MediaProxy.HMACSecret == "" {
		log.Fatal("Missing MEDIA_PROXY_HMAC_SECRET")
	}

	registration, err := appservice.LoadRegistration(cfg.Matrix.Appservice.RegistrationPath)
	if err != nil {
		log.Fatalf("Failed to load appservice registration: %v", err)
	}

	as, err := appservice.CreateFull(appservice.CreateOpts{
		Registration:     registration,
		HomeserverDomain: cfg.Matrix.HomeserverDomain,
		HomeserverURL:    cfg.Matrix.HomeserverURL,
		HostConfig: appservice.HostConfig{
			Hostname: cfg.Matrix.Appservice.Hostname,
			Port:     cfg.Matrix.Appservice.Port,
		},
	})
	if err != nil {
		log.Fatalf("Failed to initialize appservice: %v", err)
	}
	as.Log = zerolog.New(os.Stdout).With().Timestamp().Logger()

	nextcloud := handlers.NewNextcloudClient(cfg.Nextcloud.BaseURL, cfg.Nextcloud.Username, cfg.Nextcloud.Password)

	// Initialize crypto helper if encryption is enabled
	var cryptoHelper *handlers.CryptoHelper
	if cfg.Matrix.Encryption.Enabled {
		as.Log.Info().Msg("Initializing end-to-end encryption support")
		var err error
		cryptoHelper, err = handlers.NewCryptoHelper(cfg, as)
		if err != nil {
			log.Fatalf("Failed to create crypto helper: %v", err)
		}
		if err := cryptoHelper.Init(context.Background()); err != nil {
			log.Fatalf("Failed to initialize crypto: %v", err)
		}
		go cryptoHelper.Start()
		as.Log.Info().Msg("End-to-end encryption initialized successfully")
	} else {
		as.Log.Info().Msg("End-to-end encryption disabled")
	}

	mediaHandler := handlers.NewMediaHandler(cfg, nextcloud, []byte(cfg.MediaProxy.HMACSecret), as, cryptoHelper)

	mediaProxy, err := handlers.NewMediaProxy(cfg, nextcloud, []byte(cfg.MediaProxy.HMACSecret))
	if err != nil {
		log.Fatalf("Failed to initialize media proxy: %v", err)
	}
	as.Log.Info().Str("media_proxy", cfg.MediaProxy.ServerName).Msg("Media proxy initialized")
	mediaProxyMux := http.NewServeMux()
	mediaProxy.RegisterRoutes(mediaProxyMux, as.Log)

	go as.Start()
	as.Log.Info().Str("address", cfg.Matrix.Appservice.Hostname).Uint16("port", cfg.Matrix.Appservice.Port).Msg("Appservice listener starting")

	// Initialize room manager and join configured rooms
	roomManager := handlers.NewRoomManager(cfg, as)
	go func() {
		// Wait a bit for appservice to fully start
		time.Sleep(2 * time.Second)
		ctx := context.Background()
		roomManager.JoinConfiguredRooms(ctx)
		// Start monitoring room membership every 5 minutes
		roomManager.StartRoomMonitor(ctx, 5*time.Minute)
	}()

	mediaPort := cfg.MediaProxy.ListenPort
	if mediaPort == 0 {
		if cfg.MediaProxy.UseTLS {
			mediaPort = 29335
		} else {
			mediaPort = 29336
		}
	}
	mediaListenAddr := cfg.MediaProxy.ListenAddr
	if mediaListenAddr == "" {
		mediaListenAddr = "0.0.0.0"
	}
	mediaAddr := fmt.Sprintf("%s:%d", mediaListenAddr, mediaPort)
	mediaServer := &http.Server{
		Addr:    mediaAddr,
		Handler: mediaProxyMux,
	}
	go func() {
		if cfg.MediaProxy.UseTLS {
			as.Log.Info().Str("address", mediaAddr).Msg("Media proxy TLS listener starting")
			if cfg.MediaProxy.TLSCert == "" || cfg.MediaProxy.TLSKey == "" {
				cert, err := generateSelfSignedCert(mediaTLSHost(cfg.MediaProxy.ServerName, mediaAddr))
				if err != nil {
					log.Fatalf("Failed to generate self-signed media proxy cert: %v", err)
				}
				listener, err := tls.Listen("tcp", mediaAddr, &tls.Config{Certificates: []tls.Certificate{cert}})
				if err != nil {
					log.Fatalf("Media proxy TLS listen failed: %v", err)
				}
				if err := mediaServer.Serve(listener); err != nil {
					log.Fatalf("Media proxy TLS server failed: %v", err)
				}
				return
			}
			if err := mediaServer.ListenAndServeTLS(cfg.MediaProxy.TLSCert, cfg.MediaProxy.TLSKey); err != nil {
				log.Fatalf("Media proxy TLS server failed: %v", err)
			}
		} else {
			as.Log.Info().Str("address", mediaAddr).Msg("Media proxy HTTP listener starting")
			if err := mediaServer.ListenAndServe(); err != nil {
				log.Fatalf("Media proxy HTTP server failed: %v", err)
			}
		}
	}()
	// Event processing with concurrency control
	// Use a semaphore to limit concurrent event processing to prevent overwhelming Nextcloud
	maxConcurrentEvents := 10 // Configurable limit
	semaphore := make(chan struct{}, maxConcurrentEvents)

	go func() {
		ctx := context.Background()
		for evt := range as.Events {
			evt := evt // Capture loop variable for goroutine
			as.Log.Debug().Str("event_id", evt.ID.String()).Str("room_id", evt.RoomID.String()).Str("event_type", evt.Type.Type).Msg("Received event from appservice")

			// Acquire semaphore slot (blocks if at max concurrency)
			semaphore <- struct{}{}

			// Process event in separate goroutine for concurrent handling
			go func() {
				defer func() { <-semaphore }() // Release semaphore slot when done

				// Decrypt encrypted events if crypto is enabled
				if evt.Type == event.EventEncrypted && cryptoHelper != nil {
					decrypted, err := cryptoHelper.Decrypt(ctx, evt)
					if err != nil {
						as.Log.Error().Err(err).Str("event_id", evt.ID.String()).Msg("Failed to decrypt event")
						return
					}
					evt = decrypted
				}

				if err := handlers.HandleAutoJoin(ctx, as, evt); err != nil {
					as.Log.Error().Err(err).Msg("Failed to auto-join room")
				}
				if err := mediaHandler.HandleMatrixEvent(ctx, as, evt); err != nil {
					as.Log.Error().Err(err).Msg("Failed to process media event")
				}
			}()
		}
	}()

	fmt.Println("Nextcloud Media Bridge is running")
	select {}
}

func loadConfig() (*config.Config, error) {
	configPath := os.Getenv("CONFIG_PATH")
	if configPath != "" {
		return config.LoadConfig(configPath)
	}
	if _, err := os.Stat("config/config.yaml"); err == nil {
		return config.LoadConfig("config/config.yaml")
	}
	return config.LoadConfigFromEnv(), nil
}

func mediaTLSHost(serverName, listenAddr string) string {
	if serverName != "" {
		if host, _, err := net.SplitHostPort(serverName); err == nil {
			return host
		}
		if strings.Contains(serverName, ":") {
			return strings.Split(serverName, ":")[0]
		}
		return serverName
	}
	if host, _, err := net.SplitHostPort(listenAddr); err == nil {
		return host
	}
	return listenAddr
}

func generateSelfSignedCert(host string) (tls.Certificate, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, err
	}

	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		return tls.Certificate{}, err
	}

	template := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: host,
		},
		NotBefore: time.Now().Add(-time.Hour),
		NotAfter:  time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:  x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageServerAuth,
		},
	}

	if ip := net.ParseIP(host); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else if host != "" {
		template.DNSNames = []string{host}
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return tls.Certificate{}, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})

	return tls.X509KeyPair(certPEM, keyPEM)
}
