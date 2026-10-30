# Install Portico Media Server

Portico is prerelease software. Back up your Portico data and media metadata
before trying an early build, and read the notes on the release you install.

Choose a build on the releases page; the latest-release address applies when a
build is marked latest:

<https://github.com/PorticoMediaServer/portico-server/releases/latest>

The release workflow defines unsigned packages for Windows x64/ARM64,
Apple Silicon macOS, Linux x64/ARM64, and a multi-architecture Docker image.

- Windows: use the `Setup.exe` installer or portable ZIP.
- macOS: run the `.pkg` installer, directly or from the DMG. It installs a system service and an app that opens the console.
- Debian/Ubuntu: install the matching `.deb` package.
- Fedora/RHEL-family systems: install the matching `.rpm` package.
- Other Linux systems: unpack the matching `.tar.gz` archive.
- Docker: pull `ghcr.io/porticomediaserver/portico-server:latest`.

Because packages are not yet signed, operating systems may show an unknown
publisher warning. Download only from the official GitHub organization and
compare the file with `checksums.sha256` on the release page. Do not enable
unattended installation of these preview builds.

Portico stores server data separately from the application. Linux packages use
`/var/lib/portico` as a system service, or `~/.local/share/portico` for a
user launch. Windows uses `%LOCALAPPDATA%\Portico`, or `%ProgramData%\Portico`
for its service. macOS uses `~/Library/Application Support/Portico`. Containers
use their mounted `/config`. `PORTICO_DATA_DIR` overrides the data path; the
former state-directory variable is not supported.

## First start and service commands

Open `http://127.0.0.1:32500/` on the host and finish setup. From another device,
use a reachable hostname or IP; IPv6 URLs need brackets, for example
`http://[fd12:3456:789a::10]:32500/`. Allow inbound TCP 32500 through the host
firewall where you want access. Portico does not support installation under a
URL path such as `https://example.com/portico`; use a hostname of its own.

### Browser setup through a custom hostname
First-run browser setup accepts IP addresses, `localhost`, and the server machine's
hostname (including its `.local` name). For a custom DNS name or reverse proxy,
set `PORTICO_SETUP_HOSTS` to the exact browser authorities before starting the
server, for example `media.example.com,media.example.com:8443`. Entries include
the port when the URL includes one. This independent allowlist prevents an
unrelated website from rebinding its DNS name to an unconfigured server.
It does not change API CORS or proxy trust. Native app setup remains available.

### Debian/Ubuntu and RPM systems

For x64 packages (use the arm64 filenames on ARM64):

```sh
sudo apt install ./portico-media-server-linux-x64.deb
# Or, on an RPM system:
sudo dnf install ./portico-media-server-linux-x64.rpm
sudo systemctl status portico-media-server
sudo systemctl restart portico-media-server
sudo systemctl stop portico-media-server
sudo systemctl start portico-media-server
journalctl -u portico-media-server -n 100
```

First installation enables and starts the service when systemd is running,
subject to the distribution's service-start policy. An upgrade restarts an
active service and leaves a stopped service stopped. Reinstall the newer package
with the same command. Confirm its status after installation.

### Windows

Run the Setup.exe as administrator. In an elevated PowerShell window:

```powershell
Get-Service PorticoMediaServer
Restart-Service PorticoMediaServer
Stop-Service PorticoMediaServer
Start-Service PorticoMediaServer
```

### macOS

Run the PKG, including the one inside the DMG. Then open the installed app, or
open the console address in your browser. Inspect the service:

```sh
launchctl print gui/$(id -u)/tv.getportico.server.service
```

For a restart, or a clean stop before copying state, use `launchctl bootout`
and bootstrap it again afterwards:

```sh
launchctl bootout gui/$(id -u)/tv.getportico.server.service
launchctl bootstrap gui/$(id -u) "$HOME/Library/LaunchAgents/tv.getportico.server.service.plist"
```

### Portable archives

Unpack the entire archive, including `web` and `third_party`. Run
`./start-portico.sh` on Linux or `start-portico.cmd` on Windows. These run under
your account and install no service or firewall rule. Data defaults to the platform user data folder. Set an absolute `PORTICO_DATA_DIR` to keep it elsewhere.
For an upgrade, stop the process, unpack the new build into a separate folder,
and point its launcher at the existing state folder. Keep the previous build
and a backup until the new build has started successfully.

