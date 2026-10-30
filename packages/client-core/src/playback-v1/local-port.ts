import {localPlaybackScope, sameLocalScope, type LocalPlaybackScope} from '../../../player-core/src/index.ts';

/** Opaque app preparation member. Not a URL PlayerSource: only an acknowledged
 * optional loadLocal can adopt it, never the legacy MediaPlayer.load path. */
export type LocalPlayerSource = Readonly<{
  kind: 'local'; scope: LocalPlaybackScope; preparationId: string; localSourceId: string;
  entryId: string; itemId: string; generation: number; startPositionMs: number; autoplay: boolean;
}>;
export type LocalPlayerLoadContext = Readonly<{signal: AbortSignal; isCurrent(): boolean}>;
/** Facts after the native owner committed its verified load and distinct journal.
 * The adapter echoes public generation; it is never the native private load id. */
export type LocalPlayerLoadReceipt = Readonly<{
  kind: 'local'; scope: LocalPlaybackScope; preparationId: string; localSourceId: string; generation: number;
  sessionId: string; playId: string; itemId: string; entryId: string; artifact: Readonly<{sha256: string; bytes: number}>;
}>;
const object = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value);
const id = (value: unknown): value is string => typeof value === 'string' && /^[A-Za-z0-9_.:-]{1,128}$/.test(value);
const positive = (value: unknown): value is number => typeof value === 'number' && Number.isSafeInteger(value) && value > 0;
function invalid(): never {throw new Error('invalid_local_playback_source');}
function only(value: Record<string, unknown>, allowed: readonly string[]) {if (Object.keys(value).some(key => !allowed.includes(key))) invalid();}
const common = ['kind', 'scope', 'preparationId', 'localSourceId', 'entryId', 'itemId', 'generation'];

export function localPlayerSource(value: LocalPlayerSource): LocalPlayerSource {
  if (!object(value) || value.kind !== 'local' || ![value.preparationId, value.localSourceId, value.entryId, value.itemId].every(id) || !positive(value.generation)
    || !Number.isSafeInteger(value.startPositionMs) || value.startPositionMs < 0 || typeof value.autoplay !== 'boolean') invalid();
  only(value, [...common, 'startPositionMs', 'autoplay']);
  return Object.freeze({kind: 'local', scope: localPlaybackScope(value.scope), preparationId: value.preparationId, localSourceId: value.localSourceId,
    entryId: value.entryId, itemId: value.itemId, generation: value.generation, startPositionMs: value.startPositionMs, autoplay: value.autoplay});
}

/** Match the original load including full scope. No facts are filled from display metadata. */
export function localPlayerLoadReceipt(value: LocalPlayerLoadReceipt, source: LocalPlayerSource): LocalPlayerLoadReceipt {
  if (!object(value) || value.kind !== 'local' || !id(value.playId) || !object(value.artifact) || !positive(value.artifact.bytes)
    || typeof value.artifact.sha256 !== 'string' || !/^[a-f0-9]{64}$/.test(value.artifact.sha256)) invalid();
  only(value, [...common, 'sessionId', 'playId', 'artifact']);only(value.artifact, ['sha256', 'bytes']);
  const scope = localPlaybackScope(value.scope), suffix = typeof value.sessionId === 'string' && value.sessionId.startsWith(`local:${source.entryId}:`) ? value.sessionId.slice(`local:${source.entryId}:`.length) : '';
  if (!/^[1-9][0-9]{0,15}$/.test(suffix) || !Number.isSafeInteger(Number(suffix)) || !sameLocalScope(scope, source.scope)
    || value.preparationId !== source.preparationId || value.localSourceId !== source.localSourceId || value.entryId !== source.entryId
    || value.itemId !== source.itemId || value.generation !== source.generation) invalid();
  return Object.freeze({kind: 'local', scope, preparationId: value.preparationId, localSourceId: value.localSourceId, generation: value.generation,
    sessionId: value.sessionId, playId: value.playId, itemId: value.itemId, entryId: value.entryId, artifact: Object.freeze({sha256: value.artifact.sha256, bytes: value.artifact.bytes})});
}
