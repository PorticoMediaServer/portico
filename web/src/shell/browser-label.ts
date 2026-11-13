import {defaultI18n} from '@i18n';

/** A plain description of the real browser ("Chrome on a Mac"), for details shown to the owner or copied: never a
 * user-agent string. Shared by the crash page and a playback failure's Details. */
export function browserLabel(): string {
  const t = defaultI18n.t, ua = typeof navigator === 'undefined' ? '' : navigator.userAgent;
  const engines: readonly (readonly [RegExp, string])[] = [
    [/Edg\//, 'Edge'], [/OPR\//, 'Opera'], [/Firefox\//, 'Firefox'], [/Chrome\//, 'Chrome'], [/Safari\//, 'Safari'],
  ];
  const systems: readonly (readonly [RegExp, string])[] = [
    [/iPhone|iPad|iPod/, 'an iPhone or iPad'], [/Android/, 'Android'], [/Mac OS X/, 'a Mac'], [/Windows/, 'Windows'], [/CrOS/, 'ChromeOS'], [/Linux/, 'Linux'],
  ];
  const browser = engines.find(([pattern]) => pattern.test(ua))?.[1];
  const system = systems.find(([pattern]) => pattern.test(ua))?.[1];
  if (!browser) return t('next.states.unknownBrowser');
  return system ? t('next.states.browserOnSystem', {browser, system}) : browser;
}
