/**
 * X-03: pure list logic for the ordered language pickers of Settings › Audio & subtitles
 * (import-free). Each client draws the list with its own controls; the rules are here.
 */

/** The registry allows at most 8 entries per language list. */
export const MAX_LANGUAGES = 8;

/** Common languages offered for adding, in a stable order. Codes only; names
 * resolve from the catalogue first (`language.<code>`), then `Intl.DisplayNames`,
 * so they follow the viewer's language even where `Intl.DisplayNames` is missing. */
export const COMMON_LANGUAGES: readonly string[] = [
  'en', 'ja', 'fr', 'de', 'es', 'it', 'pt', 'nl', 'sv', 'no', 'da', 'fi',
  'pl', 'cs', 'sk', 'hu', 'ro', 'el', 'tr', 'ru', 'uk', 'ar', 'he', 'hi',
  'th', 'vi', 'ko', 'zh',
];

/**
 * "Japanese" for `ja`. Resolution order: the catalogue (`language.<code>`,
 * passed in as `lookup` so this file stays import-free); then
 * `Intl.DisplayNames` when the runtime has it; then the code itself, so a
 * row never shows blank. Hermes (Apple) may lack `Intl.DisplayNames`, in
 * which case catalogue languages still render by name.
 */
export function languageName(code: string, lookup?: (key: string) => string | undefined, locale?: string): string {
  const known = lookup?.(`language.${code}`);
  if (known) return known;
  try {
    const names = new Intl.DisplayNames(locale ?? undefined, {type: 'language'});
    return names.of(code) ?? code;
  } catch {
    return code;
  }
}

/** Common languages not already chosen, in the stable offer order. */
export function availableLanguages(chosen: readonly string[]): readonly string[] {
  const has = new Set(chosen);
  return COMMON_LANGUAGES.filter(c => !has.has(c));
}

export function addLanguage(list: readonly string[], code: string): readonly string[] {
  if (!code || list.includes(code) || list.length >= MAX_LANGUAGES) return list;
  return [...list, code];
}

export function removeLanguage(list: readonly string[], index: number): readonly string[] {
  if (index < 0 || index >= list.length) return list;
  return list.filter((_, i) => i !== index);
}

export function moveLanguage(list: readonly string[], index: number, direction: 'up' | 'down'): readonly string[] {
  const to = direction === 'up' ? index - 1 : index + 1;
  if (index < 0 || index >= list.length || to < 0 || to >= list.length) return list;
  const next = [...list];
  const [moved] = next.splice(index, 1);
  next.splice(to, 0, moved!);
  return next;
}
