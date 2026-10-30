import {routeBody} from '../../../contracts/src/routes.ts';
import {routePath} from '../../../contracts/src/route-paths.ts';
import type {LibraryContentApi} from '../library-content.ts';

export type RecordingRootStatus = Readonly<{revision: number; requested: string; effective: string; pendingRestart: boolean; operatorControlled: boolean}>;
export type RecordingRootIntent = Readonly<{expectedRevision: number; operationId: string; path: string}>;
const invalid = () => {throw {code: 'invalid_response'};};
const path = (v: unknown): v is string => typeof v === 'string' && new TextEncoder().encode(v).length <= 1024 && (v === '' || /^(?:\/|[A-Za-z]:[\\/]|\\\\)/.test(v) && !/[\u0000-\u001f\u007f-\u009f\u202a-\u202e\u2066-\u2069]/.test(v));
export function parseRecordingRoot(raw: unknown): RecordingRootStatus {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return invalid();
  const v = raw as Record<string, unknown>;
  if (typeof v.revision !== 'number' || !Number.isSafeInteger(v.revision) || v.revision < 1 ||
    !path(v.requested) ||
    !path(v.effective) ||
    typeof v.pendingRestart !== 'boolean' || typeof v.operatorControlled !== 'boolean' ||
    v.pendingRestart !== (!!v.requested && v.requested !== v.effective)) return invalid();
  return Object.freeze({revision: v.revision, requested: v.requested, effective: v.effective, pendingRestart: v.pendingRestart, operatorControlled: v.operatorControlled});
}
export async function readRecordingRoot(api: LibraryContentApi, signal: AbortSignal) {
  return parseRecordingRoot(await api.request(routePath('get_admin_dvr_recording_root'), 'GET', undefined, signal));
}
export async function saveRecordingRoot(api: LibraryContentApi, intent: RecordingRootIntent, signal: AbortSignal) {
  const saved = parseRecordingRoot(await api.request(routePath('patch_admin_dvr_recording_root'), 'PATCH', routeBody('patch_admin_dvr_recording_root', intent), signal));
  if (saved.requested !== intent.path || saved.revision < intent.expectedRevision) return invalid();
  return saved;
}