## Linux package paths and permissions

The packaged systemd service runs as `portico` and keeps the filesystem
read-only except for its state folder. When enabling media deletion or using a
separate recordings folder, grant access only to the directories you selected.
Create those directories first and give `portico` the required filesystem
permissions. Then run `sudo systemctl edit portico-media-server` and add, for
example:

```ini
[Service]
Environment=PORTICO_RECORDINGS_DIR=/srv/recordings
ReadWritePaths=/srv/media/Movies /srv/recordings
```

Replace the example paths with your actual library and recording directories;
quote paths containing spaces. The added paths also work beneath `/home`,
despite the default `ProtectHome=read-only`. This grants write access within the
service sandbox; it does not grant Unix ownership or ACL permissions. Keep
`ProtectSystem=strict`, `ProtectHome=read-only`, `NoNewPrivileges=true` and
`PrivateTmp=true`. Apply the drop-in with
`sudo systemctl daemon-reload && sudo systemctl restart portico-media-server`.

Optional environment settings can instead be stored in the root-owned
`/etc/portico-media-server/environment` file (one `NAME=value` per line).
A custom `PORTICO_DATA_DIR` also needs its own additive `ReadWritePaths` entry.
Selecting a path in the console or setting an environment variable alone does
not grant it sandbox write access.

Custom TLS certificate files only need read access. Copy the certificate and
private key into a private directory inside the state folder, or grant
`portico` read access to the selected files and traversal of their parent
directories using host ACLs. Keep the private key inaccessible to other users;
do not make the whole Let’s Encrypt directory writable. Configure the actual
certificate/key paths in the console. Certificate renewal must preserve that
read access.

## Windows service

The installer registers **Portico Media Server** (`PorticoMediaServer`) with
Windows Services, starting automatically at boot as LocalService. Its worker
restarts after a restore or the owner Restart action without stopping the
service. Stopping the service drains active work before installation files can
be replaced. Run a newer installer into the same directory to upgrade. The
uninstaller removes the service and firewall rule, retaining your data folder.
The state folder is restricted to administrators, SYSTEM and the service SID.

Grant `NT SERVICE\PorticoMediaServer` read access to selected local libraries
and write access where you enable deletion or recordings. Junctions, symbolic
links and mounted-volume library roots are resolved and opened as directory
handles; links within a library still follow the storage safety rules. Mapped
user drive letters are not service paths. For an authenticated NAS, use a UNC
path and configure an account with access in Services > Log On. An upgrade
retains that account. The installer adds a TCP 32500 firewall rule; adjust it
if you choose another listening port.

## macOS service

The package installs `/Applications/Portico Media Server.app` and registers
`tv.getportico.server.service` in the signed-in owner's `~/Library/LaunchAgents`.
It starts in that user's login session and uses that user's data and cache
folders. Opening the app registers the agent if installation happened before a
user signed in, then opens the console. Grant your account access to your media.
An upgrade stops the previous job and starts the replacement, retaining data.
The retired system service's data and account are left intact; no migration is
performed. `PORTICO_CONSOLE_URL` can select the console address from a shell.
To uninstall, run with `sudo` the app's `Contents/Resources/launchd/uninstall.sh`.
It removes the app and the current user's agent while retaining data.

## Data, transcodes and backups

The data folder contains `portico.toml`, `Database/`, `Metadata/`, `Cache/`,
`Logs/`, `Keys/`, `Backups/` and `Temp/`. `Temp/` is emptied at start; `Cache/`
contains rebuildable files. The owner storage API reports each folder's size.
Empty cache schedules a graceful restart and removes only the owned Cache
before media services reopen it. It refuses unowned scopes, linked roots and mounted children. Temp and cache cleanup require the single-instance lock. On a platform without complete mount enumeration, cleanup is postponed while the server continues to start.
Previous preview layouts are refused read-only: choose a fresh data folder.
There is no automatic copy migration.

