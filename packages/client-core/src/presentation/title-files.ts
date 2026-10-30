import {trackName} from './language.ts';
import type {TitleFile, TitleFileTrack} from '../detail.ts';
import type {MessageId, MessageValues} from '../../../i18n/src/index.ts';
import {formatBytes, formatDuration} from './content.ts';

/**
 * A title's Files list (Spec — Page Content §0.3; "Files and versions" in Spec — Title Pages §2):
 * what each file is, in the words a media-server owner reads — "4K · HEVC · Dolby Vision", one
 * line per audio and subtitle track, size and bitrate, and the path for owners. Clients draw the
 * lines; none of them decides what a line says.
 */
type Translate = (id: MessageId, values?: MessageValues) => string;

/** "4K", "1080p", "720p", "480p" or "SD", from the picture height. */
export function resolutionLabel(height: number, t: Translate): string {
  if (height >= 2100) return t('quality.4k');
  if (height >= 1000) return t('quality.height', {height: 1080});
  if (height >= 700) return t('quality.height', {height: 720});
  if (height >= 460) return t('quality.height', {height: 480});
  return t('quality.sd');
}

const CODECS: Readonly<Record<string, string>> = {
  h264: 'H.264', avc: 'H.264', hevc: 'HEVC', h265: 'HEVC', av1: 'AV1', vp9: 'VP9', mpeg2video: 'MPEG-2', mpeg4: 'MPEG-4', vc1: 'VC-1',
  aac: 'AAC', ac3: 'Dolby Digital', eac3: 'Dolby Digital Plus', truehd: 'Dolby TrueHD', dts: 'DTS', flac: 'FLAC', opus: 'Opus', mp3: 'MP3', vorbis: 'Vorbis', alac: 'ALAC', pcm_s16le: 'PCM', pcm_s24le: 'PCM',
  subrip: 'SRT', srt: 'SRT', ass: 'ASS', ssa: 'SSA', webvtt: 'WebVTT', vtt: 'WebVTT', mov_text: 'Text', hdmv_pgs_subtitle: 'PGS', pgs: 'PGS', dvd_subtitle: 'VobSub', dvb_subtitle: 'DVB',
};
/** A codec as people name it ("H.264", "Dolby Digital Plus"); an unknown one in capitals. */
export function codecLabel(codec: string): string {
  return CODECS[codec.toLowerCase()] ?? codec.toUpperCase();
}

/** "Dolby Vision", "HDR10+", "HDR10", "HLG"; nothing for SDR. */
export function dynamicRangeLabel(range?: string, hdr10Plus?: boolean): string | undefined {
  switch ((range ?? '').toLowerCase()) {
    case 'dolby_vision': case 'dolby-vision': case 'dovi': return 'Dolby Vision';
    case 'hdr10': return hdr10Plus ? 'HDR10+' : 'HDR10';
    case 'hdr10plus': case 'hdr10+': return 'HDR10+';
    case 'hlg': return 'HLG';
    default: return undefined;
  }
}

/** "7.1", "5.1", "Stereo", "Mono", from the layout when it says so, else the channel count. */
export function channelsLabel(track: Pick<TitleFileTrack, 'channels' | 'channelLayout'>, t: Translate): string | undefined {
  const layout = (track.channelLayout ?? '').toLowerCase();
  const named = /^(\d\.\d)/.exec(layout)?.[1];
  if (named) return named;
  if (layout === 'stereo' || track.channels === 2) return t('mediaInfo.stereo');
  if (layout === 'mono' || track.channels === 1) return t('mediaInfo.mono');
  if (track.channels === 6) return '5.1';
  if (track.channels === 8) return '7.1';
  return track.channels ? t('mediaInfo.channels', {count: track.channels}) : undefined;
}

/** "12.4 Mbps", "640 kbps". */
export function formatBitRate(bitsPerSecond?: number): string {
  if (!bitsPerSecond || !Number.isFinite(bitsPerSecond) || bitsPerSecond <= 0) return '';
  if (bitsPerSecond >= 1_000_000) { const v = bitsPerSecond / 1_000_000; return `${v >= 10 ? Math.round(v) : v.toFixed(1)} Mbps`; }
  return `${Math.round(bitsPerSecond / 1000)} kbps`;
}

