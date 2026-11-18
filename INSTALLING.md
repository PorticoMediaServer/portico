# Install Portico Media Server

Portico is prerelease software. Back up your Portico data before trying an early build, and read
the notes on the release you install.

Download from the releases page: <https://github.com/PorticoMediaServer/portico/releases>

| Platform | File |
| --- | --- |
| Windows 10 and 11 (x64) | `Portico-Media-Server-Windows-x64-Setup….exe`, or the portable `.zip` |
| macOS on Apple silicon | `Portico-Media-Server-macOS-arm64….pkg`, or the `.dmg` that holds it |
| Debian and Ubuntu (x64, ARM64) | `portico-media-server-linux-<arch>….deb` |
| Fedora and other RPM systems (x64, ARM64) | `portico-media-server-linux-<arch>….rpm` |
| Other Linux (x64, ARM64) | `portico-media-server-linux-<arch>….tar.gz` |
| Docker (amd64, arm64) | `ghcr.io/porticomediaserver/portico-server` |

There is no Windows ARM64 build yet. The packages are not signed yet, so your system may warn
about an unknown publisher: download only from the page above and compare the file with the
release's `SHA256SUMS`.

## First start

Open `http://<this computer>:32500/` in a browser (`http://127.0.0.1:32500/` on the computer
itself). Setup works from any network. It asks for the **setup code**, which the server writes
to `setup-code.txt` in its data folder and to its log the first time it starts; the code stays
the same until setup is finished.

## Where Portico keeps things

| | Data folder | Conversions |
| --- | --- | --- |
| Windows | `%LOCALAPPDATA%\Portico` | `%LOCALAPPDATA%\Portico Transcode` |
| macOS | `~/Library/Application Support/Portico` | `~/Library/Caches/Portico Transcode` |
| Linux package (service) | `/var/lib/portico` | `/var/cache/portico` |
| Linux, started by you | `~/.local/share/portico` | `~/.cache/portico` |
| Docker | `/config` | `/transcode/portico-conversions` |

- **The data folder** holds the database (`main.db`, with `guide.db` and `search.db`, which
  rebuild themselves), the keys that open what it keeps sealed (`secrets.key`,
  `remote-sources.key`), certificates (`Keys/`), uploaded pictures (`artwork/`), downloaded
  subtitles, backups (`backups/`) and the log (`logs/server-<date>.log`, kept 30 days by
  default). Set `PORTICO_DATA_DIR` (or start the server with `-data <folder>`) to keep it
  elsewhere, and use the same folder every time. A folder from Portico's earlier server is
  refused: the log says why.
- **Conversions** are temporary and never backed up. Change the folder in **Settings >
  Streaming > Conversion location**, for example to a faster disk or a RAM disk. If the folder
  cannot be written, conversions go to `transcodes` in the data folder.
- Put the data folder on a local disk: a network share can slow or damage the database.

## Windows

Run the Setup program signed in as the person Portico should run as. It asks for administrator
rights once, installs to `C:\Program Files\Portico Media Server`, and starts Portico.

- Portico runs as that person, as Plex does: it starts at their sign-in, opens no window, and
  reads what they can read (their own folders, mapped drives, network shares they have signed in
  to). It stops when they sign out. If someone else types the administrator password during
  setup, Portico is set up for that administrator instead.
- To stop it, end `portico-server.exe` in Task Manager. To start it, choose **Start Portico
  Media Server** in the Start menu. **Settings > Apps > Startup** turns off the start at sign-in.
- The installer adds a firewall rule for TCP 32500.
- To upgrade, run the newer Setup: it stops Portico, replaces it and starts it again.
- Uninstalling removes the program, the start at sign-in, the Start menu entries and the
  firewall rule, and keeps the data folder.
- An install from before 18 November 2026 ran a Windows service. The installer removes that
  service and leaves its data in `%ProgramData%\Portico`, which this server does not use.

## macOS

Run the `.pkg` (also inside the `.dmg`). It installs `/Applications/Portico Media Server.app`,
which runs the server in your account (a LaunchAgent, `tv.getportico.server.service`) whenever you
are signed in. Opening the app starts the server if needed and opens the console. Give your
account access to your media folders.

```sh
launchctl print gui/$(id -u)/tv.getportico.server.service          # status
launchctl kickstart -k gui/$(id -u)/tv.getportico.server.service    # restart
```

An upgrade replaces the app and keeps the data. To uninstall, run the app's
`Contents/Resources/launchd/uninstall.sh` with `sudo`; it keeps the data.

## Debian, Ubuntu and RPM systems

```sh
sudo apt install ./portico-media-server-linux-x64….deb     # or: sudo dnf install ./….rpm
sudo systemctl status portico-media-server
sudo journalctl -u portico-media-server -n 100
```

The first install starts the service (`portico-media-server`, user `portico`). An upgrade
restarts it if it was running. Removing the package stops it and keeps `/var/lib/portico`. The
program is in `/usr/lib/portico-media-server`.

The service may read anything the `portico` user can read, but it may only **write** to its own
folders. To let it write somewhere else (a recordings folder, a conversion or backup folder on
another disk, or media you allow it to delete), give `portico` the file permissions and add the
folder to the service:

```sh
sudo systemctl edit portico-media-server
```

