export const platformMessages = {
  'next.boot.connecting': 'Connecting',
  'next.boot.stillConnecting': 'Still connecting',
  'next.boot.offline': 'You’re offline',
  'next.boot.tvOffline': 'This TV is offline',
  'next.platform.appName': 'Portico',
  'next.platform.galleryTitle': 'Kit',
  'next.platform.kitTitle': 'Component kit',
  'next.platform.allFamilies': 'All components',
  'next.platform.galleryFamily': 'Component family',
  'next.platform.galleryCase': 'State',
} as const satisfies Record<`next.platform.${string}` | `next.boot.${string}`, string>;