export type TitleFileLines = Readonly<{
  id: string;
  /** "4K · HEVC · Dolby Vision · MKV": what the Version control and the file's heading both say. */
  summary: string;
  /** "58.2 GB · 62 Mbps · 2h 35m · 23.976 fps". */
  facts: readonly string[];
  audio: readonly string[];
  subtitles: readonly string[];
  /** Owners only. */
  path?: string;
  available: boolean;
}>;

function trackLine(track: TitleFileTrack, kind: 'audio' | 'subtitle', number: number, t: Translate, languageName: (code: string) => string): string {
  const known = track.language && track.language !== 'und' ? languageName(track.language) : undefined;
  const name = known ?? (track.title || t('mediaInfo.track', {number}));
  const marks = [track.default ? t('mediaInfo.default') : undefined, track.forced ? t('mediaInfo.forced') : undefined, track.external ? t('mediaInfo.external') : undefined].filter(Boolean).join(', ');
  // The track's own title follows only when it says more than the language ("SDH", not "Français"): the rule of `trackName`.
  const said = known ? trackName(track).split(' · ').slice(1).join(' · ') || undefined : undefined;
  const parts = kind === 'audio'
    ? [name, codecLabel(track.codec), channelsLabel(track, t), track.objectAudio === 'atmos' ? 'Atmos' : track.objectAudio === 'dtsx' ? 'DTS:X' : undefined, said]
    : [name, codecLabel(track.codec), said];
  return parts.filter(Boolean).join(' · ') + (marks ? ` (${marks})` : '');
}

/** One file as the lines its row prints. `languageName` turns "en" into "English" with the client's own Intl. */
export function titleFileLines(file: TitleFile, t: Translate, languageName: (code: string) => string): TitleFileLines {
  const v = file.video;
  const summary = [v?.height ? resolutionLabel(v.height, t) : undefined, v?.codec ? codecLabel(v.codec) : undefined, dynamicRangeLabel(v?.dynamicRange, v?.hdr10Plus), file.container ? file.container.toUpperCase() : undefined].filter(Boolean).join(' · ');
  const fps = v?.frameRate ? t('mediaInfo.fps', {fps: Number.isInteger(v.frameRate) ? String(v.frameRate) : v.frameRate.toFixed(3)}) : undefined;
  const facts = [v?.width && v.height ? `${v.width}×${v.height}` : undefined, file.size ? formatBytes(file.size) : undefined, formatBitRate(file.bitRate) || undefined, file.duration ? formatDuration(file.duration) : undefined, fps, v?.bitDepth && v.bitDepth > 8 ? t('mediaInfo.bitDepth', {bits: v.bitDepth}) : undefined].filter((x): x is string => !!x);
  return Object.freeze({
    id: file.id, summary, facts,
    audio: file.audio.map((track, i) => trackLine(track, 'audio', i + 1, t, languageName)),
    subtitles: file.subtitles.map((track, i) => trackLine(track, 'subtitle', i + 1, t, languageName)),
    ...(file.path ? {path: file.path} : {}),
    available: file.available,
  });
}

/** What tells one version from another when their headline reads the same. */
export type VersionFacts = Readonly<{edition?: string; bitRate?: number; size?: number}>;

/**
 * The names of a title's versions as the Version control lists them. A version is its headline
 * ("1080p · H.264 · MKV") and its edition when it has one. Versions that would still read the same
 * each gain the first fact that tells them all apart: the bit rate, else the file size, else their
 * number in the title's Files list ("Version 2"). No two versions ever read alike.
 */
export function versionNames(versions: readonly Readonly<{id: string; label: string}>[], facts: Readonly<Record<string, VersionFacts>>, t: Translate): readonly Readonly<{id: string; label: string}>[] {
  const base = versions.map(v => [v.label, facts[v.id]?.edition].filter(Boolean).join(' · '));
  return versions.map((version, index) => {
    const same = versions.map((_, i) => i).filter(i => base[i] === base[index]);
    if (same.length < 2) return {id: version.id, label: base[index]!};
    const told = (words: (id: string) => string) => { const said = same.map(i => words(versions[i]!.id)); return said.every(Boolean) && new Set(said).size === said.length ? words(version.id) : ''; };
    const extra = told(id => formatBitRate(facts[id]?.bitRate)) || told(id => (facts[id]?.size ? formatBytes(facts[id]!.size!) : '')) || t('mediaInfo.version', {number: index + 1});
    return {id: version.id, label: [base[index], extra].filter(Boolean).join(' · ')};
  });
}

