#!/usr/bin/env node
import {writeFileSync} from 'node:fs';

// unsealed: an internal NOT-FOR-USE build. Its feed lists no build, so no server ever offers it as an update.
const [version, repository = 'PorticoMediaServer/portico', mode = ''] = process.argv.slice(2);
if (mode !== '' && mode !== 'unsealed') throw new Error('The third argument is unsealed or nothing.');
const unsealed = mode === 'unsealed', suffix = unsealed ? '-UNSEALED-NOT-FOR-USE-ENGINE' : '';
if (!/^\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?$/.test(version ?? '')) throw new Error('A semantic version is required.');
const tag = `v${version}`;
const base = `https://github.com/${repository}/releases/download/${tag}`;
const assets = {
  windowsX64: `Portico-Media-Server-Windows-x64-Setup${suffix}.exe`,
  windowsArm64: `Portico-Media-Server-Windows-arm64-Setup${suffix}.exe`,
  windowsX64Portable: `Portico-Media-Server-Windows-x64-Portable${suffix}.zip`,
  windowsArm64Portable: `Portico-Media-Server-Windows-arm64-Portable${suffix}.zip`,
  macosArm64: `Portico-Media-Server-macOS-arm64${suffix}.pkg`,
  macosArm64DiskImage: `Portico-Media-Server-macOS-arm64${suffix}.dmg`,
  linuxX64Archive: `portico-media-server-linux-x64${suffix}.tar.gz`,
  linuxArm64Archive: `portico-media-server-linux-arm64${suffix}.tar.gz`,
  linuxX64Deb: `portico-media-server-linux-x64${suffix}.deb`,
  linuxArm64Deb: `portico-media-server-linux-arm64${suffix}.deb`,
  linuxX64Rpm: `portico-media-server-linux-x64${suffix}.rpm`,
  linuxArm64Rpm: `portico-media-server-linux-arm64${suffix}.rpm`,
};
const channel = version.includes('-') ? 'beta' : 'stable';
writeFileSync('dist/portico-update-manifest.json', `${JSON.stringify({
  schemaVersion: 1,
  channel,
  version,
  tag,
  // The shape a server's update check reads (updates.feedUrl): the builds each
  // channel offers, with the release page as the link an owner follows.
  channels: unsealed ? {} : {[channel]: [{version, channel, url: `https://github.com/${repository}/releases/tag/${tag}`}]},
  signed: false,
  automaticInstallationAllowed: false,
  warning: unsealed ? 'Internal build, NOT FOR USE: unsealed, with a development FFmpeg engine. Never offered as an update.' : 'Unsigned development release. Verify checksums and install manually.',
  assets: Object.fromEntries(Object.entries(assets).map(([key, name]) => [key, {name, url: `${base}/${name}`}]))
}, null, 2)}\n`);
