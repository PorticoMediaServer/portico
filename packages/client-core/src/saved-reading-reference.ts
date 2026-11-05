import {readingSavedReferencePath, validReadingSavedReferenceCommand, parseReadingSavedReferenceReceipt, type ReadingSavedReferenceCommand} from './reading-saved-group.ts';
import type {SavedApi} from './saved.ts';
/** One saved reading-group removal, under its captured revision and stable intent ID. */
export async function removeSavedReadingGroup(api: SavedApi, serverId: string, referenceId: string, command: ReadingSavedReferenceCommand,
  signal: AbortSignal, assertCurrent: () => void) {
  const path = readingSavedReferencePath(referenceId), captured = Object.freeze({...command});
  if (!path || !validReadingSavedReferenceCommand(captured)) throw new Error('invalid_reading_saved_group');
  const value = await api.request<unknown>(path, 'DELETE', captured, signal);
  signal.throwIfAborted(); assertCurrent();
  return parseReadingSavedReferenceReceipt(value, serverId, referenceId, captured);
}