```ini
[Service]
ReadWritePaths=/srv/recordings /mnt/fast/portico-conversions
```

Then `sudo systemctl restart portico-media-server`. Environment settings such as
`PORTICO_DATA_DIR` can go in `/etc/portico-media-server/environment` (one `NAME=value` per line);
a different data folder also needs its own `ReadWritePaths` line.

## Portable archives (Linux `.tar.gz`, Windows `.zip`)

Unpack the whole archive into a folder you can write to, then run `./start-portico.sh` (Linux) or
`start-portico.cmd` (Windows). Portico runs under your account until you stop it; nothing starts
it at boot and no firewall rule is added. To upgrade, stop it, unpack the new build into a new
folder and start that one: the data folder stays where it was.

## Docker

```sh
docker run -d --name portico --restart unless-stopped --stop-timeout 45 \
  -p 32500:32500 -e TZ=America/Halifax -e PUID=1000 -e PGID=1000 \
  -v /srv/portico/config:/config -v /srv/portico/transcode:/transcode -v /srv/media:/media:ro \
  ghcr.io/porticomediaserver/portico-server:latest
docker logs portico 2>&1 | grep -i 'setup code'
```

- `/config` must be a mapped folder or volume (the container refuses to start without one). Keep
  it on a local disk.
- `/transcode` is for conversions; map it to a fast local disk, or to a RAM disk if you prefer.
- Mount media read-only unless you want Portico to be able to delete it.
- The server runs as `PUID:PGID` (default `1000:1000`); that user needs to read your media.
  `UMASK` (default `0077`) and `TZ` are honoured.
- Hardware conversion: Intel or AMD, pass `--device /dev/dri`; NVIDIA, use the NVIDIA container
  runtime (`--gpus all`). The image has the drivers, and finds the devices' groups itself.
- Compose files: `packaging/compose/`. NAS templates: `packaging/unraid/`, `packaging/truenas/`,
  `packaging/synology/`.
- To upgrade, pull the new image, remove the old container and run the same command. Your data is
  in `/config`.
- For first-run setup, the setup code is in the log (above) and in `/config/setup-code.txt`.
- The container has a health check (`portico-server --health-check`).

## Network

Portico listens on TCP **32500** on every address, for plain HTTP and HTTPS on the same port.
Allow it through the firewall where people should reach it. At home it also uses mDNS (UDP 5353)
so apps find it, and SSDP (UDP 1900) for DLNA, which you can turn off. Set `PORTICO_BIND`, for
example `PORTICO_BIND=192.168.1.10:32500`, to listen on one address or another port. Portico
can't be served under a path such as `https://example.com/portico`; give it a host name of its
own.

Network settings are in **Settings > Remote access**:

- **Home networks**: what counts as home. By default, this computer and the private networks
  it is attached to. Add a VPN's range (Tailscale, for example) if it should count as home.
- **Remote access**: on, off, or private networks only.
- **Use your own address**: your domain, with your own certificate and key (PEM files).
- In the advanced settings: **Port mapping** (asks your router to open the port), and
  **Trusted reverse proxies** and **Custom addresses** for a reverse proxy.

### Reverse proxy

An Nginx example on the same machine, with your own certificate:

```nginx
server {
    listen 443 ssl;
    server_name media.example.com;
    ssl_certificate /etc/nginx/certs/media.crt;
    ssl_certificate_key /etc/nginx/certs/media.key;

    location / {
        proxy_pass http://127.0.0.1:32500;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Host $host;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header Connection "";
        proxy_buffering off;
        proxy_read_timeout 1h;
    }
}
```

Add `127.0.0.1` to **Trusted reverse proxies** and `https://media.example.com` to **Custom
addresses**. Without the first, everyone behind the proxy looks like one person.

## Updates

Portico checks the public release list once a day and tells the owner when there is a newer
version; it never installs anything itself. **Settings > General > Update checks** turns this off.
To update, install the newer package the way you installed this one.

## Backup and restore

Backups are in **Settings > Storage & backups**: by default one every night, the last 7 kept, in
the data folder's `backups` folder (another disk is safer). Each backup is one `.tar.gz` holding
the database (`main.db`), the two key files that open what it keeps sealed (`secrets.key`,
`remote-sources.key`), the pictures people uploaded and downloaded subtitles. Never media,
recordings, the cache, logs or conversions.

To restore from the app, choose the backup and Restore: it asks for the owner's password again, the
server restarts and everyone signs in again. On Windows the server can't restart itself: start it
again from the Start menu.

To restore by hand, as you would swap a Plex database:

1. Stop Portico.
2. In the data folder, put your copy in place of `main.db` (from a backup, take `main.db` out of
   its `.tar.gz`), and delete `main.db-wal` and `main.db-shm` if they are there.
3. Start Portico.

A copy of `main.db` you took while Portico was stopped works the same way; never copy it while
Portico is running. The search index and the TV guide rebuild by themselves. On another computer,
also copy `secrets.key` and `remote-sources.key` from the same backup. Portico refuses a file
that is not a readable database, or one from a newer Portico, and says why without changing it:
put the previous `main.db` back, or install that version. A swapped-in copy brings back the
sign-ins and keys it held, even ones you removed since: check Devices and keys afterwards.
