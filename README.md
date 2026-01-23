# Nextcloud Media Bridge

A Matrix appservice that automatically uploads media files from Matrix rooms to Nextcloud and serves them back through a custom media proxy. Files are organized using customizable path templates.

## Features

- **Automatic Media Upload**: Monitors Matrix rooms and uploads media to Nextcloud via WebDAV
- **Custom Path Templates**: Organize files with templates like `${year}/${room}/${user}/${file}`
- **Media Proxy**: Serves files from Nextcloud using `mxc://` URIs with optional TLS or HTTP mode
- **Room-Based Configuration**: Different storage paths for each room
- **Auto-Join Configured Rooms**: Automatically joins public rooms at startup
- **Room Monitoring**: Warns if bridge is not in configured rooms
- **Message Replacement**: Edits original message to replace media URL
- **Nextcloud Web Links**: Optionally includes direct links to files in Nextcloud (requires login)

## Quick Start

### Using Docker

```bash
docker run -v /path/to/config:/app/config ghcr.io/youruser/nextcloud-media-bridge:latest
```

### Using Docker Compose

```yaml
version: '3'
services:
  media-bridge:
    image: ghcr.io/muellni/nextcloud-media-bridge:latest
    volumes:
      - ./config:/app/config
    environment:
      - CONFIG_PATH=/app/config/config.yaml
    ports:
      - "29334:29334"  # Appservice
      - "29335:29335"  # Media proxy (TLS)
```

### Building from Source

```bash
git clone https://github.com/muellni/nextcloud-media-bridge
cd nextcloud-media-bridge
go mod download
CGO_ENABLED=0 go build -o nextcloud-media-bridge ./src
./nextcloud-media-bridge
```

## Configuration

Create `config/config.yaml` from the example:

```bash
cp config/config.example.yaml config/config.yaml
```

### Minimal Configuration

```yaml
nextcloud:
  base_url: "https://nextcloud.example.com/remote.php/dav/files/bridge-user"
  web_url: "https://nextcloud.example.com"  # Optional: Enables Nextcloud file links
  disable_web_link: false  # Optional: Disable adding "View in Nextcloud" link
  username: "bridge-user"
  password: "your-app-password"

matrix:
  homeserver_url: "https://matrix.example.com"
  homeserver_domain: "example.com"
  room_path_template:
    "!roomid:example.com": "/media/${year}/${room}/${user}/${file}"
  appservice:
    registration_path: "/app/config/registration.yaml"
  # Optional: delete original media from Synapse storage after upload
  # (does NOT affect files stored in Nextcloud)
  admin:
    enabled: false
    access_token: "${MATRIX_ADMIN_ACCESS_TOKEN}"

media_proxy:
  server_name: "media.example.com"
  server_key: "ed25519 a1b2c3d4 YOUR_SIGNING_KEY_HERE"
  hmac_secret: "your-random-secret-here"
  use_tls: true
```

### Path Template Variables

**For `room_path_template`:**
- `${year}` - Current year (2026)
- `${month}` - Month with zero-padding (01-12)
- `${day}` - Day with zero-padding (01-31)
- `${room}` - Sanitized room name
- `${user}` - Matrix username (local part)
- `${file}` - Sanitized filename

### Example Templates

```yaml
room_path_template:
  "!roomid1:example.com": "/work/${year}/${month}/${user}/${file}"
  "!roomid2:example.com": "/photos/${year}/${user}/${file}"
  "!roomid3:example.com": "/archive/${year}-${month}-${day}/${file}"

```

## Deployment Modes

### Direct TLS (Default)

```yaml
media_proxy:
  server_name: "nextcloud-media-bridge:29335" # This hostname becomes part of the mxc:// media link and will be internally resolved by synapse
  listen_port: 29335
  use_tls: true
  tls_cert: ""  # Auto-generates self-signed cert
  tls_key: ""
```

### Behind Reverse Proxy

```yaml
media_proxy:
  server_name: "media.example.com"  # Public domain
  listen_port: 29336
  use_tls: false  # Proxy handles TLS
```

## Matrix Appservice Setup

### 1. Create Registration File

Create `nextcloud-media-bridge-registration.yaml` with unique tokens:

