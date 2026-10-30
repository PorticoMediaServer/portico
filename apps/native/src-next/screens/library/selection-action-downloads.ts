import {listSelectionActionPreparations,
  type SelectionActionsApi} from '../../../../../packages/client-core/src/selection-actions';
import type {DownloadPreparation} from '../../../../../packages/client-core/src/downloads';

/** Local device intent for one explicitly confirmed command, never a selector or
 * a cached query result. The native queue persists this beside its exact wants. */
export type NativeSelectionActionDownloadIntent = Readonly<{operationId: string; deviceId: string; cursor: string; terminal?: boolean}>;
export type NativeSelectionActionDownloadQueue = Readonly<{
  /** Persist before the first confirmation transport, including ambiguous sends.
   * Replaying the same operation is idempotent; this must never accept a new id. */
  follow: (operationId: string, deviceId: string) => Promise<void>;
}>;
export type NativeSelectionActionDownloadAdmission = Readonly<{
  intent: NativeSelectionActionDownloadIntent;
  items: readonly DownloadPreparation[];
  nextCursor: string;
  complete: boolean;
}>;
export type NativeSelectionActionDownloadStep = Readonly<{
  api: SelectionActionsApi;
  intent: NativeSelectionActionDownloadIntent;
  signal: AbortSignal;
  isCurrent: () => boolean;
  /** Free slots in the real durable native queue, not filesystem capacity. */
  room: number;
  /** Persist the actual scoped inventory closure proof before admitting wants. */
  markTerminal?: (intent: NativeSelectionActionDownloadIntent) => Promise<void>;
  /** Atomically persist exact preparation wants and this intent's next cursor.
   * An EOF admission removes only this intent. A failed write rejects. */
  admit: (page: NativeSelectionActionDownloadAdmission) => Promise<void>;
}>;
const cancelled = () => Object.assign(new Error('native_selection_action_retired'), {name: 'AbortError', code: 'cancelled'});
const invalid = () => Object.assign(new Error('native_selection_action_inventory_invalid'), {code: 'invalid_response'});
const validId = (value: string) => /^[A-Za-z0-9_-]{1,160}$/.test(value);

/** At most one <=20-preparation page per native collect pass. The scoped
 * inventory's closure proof outlives the short command status summary.
 * Transfer completion remains the native grant/verification lifecycle's fact. */
export async function stepNativeSelectionActionDownloads(options: NativeSelectionActionDownloadStep): Promise<'blocked' | 'waiting' | 'more' | 'complete'> {
  const {signal, isCurrent, api, room, admit, markTerminal} = options;
  const intent = Object.freeze({...options.intent});
  const check = () => {if (signal.aborted || !isCurrent()) throw cancelled();};
  check();
  if (!validId(intent.operationId) || !validId(intent.deviceId) || typeof intent.cursor !== 'string' || intent.cursor.length > 4096
    || !Number.isSafeInteger(room) || room < 0) throw invalid();
  if (!room) return 'blocked';
  const limit = Math.min(20, room);
  const page = await listSelectionActionPreparations(api, intent.operationId, intent.deviceId, {cursor: intent.cursor, limit, signal}); check();
  if (typeof page.closed !== 'boolean') throw invalid();
  if (!page.closed) return 'waiting';
  if (!intent.terminal) {await markTerminal?.(Object.freeze({...intent, terminal: true})); check();}
  if (page.items.length > limit || page.nextCursor && page.nextCursor === intent.cursor) throw invalid();
  const complete = !page.nextCursor;
  await admit(Object.freeze({intent: Object.freeze({...intent, terminal: true}), items: page.items, nextCursor: page.nextCursor, complete})); check();
  return complete ? 'complete' : 'more';
}
