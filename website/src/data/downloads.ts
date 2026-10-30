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
  | 'xbox';

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

const serverRelease = 'https://github.com/PorticoMediaServer/portico-server/releases/latest/download';

export const downloadGroups: DownloadGroup[] = [
  {
    title: 'Portico Server',
    description: 'Install Portico on the computer that stores or can reach your media.',
    items: [
      { name: 'Windows x64', detail: 'Windows 10 and 11 · unsigned installer', icon: 'windows', action: 'Download', href: `${serverRelease}/Portico-Media-Server-Windows-x64-Setup.exe` },
      { name: 'Windows ARM64', detail: 'Windows 11 on ARM · unsigned installer', icon: 'windows', action: 'Download', href: `${serverRelease}/Portico-Media-Server-Windows-arm64-Setup.exe` },
      { name: 'macOS', detail: 'Apple silicon · unsigned DMG', icon: 'apple', action: 'Download', href: `${serverRelease}/Portico-Media-Server-macOS-arm64.dmg` },
      { name: 'Linux archive (x64)', detail: 'Distribution-neutral · tar.gz', icon: 'linux', action: 'Download', href: `${serverRelease}/portico-media-server-linux-x64.tar.gz` },
      { name: 'Linux archive (ARM64)', detail: 'Distribution-neutral · tar.gz', icon: 'linux', action: 'Download', href: `${serverRelease}/portico-media-server-linux-arm64.tar.gz` },
      { name: 'Debian and Ubuntu (x64)', detail: 'Installable · DEB', icon: 'linux', action: 'Download', href: `${serverRelease}/portico-media-server-linux-x64.deb` },
      { name: 'Debian and Ubuntu (ARM64)', detail: 'Installable · DEB', icon: 'linux', action: 'Download', href: `${serverRelease}/portico-media-server-linux-arm64.deb` },
      { name: 'Fedora and RPM Linux (x64)', detail: 'Installable · RPM', icon: 'linux', action: 'Download', href: `${serverRelease}/portico-media-server-linux-x64.rpm` },
      { name: 'Fedora and RPM Linux (ARM64)', detail: 'Installable · RPM', icon: 'linux', action: 'Download', href: `${serverRelease}/portico-media-server-linux-arm64.rpm` },
      { name: 'Docker', detail: 'Multi-architecture image · amd64 and arm64', icon: 'docker', action: 'Download', href: 'https://github.com/PorticoMediaServer/portico-server/pkgs/container/portico-server' }
    ]
  },
  {
    title: 'Phones and tablets',
    description: 'Keep your libraries, progress, and playback close at hand.',
    items: [
      { name: 'iPhone and iPad', detail: 'iOS and iPadOS', icon: 'apple', action: 'Coming soon' },
      { name: 'Android', detail: 'Phones and tablets', icon: 'android', action: 'Coming soon' }
    ]
  },
  {
    title: 'TV and streaming devices',
    description: 'A remote-first Portico experience for the living room.',
    items: [
      { name: 'Apple TV', detail: 'tvOS', icon: 'apple', action: 'Coming soon' },
      { name: 'Android TV', detail: 'Android TV and Google TV', icon: 'android', action: 'Coming soon' },
      { name: 'Fire TV', detail: 'Fire OS and Vega OS', icon: 'fire-tv', action: 'Coming soon' },
      { name: 'Roku', detail: 'Roku streaming players and televisions', icon: 'roku', action: 'Coming soon' },
      { name: 'LG', detail: 'webOS televisions', icon: 'lg', action: 'Coming soon' },
      { name: 'Samsung', detail: 'Tizen televisions', icon: 'samsung', action: 'Coming soon' },
      { name: 'Xbox', detail: 'Xbox One and Xbox Series', icon: 'xbox', action: 'Coming soon' }
    ]
  }
];
