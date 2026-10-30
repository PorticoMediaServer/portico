import {enUS, type MessageId} from './en-US.ts';
import {britishEnglish, canadianEnglish} from './regional.ts';

export {enUS, type MessageId};

/** Locales with a catalogue. en-US is the source and the fallback for every message. */
export const supportedLocales = ['en-US', 'en-CA', 'en-GB'] as const;
export type SupportedLocale = (typeof supportedLocales)[number];
export const sourceLocale: SupportedLocale = 'en-US';

/** The messages a locale words differently from en-US; the regional ones are derived by rule (regional.ts). */
export function catalogue(locale: SupportedLocale): Partial<Record<MessageId, string>> {
  return locale === 'en-CA' ? canadianEnglish() : locale === 'en-GB' ? britishEnglish() : enUS;
}
