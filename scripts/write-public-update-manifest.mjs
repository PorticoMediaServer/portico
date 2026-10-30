#!/usr/bin/env node
import {writeFileSync} from 'node:fs';

const [version, repository = 'PorticoMediaServer/portico-server'] = process.argv.slice(2);
if (!/^\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?$/.test(version ?? '')) throw new Error('A semantic version is required.');
const tag = `v${version}`;
const base = `https://github.com/${repository}/releases/download/${tag}`;
const assets = {
  windowsX64: 'Portico-Media-Server-Windows-x64-Setup.exe',
  windowsArm64: 'Portico-Media-Server-Windows-arm64-Setup.exe',
  windowsX64Portable: 'Portico-Media-Server-Windows-x64-Portable.zip',
  windowsArm64Portable: 'Portico-Media-Server-Windows-arm64-Portable.zip',
  macosArm64: 'Portico-Media-Server-macOS-arm64.pkg',
  macosArm64DiskImage: 'Portico-Media-Server-macOS-arm64.dmg',
  linuxX64Archive: 'portico-media-server-linux-x64.tar.gz',
  linuxArm64Archive: 'portico-media-server-linux-arm64.tar.gz',
  linuxX64Deb: 'portico-media-server-linux-x64.deb',
  linuxArm64Deb: 'portico-media-server-linux-arm64.deb',
  linuxX64Rpm: 'portico-media-server-linux-x64.rpm',
  linuxArm64Rpm: 'portico-media-server-linux-arm64.rpm',
};
const channel = version.includes('-') ? 'beta' : 'stable';
writeFileSync('dist/portico-update-manifest.json', `${JSON.stringify({
  schemaVersion: 1,
  channel,
  version,
  tag,
  // The shape a server's update check reads (updates.feedUrl): the builds each
  // channel offers, with the release page as the link an owner follows.
  channels: {[channel]: [{version, channel, url: `https://github.com/${repository}/releases/tag/${tag}`}]},
  signed: false,
  automaticInstallationAllowed: false,
  warning: 'Unsigned development release. Verify checksums and install manually.',
  assets: Object.fromEntries(Object.entries(assets).map(([key, name]) => [key, {name, url: `${base}/${name}`}]))
}, null, 2)}\n`);
