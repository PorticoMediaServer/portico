---
title: Install on Linux
description: Install Portico Server as a managed service on a supported Linux system.
category: install
order: 3
audience: Server owners
updated: 2026-10-18
---

Download the Linux package that matches your server's architecture. Keep Portico's executable, data and media folders separate.

## Choose a package

Portico publishes three formats for both x64 and ARM64:

| Format | Use it on |
| --- | --- |
| DEB package | Debian or Ubuntu |
| RPM package | Fedora or a related distribution |
| `.tar.gz` bundle | Other Linux systems with compatible runtime requirements |

The archive contains Portico Server, Portico Web, FFmpeg, FFprobe, licences and third-party notices. The package name is `portico-media-server`.

For an x64 server, install the downloaded package with one of these commands. Use the corresponding `arm64` filename on ARM64.

```sh
sudo apt install ./portico-media-server-linux-x64.deb
```

On an RPM system:

```sh
sudo dnf install ./portico-media-server-linux-x64.rpm
```

## Service account and data

DEB and RPM packages create the `portico` service account and use `/var/lib/portico-media-server` as the default data folder. Give `portico` read access to the library folders and traversal access to their parent folders. See [Portico data folder](/docs/install/server-data-folder/).

The systemd service keeps the filesystem read-only except for its data folder. If you enable media deletion or use a separate recordings, cache or backup folder, grant Unix permissions and add the selected writable paths through `sudo systemctl edit portico-media-server`:

```ini
[Service]
ReadWritePaths=/srv/media/Movies /srv/recordings
```

Create the directories first, replace the example paths and quote paths containing spaces. Keep the service's other sandbox settings. Apply changes with `sudo systemctl daemon-reload`, then restart the service. Selecting a folder or setting an environment variable doesn't grant write access.

## Manage the service

The systemd unit is `portico-media-server.service`. Package installation enables and starts it when systemd is running, subject to your distribution's service-start policy.

```sh
sudo systemctl enable --now portico-media-server
sudo systemctl status portico-media-server
sudo systemctl restart portico-media-server
sudo systemctl stop portico-media-server
sudo systemctl start portico-media-server
sudo journalctl -u portico-media-server -n 100
```

An upgrade restarts an active service and leaves a stopped service stopped. Optional environment settings go in the root-owned `/etc/portico-media-server/environment` file. A custom data path also needs a matching `ReadWritePaths` entry.

## Open Portico and check the firewall

The default listener is TCP `32500`. Open `http://127.0.0.1:32500/` on the server, or use its local IP address from another device. Follow [Complete first-run setup](/docs/install/first-run/).

If you use UFW, this example allows a home subnet. Replace the subnet with your own:

```sh
sudo ufw allow from 192.168.1.0/24 to any port 32500 proto tcp
```

On firewalld, add the port to the zone used by your trusted local network:

```sh
sudo firewall-cmd --permanent --zone=home --add-port=32500/tcp
sudo firewall-cmd --reload
```

Keep database files, operating-system administration and unrelated ports private. Review [Enable remote access](/docs/remote/enable-remote-access/) before allowing connections from outside your home.

## Portable archive

Extract the `.tar.gz` bundle and run `./start-portico.sh`. It runs under your account and installs no service or firewall rule. Its default data folder is `state` beside the launcher. Set an absolute `PORTICO_STATE_DIR` to use another location. For unattended use, arrange a service that runs the launcher under a dedicated account.

## Check after a restart

Restart the host, then confirm that the service is active, its media mounts are present and a local client can connect.
