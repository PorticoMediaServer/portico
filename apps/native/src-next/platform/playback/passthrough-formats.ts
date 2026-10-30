import type {CapabilityDocument, ClientAudioPassthrough} from '../../../../../packages/client-core/src/client-profile';

export const passthroughFormats = ['dd', 'ddp', 'truehd', 'dts', 'dtshd', 'dtsx'] as const;
export type PassthroughFormat = typeof passthroughFormats[number];
export type PassthroughFormats = Readonly<Partial<Record<PassthroughFormat, 'auto' | 'off'>>>;
const record = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value);
export function readPassthroughFormats(value: unknown): PassthroughFormats {
  return Object.freeze(Object.fromEntries(passthroughFormats.flatMap(format => record(value) && value[format] === 'off' ? [[format, 'off']] : [])));
}

/** Only remove reported capabilities. No switch manufactures a codec, variant or receiver. */
export function limitPassthroughFormats(document: CapabilityDocument, choices: PassthroughFormats): CapabilityDocument {
  if (!passthroughFormats.some(format => choices[format] === 'off')) return document;
  const allowed = (format: PassthroughFormat) => choices[format] !== 'off';
  const filter = (entries: ClientAudioPassthrough[] | undefined) => entries?.flatMap(entry => {
    if ((entry.codec === 'ac3' && !allowed('dd')) || (entry.codec === 'eac3' && !allowed('ddp')) || (entry.codec === 'truehd' && !allowed('truehd'))) return [];
    if (entry.codec !== 'dts') return [entry];
    if (!entry.variants) return allowed('dts') ? [{...entry, ...(entry.objectAudio ? {objectAudio: entry.objectAudio.filter(value => value !== 'dtsx' || allowed('dtsx'))} : {})}] : [];
    const variants = entry.variants.filter(variant => allowed(variant === 'dtsx' ? 'dtsx' : variant === 'dts_core' ? 'dts' : 'dtshd'));
    if (!variants.length) return [];
    return [{...entry, variants, ...(entry.objectAudio ? {objectAudio: entry.objectAudio.filter(value => value !== 'dtsx' || allowed('dtsx'))} : {})}];
  });
  const engine = (value: CapabilityDocument['engines']['native']) => ({...value, ...(value.audioPassthrough ? {audioPassthrough: filter(value.audioPassthrough)} : {})});
  return {...document, engines: {native: engine(document.engines.native), ...(document.engines.mpv ? {mpv: engine(document.engines.mpv)} : {})}};
}
