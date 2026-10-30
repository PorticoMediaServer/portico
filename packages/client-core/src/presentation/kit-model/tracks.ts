import {languageKey} from '../language.ts';
import {bitRateText, bytesText, codecText, hdrText, heightText} from './technical.ts';
import {compact, integer, part, supplied, type FactPart, type TextEnvironment} from './text.ts';

export type TrackFacts = Readonly<{
  id: string; resourceId?: string; streamIndex?: number;
  /** The server's 1-based place in the whole track list, when it is windowed. */
  ordinal?: number;
  /** HLS LABEL/NAME is transport identity, never display text to sanitize or split. */
  renditionLabel?: string;
  language?: string; title?: string; channels?: number; channelLayout?: string;
  codec?: string; format?: string; objectAudio?: 'atmos' | 'dtsx';
  forced?: boolean; hearingImpaired?: boolean; commentary?: boolean; default?: boolean; external?: boolean;
}>;
export type TrackModel = Readonly<{
  id: string; resourceId?: string; streamIndex?: number; renditionLabel?: string;
  name: FactPart; detail: readonly FactPart[]; technical: readonly FactPart[];
}>;

function languageText(code: string | undefined, locale?: string): string | undefined {
  const key = languageKey(code);
  if (!key) return undefined;
  const tag = code?.includes('-') ? code : key;
  try {
    const name = new Intl.DisplayNames(locale ?? 'en-US', {type: 'language', fallback: 'none'}).of(tag!);
    return name && name !== tag ? name : undefined;
  } catch { return undefined; }
}

function layoutText(track: TrackFacts, env: TextEnvironment, context: 'generic' | 'file-info'): string | undefined {
  const layout = track.channelLayout?.toLowerCase(), named = /^(\d+\.\d+)(?:\([^)]*\))?$/.exec(layout ?? '')?.[1];
  if (named) return context === 'file-info' ? named : env.t('next.kit.surround', {layout: named});
  if (layout === 'stereo' || track.channels === 2) return env.t('next.kit.stereo');
  if (layout === 'mono' || track.channels === 1) return env.t('next.kit.mono');
  if (track.channels === 6 || track.channels === 8) {
    const channels = track.channels === 6 ? '5.1' : '7.1';
    return context === 'file-info' ? channels : env.t('next.kit.surround', {layout: channels});
  }
  return integer(track.channels) ? env.t('next.kit.channels', {count: track.channels}) : undefined;
}

