export type PlatformIconName =
  | 'windows'
  | 'apple'
  | 'linux'
  | 'docker'
  | 'android'
  | 'roku'
  | 'lg'
  | 'samsung'
  | 'fire-tv'
  | 'web';

export type DownloadItem = {
  name: string;
  detail: string;
  icon: PlatformIconName;
  action: 'Download' | 'Coming soon';
  href?: string;
};

export type DownloadGroup = {
  title: string;
  description: string;
  items: DownloadItem[];
};

// The latest published release on the monorepo. Internal 0.0.x builds carry a development engine,
// so their file names end in -UNSEALED-NOT-FOR-USE-ENGINE; drop the suffix here once a release ships
// the qualified engine (the E2E link check fails if a link stops resolving).
const serverRelease = 'https://github.com/PorticoMediaServer/portico/releases/latest/download';
const unsealed = '-UNSEALED-NOT-FOR-USE-ENGINE';

export const downloadGroups: DownloadGroup[] = [
  {
    title: 'Portico Server',
    description: 'Install it on the computer that holds your media, or one that can reach it over the network.',
    items: [
      { name: 'Windows x64', detail: 'Windows 10 and 11 · unsigned installer', icon: 'windows', action: 'Download', href: `${serverRelease}/Portico-Media-Server-Windows-x64-Setup${unsealed}.exe` },
      { name: 'Windows ARM64', detail: 'Windows 11 on ARM · unsigned installer', icon: 'windows', action: 'Coming soon' },
      { name: 'macOS', detail: 'Apple silicon · unsigned DMG', icon: 'apple', action: 'Download', href: `${serverRelease}/Portico-Media-Server-macOS-arm64${unsealed}.dmg` },
      { name: 'Linux archive (x64)', detail: 'Distribution-neutral · tar.gz', icon: 'linux', action: 'Download', href: `${serverRelease}/portico-media-server-linux-x64${unsealed}.tar.gz` },
      { name: 'Linux archive (ARM64)', detail: 'Distribution-neutral · tar.gz', icon: 'linux', action: 'Download', href: `${serverRelease}/portico-media-server-linux-arm64${unsealed}.tar.gz` },
      { name: 'Debian and Ubuntu (x64)', detail: 'Installable · DEB', icon: 'linux', action: 'Download', href: `${serverRelease}/portico-media-server-linux-x64${unsealed}.deb` },
      { name: 'Debian and Ubuntu (ARM64)', detail: 'Installable · DEB', icon: 'linux', action: 'Download', href: `${serverRelease}/portico-media-server-linux-arm64${unsealed}.deb` },
      { name: 'Fedora and RPM Linux (x64)', detail: 'Installable · RPM', icon: 'linux', action: 'Download', href: `${serverRelease}/portico-media-server-linux-x64${unsealed}.rpm` },
      { name: 'Fedora and RPM Linux (ARM64)', detail: 'Installable · RPM', icon: 'linux', action: 'Download', href: `${serverRelease}/portico-media-server-linux-arm64${unsealed}.rpm` },
      { name: 'Docker', detail: 'Multi-architecture image · amd64 and arm64', icon: 'docker', action: 'Download', href: 'https://github.com/PorticoMediaServer/portico/pkgs/container/portico-server' }
    ]
  },
  {
    title: 'Phones and tablets',
    description: 'Install Portico on iPhone, iPad or Android, then connect to your server.',
    items: [
      { name: 'iPhone and iPad', detail: 'iOS and iPadOS', icon: 'apple', action: 'Coming soon' },
      { name: 'Android', detail: 'Phones and tablets', icon: 'android', action: 'Coming soon' }
    ]
  },
  {
    title: 'TV and streaming devices',
    description: 'Install the Portico app on your TV or streaming player, then connect to your server.',
    items: [
      { name: 'Apple TV', detail: 'tvOS', icon: 'apple', action: 'Coming soon' },
      { name: 'Android TV', detail: 'Android TV and Google TV', icon: 'android', action: 'Coming soon' },
      { name: 'Fire TV', detail: 'Fire OS and Vega OS', icon: 'fire-tv', action: 'Coming soon' },
      { name: 'Roku', detail: 'Roku streaming players and televisions', icon: 'roku', action: 'Coming soon' },
      { name: 'LG', detail: 'webOS televisions', icon: 'lg', action: 'Coming soon' },
      { name: 'Samsung', detail: 'Tizen televisions', icon: 'samsung', action: 'Coming soon' }
    ]
  }
];
