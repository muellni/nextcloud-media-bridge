package handlers

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/mediaproxy"

	"nextcloud-media-bridge/src/config"
	"nextcloud-media-bridge/src/utils"
)

type MediaProxy struct {
	proxy     *mediaproxy.MediaProxy
	secretKey []byte
	nextcloud *NextcloudClient
}

func NewMediaProxy(cfg *config.Config, nextcloud *NextcloudClient, secretKey []byte) (*MediaProxy, error) {
	mp, err := mediaproxy.NewFromConfig(mediaproxy.BasicConfig{
		ServerName:        cfg.MediaProxy.ServerName,
		ServerKey:         cfg.MediaProxy.ServerKey,
		FederationAuth:    false,
		WellKnownResponse: "",
	}, func(ctx context.Context, mediaID string, params map[string]string) (mediaproxy.GetMediaResponse, error) {
		ref, err := utils.DecodeMediaID(secretKey, mediaID)
		if err != nil {
			return nil, mediaproxy.ErrInvalidMediaIDSyntax
		}
		log.Printf("Media proxy download: %s", ref.Path)
		resp, err := nextcloud.DownloadFile(ref.Path)
		if err != nil {
			return nil, err
		}
		contentType := ref.MimeType
		if contentType == "" {
			contentType = resp.Header.Get("Content-Type")
		}
		return &mediaproxy.GetMediaResponseData{
			Reader:        resp.Body,
			ContentType:   contentType,
			ContentLength: resp.ContentLength,
		}, nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize media proxy: %w", err)
	}
	return &MediaProxy{proxy: mp, secretKey: secretKey, nextcloud: nextcloud}, nil
}

func (mp *MediaProxy) RegisterRoutes(router *http.ServeMux, log zerolog.Logger) {
	mp.proxy.RegisterRoutes(router, log)
}
