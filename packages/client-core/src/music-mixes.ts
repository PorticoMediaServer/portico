import type {MessageId, MessageValues} from '../../i18n/src/index.ts';

const styleKeys: Readonly<Record<string, MessageId>> = {
  classical: 'music.mix.style.classical', techno: 'music.mix.style.techno',
  electronic: 'music.mix.style.electronic', rock: 'music.mix.style.rock',
  ambient: 'music.mix.style.ambient', opera: 'music.mix.style.opera',
  pop: 'music.mix.style.pop', soft: 'music.mix.style.soft',
  'new age': 'music.mix.style.newAge', dance: 'music.mix.style.dance',
  country: 'music.mix.style.country', metal: 'music.mix.style.metal',
};
const record = (value: unknown): value is Record<string, unknown> => typeof value === 'object' && value !== null && !Array.isArray(value);

/** Format the approved server name source. The row and Save as playlist use
 * this same name; no model tag, reason, ID or demo alias becomes raw copy. */
export function musicMixDisplayName(
  source: unknown, artists: readonly unknown[],
  t: (key: MessageId, values?: MessageValues) => string,
  format: 'name' | 'label' = 'name',
): string | undefined {
  if (!record(source)) return undefined;
  if (source.kind === 'style') {
    if (Object.keys(source).length !== 4 || typeof source.tag !== 'string' || source.model !== 'musicnn-mtt' || typeof source.version !== 'string' || source.version.length === 0) return undefined;
    const key = Object.hasOwn(styleKeys, source.tag) ? styleKeys[source.tag] : undefined;
    return key ? format === 'label' ? t(key) : t('music.mix.name', {name: t(key)}) : undefined;
  }
  if (source.kind !== 'artist' || Object.keys(source).length !== 2 || typeof source.artistId !== 'string') return undefined;
  const artist = artists.find(value => record(value) && value.id === source.artistId);
  if (!record(artist) || typeof artist.name !== 'string' || artist.name.trim().length === 0) return undefined;
  return format === 'label' ? artist.name : t('music.mix.name', {name: artist.name});
}
