import {castControllerStamp,castReceiverOccurrence,castRemoteIdentity,type CastControllerStamp,type CastReceiverOccurrence,type CastRemoteIdentity} from './remote-identity.ts';
import {castBinding,type CastControllerBinding} from './controller-proof.ts';
import {apiRoot} from "../../../contracts/src/routes.ts";
import {artworkRenditionFor} from '../artwork/renditions.ts';
/**
 * The Portico Cast channel (`urn:x-cast:tv.getportico.cast`), shared by every sender. The receiver
 * is `server/receiver/cast/receiver.js`.
 *
 * The television never receives the sender's sign-in or a media address: it gets a short code the
 * server issued (`POST /v1/cast/bootstrap`), redeems it for its own credential against `origin`,
 * and is then told only which item to play.
 */
export const CAST_NAMESPACE = 'urn:x-cast:tv.getportico.cast';
export const CAST_PROTOCOL_VERSION = '1.0';
/** The TV asks the person watching before a second sender takes over; allow for their answer. */
export const CAST_PAIR_TIMEOUT_MS = 35_000;
/** The TV treats 30 s without an answer to a takeover as no. */
export const CAST_TAKEOVER_WINDOW_MS = 30_000;

/** What the TV shows while it prepares, and what Cast's own player labels the title with. */
export type CastMetadata = Readonly<{title: string; subtitle?: string; seriesTitle?: string; season?: number; episode?: number; kind?: string; artworkPath?: string}>;

export type CastTrack = Readonly<{id: number; language: string; name: string}>;

/**
 * The viewer's choices carried to the TV with `load`: the audio and subtitle tracks chosen on the sender
 * (`null` for off). The TV starts its own session with it, and the server delivers that track as
 * the TV can show it: text, or burned into the picture for an image or styled track the TV cannot
 * draw itself. Absent: the profile's usual choice.
 */
export type CastQueueRef = Readonly<{queueId: string; entryId: string; transferId?: string}>;
/** Sent only to the authenticated bootstrap endpoint, never to the receiver. */
export type CastQueueSource = Readonly<{sessionId: string; generation: number; queueId: string; entryId: string; queueRevision: string}>;
export type CastChoices = Readonly<{versionId?: string; audioTrackId?: string; subtitleTrackId?: string | null; queue?: CastQueueRef; source?: CastQueueSource}>;
const trackIdPattern = /^[A-Za-z0-9._:-]{1,128}$/;
/** `load` fields for `choices`: `subtitleTrackId` is a track id, or "off". */
export function castChoiceFields(choices: CastChoices | undefined): Record<string, string | CastQueueRef> {
  const fields: Record<string, string | CastQueueRef> = {};
  if (typeof choices?.versionId === 'string' && trackIdPattern.test(choices.versionId)) fields.versionId = choices.versionId;
  if (typeof choices?.audioTrackId === 'string' && trackIdPattern.test(choices.audioTrackId)) fields.audioTrackId = choices.audioTrackId;
  if (choices?.subtitleTrackId === null) fields.subtitleTrackId = 'off';
  else if (typeof choices?.subtitleTrackId === 'string' && trackIdPattern.test(choices.subtitleTrackId)) fields.subtitleTrackId = choices.subtitleTrackId;
  if (choices?.queue && typeof choices.queue.queueId === 'string' && typeof choices.queue.entryId === 'string' && trackIdPattern.test(choices.queue.queueId) && trackIdPattern.test(choices.queue.entryId)) fields.queue = {queueId: choices.queue.queueId, entryId: choices.queue.entryId,...(choices.queue.transferId&&trackIdPattern.test(choices.queue.transferId)?{transferId:choices.queue.transferId}:{})};
  return fields;
}