/** Only recognize a complete stock language/flag label; custom provider titles stay intact. */
function stockFlagTitle(title: string | undefined, languages: readonly (string | undefined)[]): 'sdh' | 'forced' | undefined {
  const flag = (s: string) => /^(sdh|cc|hearing impaired)$/i.test(s) ? 'sdh' : /^forced$/i.test(s) ? 'forced' : undefined;
  if (!supplied(title)) return undefined;
  const plain = flag(title.trim());
  if (plain) return plain;
  for (const language of languages) if (language && title.toLowerCase().startsWith(language.toLowerCase())) {
    const suffix = title.slice(language.length);
    if (!/^[\s(:–-]/.test(suffix)) continue;
    const rest = suffix.replace(/^[\s(:–-]+/, '').replace(/\)\s*$/, '').trim();
    const match = flag(rest);
    if (match) return match;
  }
  return undefined;
}

function trackModels(tracks: readonly TrackFacts[], kind: 'audio' | 'subtitle', env: TextEnvironment, context: 'generic' | 'file-info' = 'generic'): readonly TrackModel[] {
  const {t} = env;
  if (new Set(tracks.map(x => x.id)).size !== tracks.length) throw new Error('duplicate_track_identity');
  const models = tracks.map((track, index): TrackModel => {
    const language = track.language?.toLowerCase() === 'es-419' ? t('next.kit.language.es419') : languageText(track.language, env.locale), native = languageText(track.language, languageKey(track.language));
    const title = track.title;
    const repeated = !supplied(title) || [language, native, languageText(track.language, 'en-US'), track.language, languageKey(track.language)].some(x => x && x.toLowerCase() === title.toLowerCase());
    const stock = /^(subtitles?|captions?|audio|track(?:\s*\d+)?|default|und)$/i.test(title ?? '');
    const flagTitle = stockFlagTitle(title, [language, native, languageText(track.language, 'en-US'), track.language, languageKey(track.language)]);
    const titleSDH = flagTitle === 'sdh', titleForced = flagTitle === 'forced';
    const titleCommentary = /\bcommentary\b/i.test(title ?? '');
    const extra = !repeated && !stock && !titleSDH && !titleForced ? title : undefined;
    const name = language || (!stock && !titleSDH && !titleForced && supplied(title) ? title : t('next.kit.track', {number: track.ordinal ?? index + 1}));
    const codec = codecText(kind === 'subtitle' ? track.format ?? track.codec : track.codec, t), layout = kind === 'audio' ? layoutText(track, env, context) : undefined;
    return Object.freeze({id: track.id,
      ...(track.resourceId === undefined ? {} : {resourceId: track.resourceId}),
      ...(track.streamIndex === undefined ? {} : {streamIndex: track.streamIndex}),
      ...(track.renditionLabel === undefined ? {} : {renditionLabel: track.renditionLabel}),
      name: part('name', name, 'identity'),
      detail: compact([
        extra && extra !== name ? part('track-title', extra, 'context') : undefined,
        layout ? part('layout', layout, 'context') : undefined,
        track.forced || titleForced ? part('forced', t('next.kit.forced'), 'context') : undefined,
        track.hearingImpaired || titleSDH ? part('sdh', t(context === 'file-info' ? 'next.kit.fileInfo.sdh' : 'next.kit.sdh'), 'context') : undefined,
        track.commentary && !titleCommentary ? part('commentary', t('next.kit.commentary'), 'context') : undefined,
      ]),
      technical: compact([
        codec ? part('codec', codec) : undefined,
        track.objectAudio ? part('object-audio', t(track.objectAudio === 'atmos' ? 'next.kit.object.atmos' : 'next.kit.object.dtsx')) : undefined,
        track.default ? part('default', t('next.kit.default'), 'status') : undefined,
        track.external ? part('external', t('next.kit.external'), 'context') : undefined,
      ]),
    });
  });
  // An informational list already carries each track's facts; menu disambiguators are unrelated.
  if (context === 'file-info') return Object.freeze(models);
  // Only colliding display choices gain disambiguators. Codecs never become audio menu names.
  const signature = (m: TrackModel): string => JSON.stringify([m.name.text, m.detail.map(p => p.text)]);
  const groups = new Map<string, number[]>();
  models.forEach((m, i) => { const key = signature(m); const group = groups.get(key) ?? []; group.push(i); groups.set(key, group); });
  for (const group of groups.values()) if (group.length > 1) {
    if (kind === 'subtitle') for (const i of group) {
      const format = codecText(tracks[i]!.format, t);
      if (format) models[i] = Object.freeze({...models[i]!, detail: compact([...models[i]!.detail, part('format', format, 'supplement')])});
    }
    const repeated = new Map<string, number[]>();
    for (const i of group) { const key = signature(models[i]!); const found = repeated.get(key) ?? []; found.push(i); repeated.set(key, found); }
    for (const dup of repeated.values()) if (dup.length > 1) dup.forEach((i, n) => { models[i] = Object.freeze({...models[i]!, detail: compact([...models[i]!.detail, part('ordinal', t('next.kit.ordinal', {number: n + 1}), 'supplement')])}); });
  }
  return Object.freeze(models);
}

export const audioTrackModels = (tracks: readonly TrackFacts[], env: TextEnvironment): readonly TrackModel[] => trackModels(tracks, 'audio', env);
export const subtitleTrackModels = (tracks: readonly TrackFacts[], env: TextEnvironment): readonly TrackModel[] => trackModels(tracks, 'subtitle', env);

/** Internal File info projection; ordinary menu names/details retain their existing wording. */
export const fileInfoTrackModels = (tracks: readonly TrackFacts[], kind: 'audio' | 'subtitle', env: TextEnvironment): readonly TrackModel[] => trackModels(tracks, kind, env, 'file-info');

export type VersionInput = Readonly<{
  id: string; name?: string; edition?: string; container?: string; height?: number; videoCodec?: string;
  dynamicRange?: string; hdr10Plus?: boolean; bitRate?: number; sizeBytes?: number;
  /** The server's 1-based place among every version, so a windowed list keeps its numbers. */
  ordinal?: number;
}>;
export type VersionModel = Readonly<{id: string; name: readonly FactPart[]; detail: readonly FactPart[]}>;

/** Structured source facts only; never takes an old formatter's joined headline. */
export function versionModels(versions: readonly VersionInput[], env: TextEnvironment): readonly VersionModel[] {
  const {t} = env;
  if (new Set(versions.map(x => x.id)).size !== versions.length) throw new Error('duplicate_version_identity');
  const models = versions.map((v, i): VersionModel => {
    const height = integer(v.height) && v.height >= 2100 ? t('next.kit.4k') : heightText(v.height, t), codec = codecText(v.videoCodec, t), hdr = hdrText(v.dynamicRange, v.hdr10Plus, t);
    const name = compact([supplied(v.name) ? part('name', v.name, 'identity') : undefined, height ? part('resolution', height, 'value') : undefined, codec ? part('video-codec', codec) : undefined, hdr ? part('dynamic-range', hdr) : undefined, supplied(v.container) ? part('container', v.container.toUpperCase()) : undefined]);
    return Object.freeze({id: v.id, name: name.length ? name : [part('version', t('next.kit.version', {number: v.ordinal ?? i + 1}), 'identity')], detail: compact([supplied(v.edition) && v.edition !== v.name ? part('edition', v.edition, 'context') : undefined])});
  });
  const groups = new Map<string, number[]>();
  models.forEach((m, i) => { const key = JSON.stringify([m.name.map(p => p.text), m.detail.map(p => p.text)]); const group = groups.get(key) ?? []; group.push(i); groups.set(key, group); });
  for (const group of groups.values()) if (group.length > 1) {
    const rates = group.map(i => bitRateText(versions[i]!.bitRate, t)), sizes = group.map(i => bytesText(versions[i]!.sizeBytes, t));
    const distinct = (list: (string | undefined)[]) => list.every(supplied) && new Set(list).size === list.length;
    const values = distinct(rates) ? rates : distinct(sizes) ? sizes : group.map(i => t('next.kit.version', {number: versions[i]!.ordinal ?? i + 1}));
    const id = distinct(rates) ? 'bitrate' : distinct(sizes) ? 'size' : 'ordinal';
    group.forEach((i, n) => { models[i] = Object.freeze({...models[i]!, detail: compact([...models[i]!.detail, part(id, values[n]!, 'supplement')])}); });
  }
  return Object.freeze(models);
}