Transcodes use the data folder's separate `Transcode` directory by default
on Windows, macOS and Linux. The disk folder can be changed independently of
the memory preference. Containers use a mounted `/transcode`, falling back to
`/config/Transcode` when it is not mounted. Transcodes never enter a backup.
The disk transcode directory is created at startup and remains available for
fallback and spill. The owner's transcode location setting defaults to **Memory
when available**; choose **Disk** to keep all output there. Memory output requires
a kernel-bounded Linux tmpfs no larger than the effective **Memory cap**
(`streaming.memoryCap`, default quarter of available memory). Portico reserves
the entire remaining tmpfs capacity above the host and container memory floor.
When memory is unsuitable or under pressure, new output quietly uses disk and
active memory output can spill to disk. Playback does not depend on RAM being
available. See the Docker instructions below for a bounded mount.

Recordings require an explicitly chosen folder outside the data folder.
Set `PORTICO_RECORDINGS_DIR` to that absolute path for headless use. If no
folder is selected, the server starts with recording storage unavailable.
`PORTICO_BACKUP_DIR` can choose another backup destination. Backups include
only `portico.toml`, the consistent database, Metadata and Keys. Keep backups
on another disk to protect against a disk failure.

## Docker

Keep `/config` in a persistent volume, and mount media separately.
A minimal example (choose the image tag from the release you install):

```sh
docker volume create portico-state
docker run -d --name portico --restart unless-stopped --stop-timeout 45 \
  -p 32500:32500 -e TZ=America/Halifax -e PUID=1000 -e PGID=1000 -e UMASK=0077 \
  -v portico-state:/config -v /srv/portico-transcode:/transcode -v /srv/media:/media:ro \
  --tmpfs /memory-transcode:rw,noexec,nosuid,nodev,size=512m,nr_inodes=8192,mode=1777 \
  ghcr.io/porticomediaserver/portico-server:latest
docker logs --tail 100 portico
docker restart --time 45 portico
```

### Memory transcode mounts

Keep `/transcode` mapped to a writable local **disk** directory; never replace
that mount with tmpfs. If it is absent or RAM-backed, Portico uses the disk-backed
`/config/Transcode` fallback. `/config` must also be on disk. Inside a container, the disk folder may be `/transcode` or a child of it,
or `/config/Transcode` or a child of it; other paths are refused when saved.
Use a real directory: linked parents cannot establish disk placement. Mount
memory storage separately at `/memory-transcode`. Startup discovers that tmpfs first, otherwise
Docker's `/dev/shm`, and uses a private, marker-protected installation child.
Portico never mounts or resizes a filesystem itself and needs no privileged
mode or `SYS_ADMIN` capability.

The official Compose examples in `packaging/compose/`, `packaging/truenas/` and
`packaging/synology/` include the same 512 MiB mount as the command above, with an
8192-inode bound. The sticky `1777` mode lets numeric container users create their
private directory; `noexec,nosuid,nodev` restrict the shared mount. The kernel
limits its data size even between server measurements. These are Linux Docker
runtime mounts, not image volumes. See Docker's [tmpfs mount options](https://docs.docker.com/engine/storage/tmpfs/).

Choose a mount size **at or below the effective Memory cap**, leaving the host
and cgroup memory floor intact even if the whole remaining mount fills. The
512 MiB example fits the default quarter cap when at least 2 GiB is truly
available and the floor can be held; installed RAM or the container's configured
limit alone does not establish available memory. A container memory limit also
covers the server and other container work, so leave headroom for those too.
On smaller or busy hosts, use a smaller mount such as `size=64m`, or let Portico
fall back to disk. Changing the mount requires recreating the container.

Quarter and half caps track available memory; they cannot automatically resize
the kernel mount. A fixed mount that exceeds the effective cap, cannot reserve
its remaining capacity above the floor, or runs short of space causes safe disk
fallback or spill. Raising the cap does not enlarge the mount. Keep the disk
mapping available regardless of your cap choice.

Without an explicit memory mount, plain Docker normally supplies a bounded
64 MiB `/dev/shm`, which Portico can use with its 16 MiB per-session reserve when
the cap and floor permit. A larger custom `--shm-size` can exceed the cap and
cause disk fallback. The Dockerfile cannot declare a runtime tmpfs, and a
`VOLUME` instruction would not provide one. Unraid templates use the same
bounded `/dev/shm` fallback; to add the larger dedicated mount, put the
`--tmpfs /memory-transcode:rw,noexec,nosuid,nodev,size=512m,nr_inodes=8192,mode=1777`
option in the template's Extra Parameters, retaining any NVIDIA options and the
disk Transcode mapping.

