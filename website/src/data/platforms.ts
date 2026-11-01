import type { PlatformIconName } from '@/data/downloads';

export type Platform = { name: string; icon: PlatformIconName };

export const serverPlatforms: Platform[] = [
  { name: 'Windows', icon: 'windows' },
  { name: 'macOS', icon: 'apple' },
  { name: 'Linux', icon: 'linux' },
  { name: 'Docker', icon: 'docker' }
];

export const watchPlatforms: Platform[] = [
  { name: 'Web', icon: 'web' },
  { name: 'iPhone & iPad', icon: 'apple' },
  { name: 'Android', icon: 'android' },
  { name: 'Apple TV', icon: 'apple' },
  { name: 'Android TV', icon: 'android' },
  { name: 'Fire TV', icon: 'fire-tv' },
  { name: 'Roku', icon: 'roku' },
  { name: 'LG', icon: 'lg' },
  { name: 'Samsung', icon: 'samsung' }
];