```yaml
id: nextcloud-media-bridge
url: http://nextcloud-media-bridge:29334 # the address of the media bridge server will be reachable by synapse
as_token: "generate_random_token_1"  # Generate with: openssl rand -hex 32
hs_token: "generate_random_token_2"  # Generate with: openssl rand -hex 32
sender_localpart: mediabridge # The username of the media bridge bot in your channels
rate_limited: false
namespaces:
  users:
    - exclusive: false
      regex: "@.*:example.com"
  rooms: []
  aliases: []
```

Generate tokens:
```bash
openssl rand -hex 32  # AS token
openssl rand -hex 32  # HS token
```

### 2. Configure Synapse

Add to your Synapse `homeserver.yaml`:

```yaml
app_service_config_files:
  - /data/appservices/nextcloud-media-bridge-registration.yaml
```

### 3. Docker Compose Setup

Complete example with Synapse and the media bridge:

```yaml

services:
    # database, redis, traefik, ...
  synapse:
    # ...
    volumes:
    # ...
      - ./nextcloud-media-bridge-registration.yaml:/data/appservices/nextcloud-media-bridge.yaml:ro
    networks:
    # ...
      - default

  nextcloud-media-bridge:
    image: ghcr.io/muellni/nextcloud-media-bridge:latest
    volumes:
      - ./nextcloud-media-bridge-config.yaml:/app/config/config.yaml:ro
      - ./nextcloud-media-bridge-registration.yaml:/app/config/registration.yaml:ro
    environment:
      CONFIG_PATH: /app/config/config.yaml
    depends_on:
      - synapse
```

### 4. Bridge Configuration

Create `nextcloud-media-bridge-config.yaml`:

```yaml
nextcloud:
  base_url: "https://nextcloud.example.com/remote.php/dav/files/bridge-user"
  web_url: "https://nextcloud.example.com"
  disable_web_link: false
  username: "bridge-user"
  password: "${NEXTCLOUD_PASSWORD}"

matrix:
  homeserver_url: "http://synapse:8008"
  homeserver_domain: "example.com"
  room_path_template:
    "!roomid:example.com": "/media/${year}/${room}/${user}/${file}"
  appservice:
    registration_path: "/data/appservices/nextcloud-media-bridge.yaml"
    hostname: "0.0.0.0"
    port: 29334
  # Optional: delete original media from Synapse storage after upload
  # (does NOT affect files stored in Nextcloud)
  admin:
    enabled: false
    access_token: "${MATRIX_ADMIN_ACCESS_TOKEN}"

media_proxy:
  server_name: "nextcloud-media-bridge:29335"
  listen_address: "0.0.0.0"
  listen_port: 29335
  use_tls: true
  server_key: "${MEDIA_PROXY_SERVER_KEY}"
  hmac_secret: "${MEDIA_PROXY_HMAC_SECRET}"
```

### 5. Directory Structure

```
.
├── docker-compose.yml
├── .env
├── homeserver.yaml
├── nextcloud-media-bridge-registration.yaml
├── nextcloud-media-bridge-config.yaml
```

### 6. Environment Variables

Create `.env` file:

```bash
NEXTCLOUD_PASSWORD=nextcloud_app_password
NEXTCLOUD_DISABLE_WEB_LINK=false
MEDIA_PROXY_SERVER_KEY=generate_with_openssl_rand_hex_32
MEDIA_PROXY_HMAC_SECRET=generate_with_openssl_rand_hex_32
```

## Room Management

### Automatic Room Joining

The bridge automatically attempts to join all rooms configured in `room_path_template` at startup:

- **Public Rooms**: Automatically joined without invitation
- **Private/Invite-Only Rooms**: Must be manually invited (bot will log a warning)
- **Join Verification**: Every 5 minutes, checks if still in configured rooms

### Inviting the Bot

For private rooms, invite the bot user to your room:

```
/invite @mediabridge:example.com
```

Replace `mediabridge` with your `sender_localpart` from the registration file.

### Monitoring

The bridge logs warnings if it's not in a configured room:

```
Warning: Bridge is NOT in configured room !private:example.com!
Media uploads will be skipped. Invite bot user @mediabridge:example.com to this room.
```

Check logs to verify room membership:
```bash
docker-compose logs nextcloud-media-bridge | grep "joined room"
```

## Environment Variables

All config options can be set via environment variables:

