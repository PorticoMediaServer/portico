import type {IconName} from '../../../kit/Icon';

/** Use the advertised platform; unknown devices keep a neutral device glyph. */
export function profileDeviceIcon(platform: string): IconName {
  if (['ipados', 'tablet'].includes(platform)) return 'i-tablet';
  if (['tvos', 'androidtv', 'firetv', 'roku', 'tizen', 'webos'].includes(platform)) return 'i-tv';
  if (['ios', 'android', 'fireos'].includes(platform)) return 'i-phone';
  if (['web', 'desktop', 'macos', 'windows', 'linux'].includes(platform)) return 'i-laptop';
  if (platform === 'google-cast') return 'i-cast';
  return 'i-device';
}