### Container setup and permissions

For first-run setup at a host name such as `nas.local`, add
`-e PORTICO_SETUP_HOSTS=nas.local:32500` to `docker run`, or open the host's
IP address instead. The container's machine name is not the host's name. Router
DNS suffixes and Tailscale MagicDNS names also need their exact authority in
this list when used for browser setup.

The entrypoint drops to numeric `PUID:PGID` (default `1000:1000`); host permissions must allow it to read
media. The read-only media mount above prevents deletion. Use a writable mount
only where you intend to allow media writes. To upgrade, pull the chosen image,
stop and remove the old container, then run the same command with the new tag
and the same state volume. Removing the container does not remove a named
volume; do not delete that volume when upgrading.
`/config` must be mounted. If `/transcode` is absent or RAM-backed, Storage reports a disk-mapping notice and uses a disk-backed `/config/Transcode`. `TZ` and `UMASK` are honoured without writing `/etc`. The Unraid Community Applications templates are `packaging/unraid/portico.xml` (`99:100`) for Intel or AMD, which passes `/dev/dri`, and `packaging/unraid/portico-nvidia.xml` for NVIDIA, which starts with the driver runtime; both name Unraid so the one GPU remedy the server shows is Unraid's rather than Docker's. The TrueNAS SCALE custom app is `packaging/truenas/compose.yaml` (`568:568`). Use a local cache pool for the database. NFS, SMB and Unraid FUSE pool mounts raise a quiet storage notice. The image includes an
init for orphan reaping and the server's own restart parent; restore and Restart
work without a Docker restart policy. A restart policy remains useful for host
reboots or crashes. Allow at least 45 seconds for container stop.
For VAAPI or Quick Sync, pass `/dev/dri` and add the numeric render/video group
IDs from the host with `--group-add`; the image includes the vendor drivers.
Use host networking when you want LAN multicast discovery. Set `TZ` to your
IANA time zone for maintenance schedules. The built-in health check needs no
credentials and reports ready only after startup finishes.

Update checks use the published release feed by default. Clear the release
feed address in Updates settings to opt out. Updates are installed manually.

## Network access

By default the server listens on port 32500 for IPv4 and IPv6 where the host
supports dual-stack sockets. `PORTICO_BIND=:32500` selects this default explicitly;
use an IP and port to restrict the listener. HTTP and HTTPS share the port.
Plain HTTP works from any address, including overlay networks and carrier-grade
NAT. HTTPS is recommended when traffic crosses an untrusted network.

The owner's **Home networks** (`lanNetworks`) list is the only definition of
local access. Defaults include loopback, private IPv4, link-local, unique-local
IPv6 and directly attached subnets. Replace the list to add or remove ranges;
`[]` makes every request remote, including loopback. This controls recovery
access, remote sign-in and streaming limits. It does not create a route through NAT;
an overlay, forwarded port or reachable proxy must provide the connection. No overlay or proxy is detected.

For a reverse proxy on its own hostname, list its socket address or range in
**Trusted proxies** (`trustedProxies`). Preserve `Host`, or set
`X-Forwarded-Host`; set `X-Forwarded-Proto` and append the real source to
`X-Forwarded-For`. RFC `Forwarded` is also supported. Configure the proxy to
replace client-supplied forwarding headers. Portico ignores these headers from
other peers and walks trusted proxy chains from right to left. The bundled web
app accepts writes whose origin matches the external request origin. No origin
allow-list variable is needed. Hosting below a URL path is unsupported.

In Docker bridge, rootless port publishing or a TCP tunnel, the source address
may be the gateway for every client. If forwarding headers are available, trust
the HTTP proxy's exact address and pass the client address. Otherwise remove the
hidden gateway's range from Home networks, or use an empty list to treat everyone
as remote. The connectivity status report shows the client address Portico used;
it cannot recover an address that the transport did not send.

**External address** (`advertiseUrl`) adds an HTTP or HTTPS origin clients can
use, such as `https://media.example.com`. No path, query or credentials are
accepted. `accessUrls` adds further origins. Local clients read them at
`GET /v1/networking/connection`; only HTTPS origins are published to Hosted.
Every client still verifies the server's pinned identity before sending secrets.