```bash
# Nextcloud
NEXTCLOUD_BASE_URL="https://nextcloud.example.com/remote.php/dav/files/user"
NEXTCLOUD_WEB_URL="https://nextcloud.example.com"  # Optional: For direct file links
NEXTCLOUD_USERNAME="bridge-user"
NEXTCLOUD_PASSWORD="app-password"

# Matrix
MATRIX_HOMESERVER_URL="https://matrix.example.com"
MATRIX_HOMESERVER_DOMAIN="example.com"
MATRIX_ROOM_PATH_TEMPLATE="!room1:example.com=/path/${year}/${user}/${file}"
MATRIX_APP_REGISTRATION_PATH="/app/registration.yaml"

# Media Proxy
MEDIA_PROXY_SERVER_NAME="media.example.com"
MEDIA_PROXY_USE_TLS="false"
MEDIA_PROXY_LISTEN_PORT="29336"
MEDIA_PROXY_SERVER_KEY="ed25519 a1b2c3d4 ..."
MEDIA_PROXY_HMAC_SECRET="your-secret"
```

## Nextcloud Web Links

When `web_url` is configured, the bridge automatically includes a direct link to the file in Nextcloud with each media message. This allows users to:

- View files in their original location within Nextcloud
- Access file management features (move, rename, share, etc.)
- See file metadata and version history
- Download files directly from Nextcloud

**Important Notes:**
- Users must be logged into Nextcloud to view files (these are NOT public share links)
- The link uses the format: `https://nextcloud.example.com/apps/files/?dir=/path&scrollto=filename`
- Users need appropriate permissions to access the shared folder where files are stored
- Leave `web_url` empty in the configuration to disable this feature

**Example message with Nextcloud link:**
```
[Image preview]

View in Nextcloud: https://nextcloud.example.com/apps/files/?dir=/bridge-media/2026/room-name/username&scrollto=photo.jpg
```

## How It Works

1. **Bridge Startup**:
   - Registers as an appservice with Synapse
   - Automatically attempts to join all configured rooms
   - Logs warnings for rooms it cannot join (private/invite-only)

2. **Room Monitoring**:
   - Every 5 minutes, verifies bridge is still in configured rooms
   - Logs warnings if kicked or left from a configured room

3. **Media Upload Flow** - When a user posts media (image, video, file):
   - Downloads the file from Matrix homeserver
   - Uploads it to Nextcloud using the room's path template
   - Generates a new `mxc://` URL pointing to the bridge's media proxy
   - Attempts to edit the original message to replace the media URL (preserves original sender)
   - Falls back to posting a new message from the bot if editing fails

4. **Media Download Flow** - When someone accesses the `mxc://` URL:
   - Matrix homeserver contacts the media proxy
   - Bridge downloads from Nextcloud
   - Streams content back to the requesting client

## Security Notes

- Use Nextcloud app passwords, not your main password
- Keep `hmac_secret` private - it signs media IDs
- Generate a proper ed25519 signing key for `server_key`
- Run as non-root user (automatic in Docker image)
- Use TLS in production (either direct or via reverse proxy)

## Development

```bash
# Run tests
go test ./... -v

# Build binary
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o nextcloud-media-bridge ./src

# Build Docker image
docker build -t nextcloud-media-bridge .
```

## Troubleshooting

**Bridge not in room / Media not uploading:**
- Check if bridge joined the room: `docker-compose logs nextcloud-media-bridge | grep "joined room"`
- For private rooms, manually invite: `/invite @mediabridge:example.com`
- Verify room is in `room_path_template` config
- Check logs for "Warning: Bridge is NOT in configured room" messages
- Check Nextcloud credentials and WebDAV URL
- Check logs for permission errors

**Bridge cannot join room:**
- If "M_FORBIDDEN" or "M_NOT_FOUND" error: Room is private or doesn't exist
- Invite the bot user manually: `/invite @mediabridge:example.com`
- Verify room ID is correct in config
- Check appservice registration is loaded by Synapse

**Media proxy 404s:**
- Ensure `server_name` matches your mxc:// domain
- Verify `server_key` is correctly formatted
- Check file exists in Nextcloud at the expected path
- Check media proxy logs for download errors

**Synapse federation errors:**
- For reverse proxy setup, verify DNS resolves to proxy
- Check proxy forwards to correct bridge port (29336 for HTTP, 29335 for TLS)
- Ensure TLS certificates are valid
- Test media proxy endpoint: `curl https://media.example.com/_matrix/media/v3/config`

## License

MIT License - see [LICENSE](LICENSE) file

## Contributing

This project is not actively accepting contributions at this time. However, you're welcome to fork the repository for your own use.