/** A version as the playback options describe it (`MediaVersion`): only what its name is made of. */
export type NamedMediaVersion = Readonly<{id: string; label: string; edition?: string; container?: string; sizeBytes?: number; bitrateKbps?: number; video: readonly Readonly<{height?: number}>[]}>;

/**
 * The names of a title's versions from its playback options, as the Version menus on phones,
 * tablets and televisions list them: the server's label, the picture's height and the container,
 * then the edition, with `versionNames`' rule for versions that would still read the same (bit
 * rate, else size, else "Version 2"). A version with nothing to say is "Version 1".
 */
export function mediaVersionNames(versions: readonly NamedMediaVersion[], t: Translate): ReadonlyMap<string, string> {
  // The server's label already names the resolution and container ("720p · MKV"): a fact it
  // states is not said twice, and the picture height goes before the container.
  const headline = (v: NamedMediaVersion) => {
    const parts = v.label.split(' · ').map(part => part.trim()).filter(Boolean);
    const has = (word: string) => parts.some(part => part.toLowerCase() === word.toLowerCase());
    const container = v.container?.toUpperCase() ?? '';
    const height = v.video[0]?.height ? t('quality.height', {height: v.video[0].height}) : '';
    if (height && !has(height)) {
      const at = container ? parts.findIndex(part => part.toLowerCase() === container.toLowerCase()) : -1;
      parts.splice(at < 0 ? parts.length : at, 0, height);
    }
    if (container && !has(container)) parts.push(container);
    return parts.join(' · ');
  };
  const facts: Record<string, VersionFacts> = {};
  for (const v of versions) facts[v.id] = {...(v.edition && v.edition !== v.label ? {edition: v.edition} : {}), ...(v.bitrateKbps ? {bitRate: v.bitrateKbps * 1000} : {}), ...(v.sizeBytes ? {size: v.sizeBytes} : {})};
  const named = versionNames(versions.map(v => ({id: v.id, label: headline(v)})), facts, t);
  return new Map(named.map((x, index) => [x.id, x.label || t('mediaInfo.version', {number: index + 1})]));
}

/** A subtitle track as the menus need it named. */
export type NamedSubtitleTrack = Readonly<{id: string; language?: string; title?: string; format?: string; forced?: boolean; hearingImpaired?: boolean}>;

/**
 * The names of a title's subtitle tracks as a menu lists them: the language and what the track
 * says of itself (`trackName`), then "Forced" or "SDH" when the track is marked so and its title
 * did not already say it. Tracks that would still read the same (two unnamed English files) each
 * gain their format ("SRT", "ASS", "PGS"), and where that is the same too, a number. No two
 * tracks ever read alike, so a choice is never a guess.
 */
export function subtitleTrackNames(tracks: readonly NamedSubtitleTrack[], t: Translate, locale?: string): ReadonlyMap<string, string> {
  const said = (name: string, word: string) => name.toLowerCase().includes(word.toLowerCase());
  const base = tracks.map((track, index) => {
    const name = trackName(track, locale) || (track.title ?? '').trim() || t('mediaInfo.track', {number: index + 1});
    const forced = track.forced && !said(name, 'forced') ? t('player.opt.forced') : '';
    const sdh = track.hearingImpaired && !/\b(sdh|cc|hearing)\b/i.test(name) ? 'SDH' : '';
    return [name, forced, sdh].filter(Boolean).join(' · ');
  });
  const withFormat = base.map((name, index) => (base.filter(other => other === name).length > 1 && tracks[index]!.format ? `${name} · ${codecLabel(tracks[index]!.format!)}` : name));
  const seen = new Map<string, number>();
  const names = new Map<string, string>();
  withFormat.forEach((name, index) => {
    const repeats = withFormat.filter(other => other === name).length > 1;
    const number = (seen.get(name) ?? 0) + 1;
    seen.set(name, number);
    names.set(tracks[index]!.id, repeats ? `${name} (${number})` : name);
  });
  return names;
}
