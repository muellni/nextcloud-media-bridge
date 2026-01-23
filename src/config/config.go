package config

import (
	"io/ioutil"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v2"
)

type Config struct {
	Nextcloud struct {
		BaseURL        string `yaml:"base_url"`
		WebURL         string `yaml:"web_url"`
		DisableWebLink bool   `yaml:"disable_web_link"`
		Username       string `yaml:"username"`
		Password       string `yaml:"password"`
	} `yaml:"nextcloud"`
	Matrix struct {
		HomeserverURL    string            `yaml:"homeserver_url"`
		HomeserverDomain string            `yaml:"homeserver_domain"`
		RoomPathTemplate map[string]string `yaml:"room_path_template"`
		Appservice       struct {
			RegistrationPath string `yaml:"registration_path"`
			Hostname         string `yaml:"hostname"`
			Port             uint16 `yaml:"port"`
		} `yaml:"appservice"`
		Admin struct {
			Enabled     bool   `yaml:"enabled"`      // Enable Synapse admin API for media deletion
			AccessToken string `yaml:"access_token"` // Admin access token for Synapse admin API
		} `yaml:"admin"`
	} `yaml:"matrix"`
	MediaProxy struct {
		ServerName string `yaml:"server_name"`
		ServerKey  string `yaml:"server_key"`
		HMACSecret string `yaml:"hmac_secret"`
		ListenAddr string `yaml:"listen_address"`
		ListenPort uint16 `yaml:"listen_port"`
		UseTLS     bool   `yaml:"use_tls"`
		TLSCert    string `yaml:"tls_cert"`
		TLSKey     string `yaml:"tls_key"`
	} `yaml:"media_proxy"`
}

func LoadConfig(path string) (*Config, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}

	expanded := expandEnvPreserveUnknown(string(data))
	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func LoadConfigFromEnv() *Config {
	port, _ := strconv.ParseUint(os.Getenv("MATRIX_APP_PORT"), 10, 16)
	mediaPort, _ := strconv.ParseUint(os.Getenv("MEDIA_PROXY_LISTEN_PORT"), 10, 16)
	return &Config{
		Nextcloud: struct {
			BaseURL        string `yaml:"base_url"`
			WebURL         string `yaml:"web_url"`
			DisableWebLink bool   `yaml:"disable_web_link"`
			Username       string `yaml:"username"`
			Password       string `yaml:"password"`
		}{
			BaseURL:        os.Getenv("NEXTCLOUD_BASE_URL"),
			WebURL:         os.Getenv("NEXTCLOUD_WEB_URL"),
			DisableWebLink: parseBool(os.Getenv("NEXTCLOUD_DISABLE_WEB_LINK")),
			Username:       os.Getenv("NEXTCLOUD_USERNAME"),
			Password:       os.Getenv("NEXTCLOUD_PASSWORD"),
		},
		Matrix: struct {
			HomeserverURL    string            `yaml:"homeserver_url"`
			HomeserverDomain string            `yaml:"homeserver_domain"`
			RoomPathTemplate map[string]string `yaml:"room_path_template"`
			Appservice       struct {
				RegistrationPath string `yaml:"registration_path"`
				Hostname         string `yaml:"hostname"`
				Port             uint16 `yaml:"port"`
			} `yaml:"appservice"`
			Admin struct {
				Enabled     bool   `yaml:"enabled"`
				AccessToken string `yaml:"access_token"`
			} `yaml:"admin"`
		}{
			HomeserverURL:    os.Getenv("MATRIX_HOMESERVER_URL"),
			HomeserverDomain: os.Getenv("MATRIX_HOMESERVER_DOMAIN"),
			RoomPathTemplate: parseRoomPathTemplate(os.Getenv("MATRIX_ROOM_PATH_TEMPLATE")),
			Appservice: struct {
				RegistrationPath string `yaml:"registration_path"`
				Hostname         string `yaml:"hostname"`
				Port             uint16 `yaml:"port"`
			}{
				RegistrationPath: os.Getenv("MATRIX_APP_REGISTRATION_PATH"),
				Hostname:         os.Getenv("MATRIX_APP_HOST"),
				Port:             uint16(port),
			},
			Admin: struct {
				Enabled     bool   `yaml:"enabled"`
				AccessToken string `yaml:"access_token"`
			}{
				Enabled:     parseBool(os.Getenv("MATRIX_ADMIN_ENABLED")),
				AccessToken: os.Getenv("MATRIX_ADMIN_ACCESS_TOKEN"),
			},
		},
		MediaProxy: struct {
			ServerName string `yaml:"server_name"`
			ServerKey  string `yaml:"server_key"`
			HMACSecret string `yaml:"hmac_secret"`
			ListenAddr string `yaml:"listen_address"`
			ListenPort uint16 `yaml:"listen_port"`
			UseTLS     bool   `yaml:"use_tls"`
			TLSCert    string `yaml:"tls_cert"`
			TLSKey     string `yaml:"tls_key"`
		}{
			ServerName: os.Getenv("MEDIA_PROXY_SERVER_NAME"),
			ServerKey:  os.Getenv("MEDIA_PROXY_SERVER_KEY"),
			HMACSecret: os.Getenv("MEDIA_PROXY_HMAC_SECRET"),
			ListenAddr: envOrDefault("MEDIA_PROXY_LISTEN_ADDRESS", "0.0.0.0"),
			ListenPort: uint16(mediaPort),
			UseTLS:     parseBool(os.Getenv("MEDIA_PROXY_USE_TLS")),
			TLSCert:    os.Getenv("MEDIA_PROXY_TLS_CERT"),
			TLSKey:     os.Getenv("MEDIA_PROXY_TLS_KEY"),
		},
	}
}

func envOrDefault(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func parseRoomPathTemplate(value string) map[string]string {
	if value == "" {
		return map[string]string{}
	}
	result := map[string]string{}
	pairs := strings.Split(value, ",")
	for _, pair := range pairs {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) == 2 {
			result[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
		}
	}
	return result
}

func parseBool(value string) bool {
	if value == "" {
		return false
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false
	}
	return parsed
}

func expandEnvPreserveUnknown(value string) string {
	return os.Expand(value, func(key string) string {
		if envValue, ok := os.LookupEnv(key); ok {
			return envValue
		}
		return "${" + key + "}"
	})
}
