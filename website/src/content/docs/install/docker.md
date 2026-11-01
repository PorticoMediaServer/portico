---
title: Install with Docker
description: Set up a Portico Server container with persistent data and media mounts.
category: install
order: 4
audience: Server owners
updated: 2026-10-18
---

Use `ghcr.io/porticomediaserver/portico-server:latest`, or replace `latest` with a release version tag. The image includes the server, Portico Web, FFmpeg and FFprobe.

## Data, media and port

- Persist `/var/lib/portico`. It holds configuration, the database, artwork, logs and generated playback files. There isn't a separate required configuration volume.
- Mount media separately, for example host `/srv/media` at container `/media`. Use read-only mounts unless you explicitly enable media writes.
- Publish TCP `32500`, the default listener. HTTP and HTTPS use the same port.

The image runs as the unprivileged `portico` user. A named data volume uses the image's directory permissions. Host media permissions must let the container user read files and traverse directories. A bind-mounted data folder must also be writable by that user.

## Start with Docker

Create `/srv/media` on the host, or replace it below with your existing media directory. Set `TZ` to your IANA time zone for maintenance schedules.

```sh
docker volume create portico-state
docker run -d --name portico --restart unless-stopped --stop-timeout 45 \
  -p 32500:32500 \
  -e TZ=America/Halifax \
  -v portico-state:/var/lib/portico \
  -v /srv/media:/media:ro \
  ghcr.io/porticomediaserver/portico-server:latest
```

Open `http://127.0.0.1:32500/` on the Docker host, or use its local IP address from another device. Follow [Complete first-run setup](/docs/install/first-run/). When adding a library, use its container path, such as `/media/Movies`.

## Use Docker Compose

Save this as `compose.yaml`, adjusting the media path and time zone:

```yaml
services:
  portico:
    image: ghcr.io/porticomediaserver/portico-server:latest
    container_name: portico
    restart: unless-stopped
    stop_grace_period: 45s
    ports:
      - "32500:32500"
    environment:
      TZ: America/Halifax
    volumes:
      - portico-state:/var/lib/portico
      - /srv/media:/media:ro

volumes:
  portico-state:
    name: portico-state
```

Run `docker compose up -d`. Use either this Compose setup or the `docker run` example for the named container.

## Environment settings

The image already supplies these defaults:

| Variable | Default | Purpose |
| --- | --- | --- |
| `PORTICO_STATE_DIR` | `/var/lib/portico` | Persistent server data |
| `PORTICO_BIND` | `[::]:32500` | Listener inside the container |
| `PORTICO_WEB_DIR` | `/opt/portico/web` | Bundled Portico Web |
| `PORTICO_FFMPEG` | `/opt/portico/third_party/ffmpeg/bin/ffmpeg` | Bundled conversion tool |
| `PORTICO_FFPROBE` | `/opt/portico/third_party/ffmpeg/bin/ffprobe` | Bundled media analysis tool |

Optional `PORTICO_CACHE_DIR` and `PORTICO_BACKUP_DIR` values must be absolute, writable container paths. Mount persistent storage for backups. A separate cache mount can hold temporary generated playback files. Changing an environment path alone doesn't create a host mount.

`PORTICO_DECODER_SANDBOX` accepts `required` to fail media jobs when the decoder sandbox is unavailable, or `off` to disable it. By default, Portico uses the sandbox where the host permits it and reports fallback restrictions in diagnostics.

## Networking and shutdown

The published port supports direct connections. LAN multicast discovery needs host networking; the examples above use a bridge network, so connect by server address. If you switch to host networking on a supported host, remove the port mapping.

The image includes an init process. Keep its entrypoint and allow at least 45 seconds for shutdown. Its built-in health check reports readiness without credentials.

## Updates

1. Back up Portico data. See [Back up Portico Server](/docs/administration/backups/).
2. Run `docker compose pull`, then `docker compose up -d`. For a container started with `docker run`, pull the image, stop and remove the old container, then repeat the command with the same mounts.
3. Check `docker logs --tail 100 portico` and the container's health.
4. Keep your backup until playback and server settings are checked.

Don't delete `portico-state` when replacing the container. Avoid `docker compose down -v`, which removes its data volume.
