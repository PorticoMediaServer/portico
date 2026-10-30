import {defaultI18n} from '@i18n';
import {availableLanguages, languageName, MAX_LANGUAGES} from '@core/presentation/language-list.ts';
import {captionAppearance} from '../../kit/media/caption-style';

/** The canonical registry cap for either ordered language list. */
export const maxPreferredLanguages = MAX_LANGUAGES;
export type PreferredLanguageKind = 'audio' | 'subtitles';

/** Audio may keep the original language; subtitles cannot list @original (server rule). */
export function preferredLanguagePool(kind: PreferredLanguageKind): readonly string[] {
  return ['@appLanguage', ...(kind === 'audio' ? ['@original'] : []), ...availableLanguages([])];
}
const lookup = (key: string) => defaultI18n.has(key) ? defaultI18n.t(key) : undefined;
/** @appLanguage displays the language chosen in Language & region, not a fixed English. */
export function appLanguageCode(appLocale: string): string {
  return appLocale.split('-')[0]!.toLowerCase();
}
/** The drawn picker entry: the special codes keep their authored labels. */
export function preferredLanguageName(code: string, kind: PreferredLanguageKind, uiLocale: string, appLocale: string): string {
  if (code === '@original') return kind === 'audio' ? defaultI18n.t('next.settingsSpec.account.audio-subtitles.groups.0.rows.0.control.items.1.label') : code;
  if (code === '@appLanguage') return defaultI18n.t('next.settingsSpec.account.audio-subtitles.groups.0.rows.0.control.items.0.label');
  return languageName(code, lookup, uiLocale);
}
/** A list entry names the language it stands for, so @appLanguage reads as that language. */
export function preferredLanguageEntryName(code: string, kind: PreferredLanguageKind, uiLocale: string, appLocale: string): string {
  if (code === '@appLanguage') return languageName(appLanguageCode(appLocale), lookup, uiLocale);
  return preferredLanguageName(code, kind, uiLocale, appLocale);
}
/** Entries offered by Add, in the canonical order, without languages already present. */
export function preferredLanguageChoices(kind: PreferredLanguageKind, present: readonly string[], uiLocale: string, appLocale: string) {
  const special = ['@appLanguage', ...(kind === 'audio' ? ['@original'] : [])].filter(code => !present.includes(code));
  return [...special.map(code => ({code, label: preferredLanguageName(code, kind, uiLocale, appLocale)})),
    ...availableLanguages(present).map(code => ({code, label: preferredLanguageName(code, kind, uiLocale, appLocale)}))];
}
const sizes = ['small', 'medium', 'large', 'extra-large'] as const;
const colors = ['white', 'yellow', 'cyan', 'green', 'pink'] as const;
const backgrounds = ['none', 'outline', 'shadow', 'translucent', 'opaque'] as const;
const positions = ['bottom', 'raised', 'top'] as const;
const oneOf = <T extends string>(values: readonly T[], value: unknown, fallback: T): T => values.includes(value as T) ? value as T : fallback;
/** The Preview draws the player's own palette, at the drawn stage proportion (5.2% of height at Medium). */
export function subtitlePreviewPaint(values: Readonly<{size?: unknown; color?: unknown; background?: unknown; position?: unknown}>) {
  const paint = captionAppearance({
    size: oneOf(sizes, values.size, 'medium'), color: oneOf(colors, values.color, 'white'),
    background: oneOf(backgrounds, values.background, 'translucent'), font: 'default',
  });
  return {...paint, scale: Number.parseFloat(paint.fontSize) / 100, position: oneOf(positions, values.position, 'bottom')};
}
export type SubtitlePreviewPaint = ReturnType<typeof subtitlePreviewPaint>;