For the server's own TLS listener, set `customCertificatePath`,
`customCertificateKeyPath` and `customCertificateDomain` together. Use a PEM
certificate chain and unencrypted PEM private key readable by the server's user.
Hosted is optional. Changes reload within 30 seconds of the next handshake or
status request; a broken replacement retains the last valid certificate until
it expires. Status appears in `GET /v1/admin/connectivity/status`.

Updated native apps can automatically trust an owner's self-signed or private-CA
certificate when the server's signed identity proof lists that exact certificate.
Portico takes this list from the TLS material it serves, including the managed
renewal fallback. If TLS terminates at a reverse proxy, Portico cannot infer the
proxy's certificate: an untrusted proxy certificate requires the app's explicit
trust question. Older servers also use that question path with updated apps.

**Connection limit** (`connectionLimit`) accepts 0 for the memory-based default,
or a number up to 65536. Restart after changing it. The console and sign-in have
32 reserved connections beyond the ordinary limit. Request lanes, header
deadlines and HTTP/2 stream limits still apply. Capacity is not a tested viewer
count; increase it according to the machine and workload.

First-run setup needs no local code. Browser setup writes require JSON and the
same request origin and a local or explicitly allowed setup hostname; cross-site
forms and cross-site preflights cannot claim the server. Native API clients may omit Origin but must send `application/json`.

## Backup and restore

Settings → Server → Storage & backups provides backup and restore controls.
A manual backup creates `Database/server.sqlite`, a checksum manifest,
`portico.toml`, `Metadata/` and `Keys/`. It excludes media, recordings, Cache,
Logs, Temp and transcodes. A database backup is not a backup of your collection.

The API can start a backup with
`POST /v1/admin/backups {"operationId":"backup-20261013-1"}`. It answers
`202 {"jobId":"…"}`. Poll `GET /v1/admin/backups`: `running` reports progress,
and the finished entry appears in `backups`. Copy the whole finished backup
directory to separate storage. Scheduled copies use the configured keep count;
manual copies remain until deleted.

To restore a listed backup, send
`POST /v1/admin/backups/restore {"operationId":"restore-20261013-1","source":{"backupId":"…"}}`.
A full backup folder or supported standalone database file can instead be named
with `source.path`, an absolute path on the server. Poll the same list for the
restore job. Validation and copying happen in the background. The server stages
the result and restarts; the connection dropping during that restart is expected.
After it returns, sign in again and check `lastRestore`, your libraries and keys.

Restore requires the server's single-instance lock. It refuses databases from
an incompatible prerelease schema or a newer schema; do not assume an arbitrary
old backup is accepted by a new build. Restore keeps the preceding database and
state in a pre-restore folder beside the live database. If applying a staged
restore cannot be completed safely, startup refuses a partial state; fix the
reported disk or permission problem and start again. Keep those recovery files.

People are signed out by restore. Passwords, two-factor seeds and approved
devices return to their backup-time state. API keys and personal tokens survive,
so a credential revoked after the backup may work again; review their lists.
Owner-supplied certificates outside the selected state files, host service
configuration, media and recordings need their own backups.

For a complete offline recovery copy, stop Portico cleanly and copy the entire
state folder, plus external media/recording/certificate folders you need. Never
copy only the live `portico.db` while it is running: SQLite may also have data
in its WAL. Restore an offline copy while stopped, preserve the old folder,
retain ownership/ACLs, and start the same build first. A raw filesystem copy
contains live session material; protect it as a secret and prefer the built-in
restore path when restoring its database.

## Reverse proxy and overlays

An Nginx example for a proxy on the same machine, with your existing certificate:

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

Set Trusted proxies to the actual backend peer (`127.0.0.1/32` here) and External
address to `https://media.example.com`. Overwriting forwarding headers in this
single-proxy example prevents a client claiming another address. A multi-proxy
chain must preserve only addresses validated by its trusted hops. Event streams
need buffering disabled and an idle timeout longer than their 25-second heartbeat.
The proxy must allow the upload sizes used by your chosen API operations.

With Tailscale or another overlay, use the server's overlay address and the
port, or a hostname that resolves to it. Add only the overlay ranges you want
to count as home to Home networks; an overlay address is otherwise judged by
the same owner list as every address. HTTP remains accepted. Overlay reachability
and access policy are configured in the overlay itself. IPv6 literals need
brackets in URLs; a custom certificate should match the hostname clients use.
