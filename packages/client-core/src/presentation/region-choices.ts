/**
 * X-03: language is a choice, never free text. The same options on every client: "Same as this
 * device" (`auto`) and the supported catalogue locales. A value set elsewhere (the web, an import)
 * is kept as a choice so it can be seen and changed back. There is no time zone choice: times are
 * shown in the device's own zone.
 */
import {supportedLocales, type I18n, type MessageId} from '../../../i18n/src/index.ts';

export type RegionChoice = Readonly<{id: string; label: string}>;

export function localeChoices(i18n: I18n, current?: string): readonly RegionChoice[] {
  const out: RegionChoice[] = [{id: 'auto', label: i18n.t('preferences.followDevice')}, ...supportedLocales.map(l => ({id: l, label: i18n.t(`locale.${l}` as MessageId)}))];
  if (current && !out.some(o => o.id === current)) out.push({id: current, label: current});
  return out;
}
