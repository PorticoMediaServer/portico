/**
 * Languages as tracks name them. A file's stream says "fra" (ISO 639-2), a subtitle plan says
 * "fr" (639-1), a browser text track says "fr-FR": the same language to a person, so every
 * comparison goes through one key.
 */
const ALPHA3: Readonly<Record<string, string>> = {
  eng: 'en', fra: 'fr', fre: 'fr', deu: 'de', ger: 'de', spa: 'es', ita: 'it', por: 'pt', rus: 'ru', jpn: 'ja', kor: 'ko', zho: 'zh', chi: 'zh',
  nld: 'nl', dut: 'nl', swe: 'sv', nor: 'no', nob: 'nb', nno: 'nn', dan: 'da', fin: 'fi', pol: 'pl', tur: 'tr', ara: 'ar', heb: 'he', hin: 'hi',
  tha: 'th', vie: 'vi', ces: 'cs', cze: 'cs', ell: 'el', gre: 'el', hun: 'hu', ron: 'ro', rum: 'ro', ukr: 'uk', ind: 'id', msa: 'ms', may: 'ms',
  cat: 'ca', hrv: 'hr', srp: 'sr', slk: 'sk', slo: 'sk', slv: 'sl', bul: 'bg', lit: 'lt', lav: 'lv', est: 'et', fas: 'fa', per: 'fa', isl: 'is', ice: 'is',
  ben: 'bn', tam: 'ta', tel: 'te', urd: 'ur', fil: 'tl', tgl: 'tl', eus: 'eu', baq: 'eu', glg: 'gl', cym: 'cy', wel: 'cy', gle: 'ga', lat: 'la',
};

/** The comparison key for a language code: its primary subtag, two letters where the language has them. Empty for no language ("", "und"). */
export function languageKey(code: unknown): string {
  if (typeof code !== 'string') return '';
  const primary = code.trim().toLowerCase().split(/[-_]/)[0] ?? '';
  if (!primary || primary === 'und' || primary === 'zxx') return '';
  return ALPHA3[primary] ?? primary;
}

/** Whether two codes name the same language ("fra", "fr", "fr-CA"). Two codes without a language never match. */
export function sameLanguage(a: unknown, b: unknown): boolean {
  const key = languageKey(a);
  return key !== '' && key === languageKey(b);
}

/**
 * What a track is called: its language as the viewer reads it ("French"), then its own title only
 * when the title says something more ("SDH", "Director's commentary"). A title that is just the
 * language again, in any spelling ("Français", "French", "fr"), or a container's stock word
 * ("Subtitles", "Audio") adds nothing and is dropped. Empty when the track names nothing.
 */
export function trackName(track: Readonly<{language?: string; title?: string}>, locale?: string): string {
  const code = track.language && languageKey(track.language) ? track.language : '';
  const display = (inLocale: string | undefined) => { try { return code ? new Intl.DisplayNames(inLocale, {type: 'language'}).of(languageKey(code)) ?? '' : ''; } catch { return ''; } };
  const language = display(locale);
  const title = (track.title ?? '').trim();
  const fold = (v: string) => v.toLowerCase();
  const stock = /^(subtitles?|captions?|audio|track(\s*\d+)?|default|und)$/i;
  const repeats = [language, display(code || undefined), display('en'), code, languageKey(code)].some(name => name && fold(title) === fold(name));
  const extra = title && !repeats && !stock.test(title) ? title.replace(new RegExp(`^(${[language, display('en')].filter(Boolean).map(n => n.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('|') || '\\b\\B'})\\s*[-–·:(]?\\s*`, 'i'), '').replace(/\)$/, '').trim() : '';
  return [language, extra && fold(extra) !== fold(language) ? extra : ''].filter(Boolean).join(' · ') || title;
}
