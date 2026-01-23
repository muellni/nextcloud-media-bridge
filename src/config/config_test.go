package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigPreservesTemplateVars(t *testing.T) {
	t.Setenv("NEXTCLOUD_PASSWORD", "secret")
	configYAML := `nextcloud:
  base_url: "https://nextcloud.example.com/remote.php/dav/files/media-bridge"
  username: "media-bridge"
  password: "${NEXTCLOUD_PASSWORD}"

matrix:
  homeserver_url: "https://matrix.example.com"
  homeserver_domain: "example.com"
  room_path_template:
    "!roomid:example.com": "/vup/matrixupload/${year}/mediatest/${user}/${file}"
  appservice:
    registration_path: "/data/registration.yaml"
    hostname: "0.0.0.0"
    port: 29334

media_proxy:
  server_name: "nextcloud-media-bridge:29335"
  listen_address: "0.0.0.0"
  listen_port: 29335
  use_tls: true
  tls_cert: ""
  tls_key: ""
  server_key: "ed25519 a1b2c3d4 abcdef"
  hmac_secret: "secret"
`

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(configYAML), 0600); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if cfg.Nextcloud.Password != "secret" {
		t.Fatalf("expected password from env, got %q", cfg.Nextcloud.Password)
	}
	if cfg.Matrix.RoomPathTemplate["!roomid:example.com"] != "/vup/matrixupload/${year}/mediatest/${user}/${file}" {
		t.Fatalf("expected template variables preserved, got %q", cfg.Matrix.RoomPathTemplate["!roomid:example.com"])
	}
}
