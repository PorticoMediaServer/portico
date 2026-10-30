import type {shared} from '../../../../i18n/src/catalog/en-US/next/shared.ts';

export type KitMessageId = keyof typeof shared | 'filter.preset.lastDays' | 'filter.preset.lastYear' | 'next.search.resume' | 'music.radioNamed' | 'music.radio';
type Arguments = {
  'music.radioNamed': {name: string};
  'filter.preset.lastDays': {count: number};
  'next.search.resume': {episode: string; remaining: string};
  'next.kit.episode': {number: number}; 'next.kit.episodeCode': {season: number; episode: number};
  'next.kit.season': {number: number}; 'next.kit.seasons': {count: number};
  'next.kit.songs': {count: number}; 'next.kit.albums': {count: number}; 'next.kit.books': {count: number}; 'next.kit.items': {count: number}; 'next.kit.itemsAtLeast': {count: number};
  'next.kit.chapter': {number: number}; 'next.kit.chapters': {count: number}; 'next.kit.chapterOf': {number: number; total: number}; 'next.kit.readBy': {name: string};
  'next.kit.duration.hoursMinutes': {hours: number; minutes: number}; 'next.kit.duration.hours': {hours: number};
  'next.kit.duration.minutes': {minutes: number}; 'next.kit.duration.seconds': {seconds: number};
  'next.kit.remaining': {time: string}; 'next.kit.range': {start: string; end: string}; 'next.kit.started': {time: string};
  'next.kit.genres.append': {list: string; genre: string};
  'next.kit.collection.filmsOnServer': {count: number}; 'next.kit.collection.filmsFromTo': {count: number; year: number; title: string};
  'next.kit.filter.decade': {year: string}; 'next.kit.track': {number: number}; 'next.kit.version': {number: number}; 'next.kit.ordinal': {number: number};
  'next.kit.surround': {layout: string}; 'next.kit.channels': {count: number}; 'next.kit.height': {height: number};
  'next.kit.bytes': {count: number}; 'next.kit.kib': {value: string}; 'next.kit.mib': {value: string}; 'next.kit.gib': {value: string}; 'next.kit.tib': {value: string};
  'next.kit.mbps': {rate: string}; 'next.kit.kbps': {rate: string}; 'next.kit.fps': {rate: string}; 'next.kit.bitDepth': {bits: number};
  'next.kit.dimensions': {width: number; height: number}; 'next.kit.toneMapped': {format: string};
  'next.kit.droppedOf': {dropped: number; total: number}; 'next.kit.bufferSeconds': {count: number}; 'next.kit.reason.chosen': {quality: string};
  'next.kit.quality.autoNow': {quality: string}; 'next.kit.quality.adjusts': {quality: string}; 'next.kit.quality.home': {quality: string}; 'next.kit.quality.away': {quality: string};
  'next.kit.session.onDevice': {device: string}; 'next.kit.session.position': {position: string};
  'next.kit.session.positionOf': {position: string; duration: string}; 'next.kit.session.started': {time: string};
};
export type KitMessageValues<K extends KitMessageId> = K extends keyof Arguments ? Arguments[K] : Record<string, never>;
export type KitTranslate = <K extends KitMessageId>(id: K, ...args: K extends keyof Arguments ? [values: KitMessageValues<K>] : [values?: KitMessageValues<K>]) => string;

/** Roles describe meaning, never type weight, color, CSS, or native presentation. */
export type FactRole = 'identity' | 'context' | 'value' | 'supplement' | 'status' | 'reason';
export type FactPart = Readonly<{id: string; text: string; role: FactRole}>;
export type TextEnvironment = Readonly<{t: KitTranslate; locale?: string}>;

export const positive = (n: number | undefined): n is number => n !== undefined && Number.isFinite(n) && n > 0;
export const nonnegative = (n: number | undefined): n is number => n !== undefined && Number.isFinite(n) && n >= 0;
export const integer = (n: number | undefined): n is number => positive(n) && Number.isInteger(n);
export const supplied = (text: string | undefined): text is string => !!text?.trim();
export const part = (id: string, text: string, role: FactRole = 'value'): FactPart => Object.freeze({id, text, role});
export const compact = (parts: readonly (FactPart | undefined)[]): readonly FactPart[] => Object.freeze(parts.filter((x): x is FactPart => x !== undefined));

export function durationText(seconds: number | undefined, t: KitTranslate): string | undefined {
  if (!positive(seconds)) return undefined;
  if (seconds < 60) return t('next.kit.duration.seconds', {seconds: Math.max(1, Math.round(seconds))});
  const total = Math.round(seconds / 60), hours = Math.floor(total / 60), minutes = total % 60;
  return hours ? minutes ? t('next.kit.duration.hoursMinutes', {hours, minutes}) : t('next.kit.duration.hours', {hours}) : t('next.kit.duration.minutes', {minutes});
}

/** Missing and invalid position/length stay unknown; an overshoot is bounded, never exaggerated. */
export function timelineFacts(duration: number | undefined, position: number | undefined): Readonly<{progress?: number; remainingSeconds?: number}> {
  if (!positive(duration) || !nonnegative(position)) return Object.freeze({});
  return Object.freeze({progress: Math.min(1, position / duration), remainingSeconds: Math.max(0, duration - position)});
}

export function clockText(seconds: number | undefined): string | undefined {
  if (!nonnegative(seconds)) return undefined;
  const total = Math.floor(seconds), h = Math.floor(total / 3600), m = Math.floor(total % 3600 / 60), s = total % 60;
  return h ? `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}` : `${m}:${String(s).padStart(2, '0')}`;
}