/** Sender → receiver. */
export type CastCommand =
  | {type: 'pair'; code: string; origin: string; displayName: string}
  | ({type: 'load'; itemId: string; startSeconds: number;controller?:CastControllerStamp} & Record<string, string | number | CastQueueRef | CastControllerStamp | undefined>)
  | {type:'adopt'|'renewal-ack';bindingId:string;attemptId:string;ticket:string;controlToken:string}
  | {type:'status';controller?:CastControllerStamp}
  | (({type:'play'}|{type:'pause'}|{type:'stop'}|{type:'seek';positionSeconds:number}|{type:'audio';trackId:number}|{type:'subtitles';trackIds:number[]}) & {target?:CastRemoteIdentity;requestId?:string})
  | {type:'takeover';allow:boolean;controller?:CastControllerStamp;requestId?:string};

/** Receiver → sender, validated. Unknown or malformed messages are dropped. */
export type CastEvent =
  | {type: 'paired';controlToken?:string;binding?:CastControllerBinding;controller?:CastControllerStamp}
  | {type:'renewal-offer';attemptId:string;binding:CastControllerBinding;previousController:CastControllerStamp;controller:CastControllerStamp}
  | {type:'renewal-complete';attemptId:string;controller:CastControllerStamp}
  | {type: 'pair-pending'}
  | {type: 'pair-failed'; code: string}
  | {type: 'busy'}
  | {type: 'pair-required'}
  | {type: 'load-failed'; requestId?: string}
  | {type: 'loaded'; requestId: string; itemId: string}
  /** Original valid-202 observation only; no playback/credential authority. */
  | {type:'load-pending';requestId:string;itemId:string;startId:string;sequence:number;controller:CastControllerStamp}
  | {type:'takeover-requested';requester:string;controller?:CastControllerStamp;requestId?:string}
  | {type: 'replaced'}
  | {type:'stopped';requestId:string;target:CastRemoteIdentity;positionSeconds?:number;durationSeconds?:number}
  | {type:'control-failed';requestId:string;target:CastRemoteIdentity}
  | {type: 'status'; controller?:CastControllerStamp;occurrence?:CastReceiverOccurrence;clockKnown?:true;attemptId?:string;versionId?:string;itemId: string; positionSeconds: number; durationSeconds: number; paused: boolean; audioTracks: readonly CastTrack[]; textTracks: readonly CastTrack[]};

const finite = (v: unknown) => (typeof v === 'number' && Number.isFinite(v) && v >= 0 ? v : 0);
const short = (v: unknown, max: number) => (typeof v === 'string' ? v.trim().slice(0, max) : '');
function tracks(v: unknown): readonly CastTrack[] {
  if (!Array.isArray(v)) return [];
  return Object.freeze(v.slice(0, 32).flatMap(t => (t && typeof t === 'object' && Number.isSafeInteger((t as {id?: unknown}).id) ? [Object.freeze({id: (t as {id: number}).id, language: short((t as {language?: unknown}).language, 35), name: short((t as {name?: unknown}).name, 100)})] : [])));
}

