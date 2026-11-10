/**
 * The server's change feed (`GET /v1/events`) read into the events the app already acts on.
 *
 * The server says "this changed, read it again" and never carries the data: a `change` names a
 * topic, what happened and where. `ready` only says where the feed starts, and `resync` means
 * "read again everything you show". Every event has an id, which is the place to reconnect from.
 * A topic or event this app does not know moves the place on and changes nothing.
 */
import type {ServerEvent} from './playback-v1/types.ts';

type Frame = Readonly<{id?: string; event?: string; data: string}>;
/** The data of a `change`: what to read again. The feed is a stream, so the generated route table has no shape for it. */
type Change = Readonly<{topic?: unknown; op?: unknown; id?: unknown; libraryId?: unknown}>;
/** A profile's access changed (a library taken away, a rating limit set): everything read for it is thrown away. */
export const accessChangedEvent = 'access.changed';

export function serverEventsFrom(frame: Frame): readonly ServerEvent[] {
  const id = frame.id;
  if (!id || id.length > 128) return [];
  if (frame.event === 'resync') return [{id, type: 'stream.resync'}];
  if (frame.event !== 'change') return [{id, type: 'stream.' + (frame.event || 'message')}];
  let change: Change;
  try { change = JSON.parse(frame.data) as Change; } catch { return [{id, type: 'stream.unread'}]; }
  const library = typeof change.libraryId === 'string' && change.libraryId ? {kind: 'library', id: change.libraryId} : undefined;
  // Two things can move with one change. The last carries the feed's own id, so that is where the feed stands afterwards.
  const pair = (first: string, second: string, resource: ServerEvent['resource']): ServerEvent[] => [{id: id + ':' + first, type: first, resource}, {id, type: second, resource}];
  switch (change.topic) {
    // Someone's own state on a title: watched, favourite, My List, rating, or where they stopped.
    case 'state': return library ? pair('progress.changed', 'personal.changed', library) : [{id, type: 'stream.resync'}];
    case 'item': return library ? [{id, type: 'library.changed', resource: library}] : [{id, type: 'stream.resync'}];
    case 'library': return library && change.op === 'updated' ? pair('libraries.changed', 'library.changed', library) : [{id, type: 'libraries.changed', resource: {kind: 'libraries', id: 'directory'}}];
    case 'access': return [{id, type: accessChangedEvent}];
    case 'person': case 'list': return [{id, type: change.topic + '.changed', ...(typeof change.id === 'string' && change.id ? {resource: {kind: change.topic, id: change.id}} : {})}];
    default: return [{id, type: 'stream.unknown'}];
  }
}
