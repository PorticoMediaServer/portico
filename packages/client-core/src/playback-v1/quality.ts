/**
 * Quality (spec §5.2). The default is the original file. A viewer may instead pick a fixed
 * quality (one step of the server's ladder, a ceiling: a file already within it still plays as it
 * is) or Automatic, which starts at the original and moves down, and back up, as the connection
 * requires. The owner's ceilings, the member's caps and the upload budget always apply on top.
 *
 * A viewer has two saved defaults on the server, `quality.home` and `quality.remote` (the
 * preference registry): a start that names no quality gets the one for where it comes from, as
 * the server decides it from the owner's home networks. The player can name another quality for
 * one session, at start or with a change.
 *
 * Automatic is driven by the client: it measures its connection and names the step the session
 * should be on (`{mode: 'automatic', id}`; `id: 'original'` is back to the original), or asks the
 * server for one step from where the session is (`step: 'down' | 'up'`). Each change re-plans the
 * same session at the current position. The player-facing menu model and the measuring are in
 * `choices.ts`; this file is the server's vocabulary.
 */

import {canonicalMusicQualityLadder, canonicalQualityLadder} from '../../../contracts/src/types.ts';
export type SavedQuality = (typeof canonicalQualityLadder)[number]['id'];
export type QualityId = Exclude<SavedQuality, 'original' | 'automatic'>;
export const savedQualities: readonly SavedQuality[] = Object.freeze(canonicalQualityLadder.map(r => r.id));

/** The `quality` field of a start, a change and the options preview. */
export type QualityRequest =
  | Readonly<{mode: 'original' | 'cd-quality'}>
  /** One ladder step, by id. */
  | Readonly<{mode: 'fixed'; id: QualityId}>
  /** Automatic. `id` names the step to be on (`'original'`: none); `step` moves one step from where the session is (a change only). */
  | Readonly<{mode: 'automatic'; id?: QualityId | 'original'; step?: 'down' | 'up'}>
  /** Ceilings in numbers, for a client that has its own. */
  | Readonly<{mode: 'limit'; maxVideoBitrateKbps?: number; maxHeight?: number; maxAudioBitrateKbps?: number}>;

/** The quality in force on a presentation: never a `step`; automatic's `id` is the step it is on (absent: the original). */
export type QualityInForce = Readonly<{mode: 'original' | 'cd-quality' | 'fixed' | 'automatic' | 'limit'; id?: string; maxVideoBitrateKbps?: number; maxHeight?: number; maxAudioBitrateKbps?: number}>;

/** One quality menu entry: video uses fixed ladder IDs; music uses numeric audio limits. */
export type QualityOption = Readonly<{mode: 'original' | 'cd-quality' | 'automatic' | 'fixed' | 'limit'; id?: QualityId; maxHeight?: number; maxVideoBitrateKbps?: number; maxAudioBitrateKbps?: number; sameAsOriginal?: boolean; dataGBPerHour?: number; applicable?: boolean}>;

/** The server's ladder (`GET /v1/playback/delivery-policy` `ladder`), widest first: what each fixed quality allows. */
export const qualityLadder = Object.freeze(canonicalQualityLadder.filter(r => r.height > 0).map(r => Object.freeze({...r, id: r.id as QualityId})));

const isQualityId = (v: unknown): v is QualityId => qualityLadder.some(r => r.id === v);

/** The request a saved default (`quality.home`, `quality.remote`) stands for. Anything unknown is the original. */
export function savedQualityRequest(saved: string | undefined): QualityRequest {
  if (saved === 'automatic') return Object.freeze({mode: 'automatic'});
  if (isQualityId(saved)) return Object.freeze({mode: 'fixed', id: saved});
  return Object.freeze({mode: 'original'});
}

/** The request that asks again for the quality in force on a presentation (what the next title of a viewing starts with). */
export function requestInForce(inForce: QualityInForce): QualityRequest {
 if(inForce.mode==='cd-quality')return Object.freeze({mode:'cd-quality'});
  if (inForce.mode === 'fixed' && isQualityId(inForce.id)) return Object.freeze({mode: 'fixed', id: inForce.id});
  if (inForce.mode === 'automatic') return Object.freeze(isQualityId(inForce.id) ? {mode: 'automatic', id: inForce.id} : {mode: 'automatic'});
  if (inForce.mode === 'limit') return Object.freeze({mode: 'limit', ...(inForce.maxVideoBitrateKbps ? {maxVideoBitrateKbps: inForce.maxVideoBitrateKbps} : {}), ...(inForce.maxHeight ? {maxHeight: inForce.maxHeight} : {}), ...(inForce.maxAudioBitrateKbps ? {maxAudioBitrateKbps: inForce.maxAudioBitrateKbps} : {})});
  return Object.freeze({mode: 'original'});
}

/** The request a menu entry stands for. */
export function qualityOptionRequest(option: QualityOption): QualityRequest {
 if(option.mode==='cd-quality')return Object.freeze({mode:'cd-quality'});
  if (option.mode === 'limit') return Object.freeze({mode: 'limit', maxAudioBitrateKbps: option.maxAudioBitrateKbps});
  if (option.mode === 'fixed' && option.id) return Object.freeze({mode: 'fixed', id: option.id});
  return Object.freeze({mode: option.mode === 'automatic' ? 'automatic' : 'original'});
}

/** Whether a menu entry is the quality in force (Automatic matches whatever step it is on). */
export function qualityOptionSelected(option: QualityOption, inForce: QualityInForce | undefined): boolean {
  const mode = inForce?.mode ?? 'original';
  if (option.mode !== mode) return false;
  if (option.mode === 'limit') return option.maxAudioBitrateKbps === inForce?.maxAudioBitrateKbps && !inForce?.maxVideoBitrateKbps && !inForce?.maxHeight;
  return option.mode !== 'fixed' || option.id === inForce?.id;
}

/** Whether two requests are the same request (so a change would do nothing). */
export function sameQuality(a: QualityRequest, b: QualityRequest): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
}

/**
 * A quality change the server refused because the connection is too slow for it
 * (`connection_too_slow`), with the widest fixed quality that would fit when the server named one
 * (its error `reason`). Null for any other failure. Every player says the same thing with it.
 */
export function connectionTooSlow(error: unknown): Readonly<{fits?: (typeof qualityLadder)[number]}> | null {
  const e = error as {code?: unknown; reason?: unknown} | null;
  if (!e || typeof e !== 'object' || e.code !== 'connection_too_slow') return null;
  const fits = typeof e.reason === 'string' ? qualityLadder.find(q => q.id === e.reason) : undefined;
  return Object.freeze(fits ? {fits} : {});
}

/** Saved music qualities have their own canonical domain and use audio-only limits. */
export type SavedMusicQuality = (typeof canonicalMusicQualityLadder)[number]['id'];
export const savedMusicQualities: readonly SavedMusicQuality[] = Object.freeze(canonicalMusicQualityLadder.map(q => q.id));
export const musicQualityLadder = canonicalMusicQualityLadder;
export function savedMusicQualityRequest(saved: string | undefined): QualityRequest {
  if(saved==='256'||saved==='192'||saved==='128')return Object.freeze({mode:'limit',maxAudioBitrateKbps:Number(saved)});
  const preset = canonicalMusicQualityLadder.find(q => q.id === saved);
  if(saved==='cd-quality')return Object.freeze({mode:'cd-quality'});
  return Object.freeze(preset && preset.audioKbps > 0 ? {mode: 'limit', maxAudioBitrateKbps: preset.audioKbps} : {mode: 'original'});
}