/** Parse one receiver message (a JSON string, or an object on SDKs that decode it). */
export function parseCastEvent(raw: unknown): CastEvent | undefined {
  let m: unknown = raw;
  if (typeof raw === 'string') { if (raw.length > 65536) return undefined; try { m = JSON.parse(raw); } catch { return undefined; } }
  if (!m || typeof m !== 'object') return undefined;
  const o = m as Record<string, unknown>;
  switch (o.type) {
    case 'renewal-offer': {
      const binding=castBinding(o.binding),controller=castControllerStamp(o.controller),previousController=castControllerStamp(o.previousController);
      if(!binding||!controller||!previousController||typeof o.attemptId!=='string'||!trackIdPattern.test(o.attemptId))return;
      return {type:'renewal-offer',attemptId:o.attemptId,binding,previousController,controller};
    }
    case 'renewal-complete': {
      const controller=castControllerStamp(o.controller);
      if(!controller||typeof o.attemptId!=='string'||!trackIdPattern.test(o.attemptId))return;
      return {type:'renewal-complete',attemptId:o.attemptId,controller};
    }
    case 'paired': return {type:'paired',...(castControllerStamp(o.controller)?{controller:castControllerStamp(o.controller)}:{}),...(castBinding(o.binding)?{binding:castBinding(o.binding)}:{}),...(typeof o.controlToken==='string'&&/^[a-f0-9]{48}$/.test(o.controlToken)?{controlToken:o.controlToken}:{})};
    case 'load-failed': return {type: 'load-failed', ...(typeof o.requestId === 'string' && trackIdPattern.test(o.requestId) ? {requestId: o.requestId} : {})};
    case 'load-pending': {
      const controller=castControllerStamp(o.controller);
      if(!controller||typeof o.requestId!=='string'||!trackIdPattern.test(o.requestId)||typeof o.itemId!=='string'||!trackIdPattern.test(o.itemId)||typeof o.startId!=='string'||!trackIdPattern.test(o.startId)||!Number.isSafeInteger(o.sequence)||(o.sequence as number)<1)return;
      return {type:'load-pending',requestId:o.requestId,itemId:o.itemId,startId:o.startId,sequence:o.sequence as number,controller};
    }
    case 'loaded': return typeof o.requestId === 'string' && trackIdPattern.test(o.requestId) && typeof o.itemId === 'string' && trackIdPattern.test(o.itemId) ? {type: 'loaded', requestId: o.requestId, itemId: o.itemId} : undefined;
    case 'pair-pending': case 'busy': case 'pair-required': case 'replaced':
      return {type: o.type};
    case 'pair-failed': return {type: 'pair-failed', code: short(o.code, 64) || 'pair_failed'};
    case 'takeover-requested': return {type:'takeover-requested',requester:short(o.requester,64),...(castControllerStamp(o.controller)?{controller:castControllerStamp(o.controller)}:{}),...(typeof o.requestId==='string'&&trackIdPattern.test(o.requestId)?{requestId:o.requestId}:{})};
    case 'stopped': case 'control-failed': {
      const target=castRemoteIdentity(o.target);if(!target||typeof o.requestId!=='string'||!trackIdPattern.test(o.requestId))return;
      return {type:o.type,requestId:o.requestId,target,...(o.type==='stopped'&&typeof o.positionSeconds==='number'&&Number.isFinite(o.positionSeconds)&&o.positionSeconds>=0?{positionSeconds:o.positionSeconds}:{}),...(o.type==='stopped'&&typeof o.durationSeconds==='number'&&Number.isFinite(o.durationSeconds)&&o.durationSeconds>=0?{durationSeconds:o.durationSeconds}:{})};
    }
    case 'status': {
      const controller=castControllerStamp(o.controller),occurrence=castReceiverOccurrence(o.occurrence),itemId=short(o.itemId,128);
      if(o.controller!==undefined&&!controller||o.occurrence!==undefined&&(!occurrence||!controller||occurrence.itemId!==itemId))return;
      // The receiver's known clock fields are genuine numbers or absent; paused is
      // a boolean or absent. A malformed field drops the whole message rather than
      // turning it into a zero/false fact.
      const clock=(v:unknown)=>v===undefined||typeof v==='number'&&Number.isFinite(v)&&v>=0;
      if(typeof o.itemId!=='string'||!clock(o.positionSeconds)||!clock(o.durationSeconds)||o.paused!==undefined&&typeof o.paused!=='boolean')return;
      return {type:'status',...(typeof o.versionId==='string'&&trackIdPattern.test(o.versionId)?{versionId:o.versionId}:{}),...(controller?{controller}:{}),...(occurrence?{occurrence}:{}),...(typeof o.positionSeconds==='number'&&Number.isFinite(o.positionSeconds)&&o.positionSeconds>=0&&typeof o.durationSeconds==='number'&&Number.isFinite(o.durationSeconds)&&o.durationSeconds>=0?{clockKnown:true as const}:{}),...(typeof o.attemptId==='string'&&trackIdPattern.test(o.attemptId)?{attemptId:o.attemptId}:{}),itemId,positionSeconds:finite(o.positionSeconds),durationSeconds:finite(o.durationSeconds),paused:o.paused===true,audioTracks:tracks(o.audioTracks),textTracks:tracks(o.textTracks)};
    }
    default: return undefined;
  }
}

/** The receiver paints cover art into a 560 px square at 1080p (cast-receiver frames). */
const castArtworkBox = Object.freeze({width: 560, height: 560, dpr: 1, fit: 'cover' as const});

/** Display fields for `load`: only what's present, bounded, integers for season and episode.
 * Artwork names the one slot the receiver's cover box needs. */
export function castMetadataFields(meta: CastMetadata): Record<string, string | number> {
  const out: Record<string, string | number> = {title: meta.title.slice(0, 200)};
  if (meta.subtitle) out.subtitle = meta.subtitle.slice(0, 200);
  if (meta.seriesTitle) out.seriesTitle = meta.seriesTitle.slice(0, 200);
  if (Number.isInteger(meta.season)) out.season = meta.season!;
  if (Number.isInteger(meta.episode)) out.episode = meta.episode!;
  if (meta.kind) out.kind = meta.kind.slice(0, 32);
  if (meta.artworkPath && meta.artworkPath.startsWith(apiRoot)) out.artworkPath = artworkRenditionFor(meta.artworkPath, castArtworkBox).url.slice(0, 512);
  return out;
}

/** Exact capture for one source attempt; malformed queue intent never degrades to an item load. */
export function captureCastChoices(value: CastChoices | undefined): CastChoices | undefined {
 if (!value) return undefined;
 const queue=value.queue;
 if(queue&&(!trackIdPattern.test(queue.queueId)||!trackIdPattern.test(queue.entryId)))throw new Error('invalid_cast_queue');
 const source=value.source;
 if(source&&(!queue||!trackIdPattern.test(source.sessionId)||!Number.isSafeInteger(source.generation)||source.generation<1||source.queueId!==queue.queueId||source.entryId!==queue.entryId||!/^(?:[0-9]+|"[0-9]+")$/.test(source.queueRevision)))throw new Error('invalid_cast_source');
 const {versionId,...rest}=value;
 return Object.freeze({...rest,...(typeof versionId==='string'&&trackIdPattern.test(versionId)?{versionId}:{}),...(queue?{queue:Object.freeze({queueId:queue.queueId,entryId:queue.entryId})}:{}),...(source?{source:Object.freeze({...source})}:{})});
}
export type CastTransferReceipt=Readonly<{id:string;state:'pending'|'bound'|'cancelled'|'committed';itemId?:string;playbackState?:'preparing'|'playing'|'paused'|'ended'}>;
export function castTransferReceipt(raw:unknown,id:string):CastTransferReceipt|undefined{
 if(!raw||typeof raw!=='object')return undefined;
 const bootstrap=(raw as {bootstrap?:unknown}).bootstrap;if(!bootstrap||typeof bootstrap!=='object')return undefined;
 const transfer=(bootstrap as {transfer?:unknown}).transfer;if(!transfer||typeof transfer!=='object')return undefined;
 const t=transfer as Record<string,unknown>;
 if(t.id!==id||!['pending','bound','cancelled','committed'].includes(t.state as string))return undefined;
 if(t.state==='committed'&&(typeof t.itemId!=='string'||!trackIdPattern.test(t.itemId)||!['preparing','playing','paused','ended'].includes(t.playbackState as string)))return undefined;
 return Object.freeze({id,state:t.state as CastTransferReceipt['state'],...(t.state==='committed'?{itemId:t.itemId as string,playbackState:t.playbackState as CastTransferReceipt['playbackState']}:{})});
}
