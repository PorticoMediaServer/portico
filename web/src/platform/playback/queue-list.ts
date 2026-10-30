import type {QueuePlaybackView} from '@core/queue-controller.ts';
import type {QueueState} from '@core/queues.ts';
import {queueVideoKind, type V1QueueEditIntent} from '@core/playback-v1/v1-queue-player.ts';
import type {QueuePlaybackFence} from '../../../../packages/player-core/src/index.ts';

export type WebQueueListEntry = Readonly<{
  entryId: string; title?: string; hidden: boolean; pending: boolean; playing: boolean;
  canPlay: boolean; canMoveUp: boolean; canMoveDown: boolean; canRemove: boolean;
}>;
export type WebQueueListSnapshot = Readonly<{
  current: boolean; fence?: QueuePlaybackFence; queueId?: string; revision?: string; currentEntryId?: string;
  entries: readonly WebQueueListEntry[]; more: number | null;
  change: 'idle' | 'sending' | 'failed'; error?: 'queue_changed' | 'queue_edit_failed';
}>;
export type WebQueueListPort = Readonly<{
  getSnapshot(): WebQueueListSnapshot; subscribe(listener: () => void): () => void; isCurrent(): boolean;
  play(snapshot: WebQueueListSnapshot, entryId: string, lifetime: WebQueueListLifetime): Promise<void>;
  move(snapshot: WebQueueListSnapshot, entryId: string, direction: -1 | 1, lifetime: WebQueueListLifetime): Promise<void>;
  remove(snapshot: WebQueueListSnapshot, entryId: string, lifetime: WebQueueListLifetime): Promise<void>;
}>;
export type WebQueueListLifetime = Readonly<{signal: AbortSignal; isCurrent(): boolean}>;
type QueueListSource = Readonly<{
  fence: QueuePlaybackFence; queueId: string; selectable: boolean; isCurrent(): boolean;
  read(): Readonly<{view: QueuePlaybackView | null; workspace: QueueState}>;
  subscribe(listener: () => void): () => void;
  play(entryId: string, revision: string, lifetime: WebQueueListLifetime): Promise<void>;
  edit(revision: string, intent: V1QueueEditIntent, lifetime: WebQueueListLifetime): Promise<void>;
}>;
const empty: WebQueueListSnapshot = Object.freeze({current: false, entries: Object.freeze([]), more: null, change: 'idle'});
const changed = () => Object.assign(new Error('queue_changed'), {code: 'queue_changed'});

/** One captured queue run. A screen must pass the immutable snapshot that
 * rendered its command; neither an entry id nor the latest player is a grant. */
export function createWebQueueListPort(source: QueueListSource): WebQueueListPort {
  let sending = false, error: WebQueueListSnapshot['error'];
  let cache: {view: QueuePlaybackView; workspace: QueueState; sending: boolean; error: WebQueueListSnapshot['error']; snapshot: WebQueueListSnapshot} | undefined;
  const listeners = new Set<() => void>();
  const notify = () => {for (const listener of [...listeners]) listener();};
  const getSnapshot = (): WebQueueListSnapshot => {
    if (!source.isCurrent()) return empty;
    const {view, workspace} = source.read(), queue = view?.queue;
    if (!view || !queue || queue.id !== source.queueId || workspace.queue?.id !== queue.id || workspace.queue.revision !== queue.revision) return empty;
    if (workspace.phase === 'denied' || workspace.phase === 'fenced' || workspace.phase === 'expired') return empty;
    if (cache?.view === view && cache.workspace === workspace && cache.sending === sending && cache.error === error) return cache.snapshot;
    const all = queue.entries.filter(entry => !entry.removed), at = all.findIndex(entry => entry.id === queue.currentEntryId);
    if (at < 0) return empty;
    const shown = all.slice(at, at + 50), items = new Map(view.items.map(item => [item.entryId, item]));
    const readable = (entry: typeof shown[number] | undefined) => !!entry && !entry.hidden && items.get(entry.id)?.itemId === entry.itemId;
    // Only the accepted wire window supplies availability and absolute positions.
    // QueueSnapshot placeholders and visible-array indexes establish neither.
    const window = view.window?.queueId === queue.id && view.window.revision === queue.revision ? view.window : undefined;
    const facts = new Map(window?.entries.map(entry => [entry.entryId, entry]));
    const supplied = !!window && facts.size === window.entries.length && window.entries.every((entry, index) =>
      typeof entry.position === 'number' && Number.isSafeInteger(entry.position) && entry.position >= 0
      && (index === 0 || entry.position > window.entries[index - 1].position!));
    const position = (entry: typeof shown[number] | undefined) => {
      return supplied && entry ? facts.get(entry.id)?.position : undefined;
    };
    const currentPosition = readable(shown[0]) && facts.get(shown[0].id)?.available === true ? position(shown[0]) : undefined;
    const upcoming = (entry: typeof shown[number] | undefined) => {
      const at = position(entry);
      return !!entry && readable(entry) && facts.get(entry.id)?.available === true
        && at !== undefined && currentPosition !== undefined && at > currentPosition;
    };
    const busy = sending || workspace.phase === 'sending';
    const entries = Object.freeze(shown.map((entry, index): WebQueueListEntry => {
      const item = items.get(entry.id), known = readable(entry);
      const playing = entry.id === queue.currentEntryId;
      const allowed = upcoming(entry) && !playing && !busy && workspace.phase === 'ready';
      return Object.freeze({entryId: entry.id, ...(known && item ? {title: item.title} : {}), hidden: !known,
        pending: entry.hidden && entry.pending === true, playing,
        canPlay: allowed && source.selectable && !!item && queueVideoKind(item.kind),
        canMoveUp: allowed && upcoming(shown[index - 1]) && position(shown[index - 1])! + 1 === position(entry),
        canMoveDown: allowed && upcoming(shown[index + 1]) && position(entry)! + 1 === position(shown[index + 1]), canRemove: allowed});
    }));
    const remaining = view.remainingCount;
    const shownPositions = shown.map(position), counted = currentPosition !== undefined && shownPositions.every(value => value !== undefined)
      && new Set(shownPositions).size === shown.length && shownPositions.slice(1).every(value => value! > currentPosition);
    const more = counted && remaining !== undefined && Number.isSafeInteger(remaining) && remaining >= shown.length - 1
      ? remaining - (shown.length - 1) : null;
    const failure = error ?? (workspace.error ? 'queue_edit_failed' as const : undefined);
    const snapshot: WebQueueListSnapshot = Object.freeze({current: true, fence: source.fence, queueId: queue.id, revision: queue.revision,
      currentEntryId: queue.currentEntryId ?? undefined, entries, more, change: busy ? 'sending' : failure ? 'failed' : 'idle', ...(failure ? {error: failure} : {})});
    cache = {view, workspace, sending, error, snapshot};
    return snapshot;
  };
  const execute = async (snapshot: WebQueueListSnapshot, entryId: string, action: 'play' | 'remove' | -1 | 1, lifetime: WebQueueListLifetime) => {
    const current = () => source.isCurrent() && !lifetime.signal.aborted && lifetime.isCurrent();
    if (sending || !current() || snapshot !== getSnapshot() || !snapshot.current || !snapshot.revision || snapshot.queueId !== source.queueId) throw changed();
    const entry = snapshot.entries.find(row => row.entryId === entryId);
    if (!entry || (action === 'play' ? !entry.canPlay : action === 'remove' ? !entry.canRemove : action === -1 ? !entry.canMoveUp : !entry.canMoveDown)) throw changed();
    const {view} = source.read();
    if (!view || view.queue.revision !== snapshot.revision) throw changed();
    const index = snapshot.entries.findIndex(row => row.entryId === entryId);
    if (index < 0) throw changed();
    sending = true;error = undefined;notify();
    try {
      if (!current()) throw changed();
      const accepted = source.read();
      // Subscribers may run synchronously while the sending state publishes.
      // Their newer queue can never replace the command's captured witness.
      if (accepted.view !== view || accepted.workspace.queue?.id !== snapshot.queueId || accepted.workspace.queue.revision !== snapshot.revision) throw changed();
      if (action === 'play') await source.play(entryId, snapshot.revision, lifetime);
      else if (action === 'remove') await source.edit(snapshot.revision, {action: 'remove', entryId}, lifetime);
      else {
        const to = index + action;
        const neighbor = snapshot.entries[to];
        if (!neighbor || neighbor.playing || neighbor.hidden) throw changed();
        // Only the actual adjacent loaded upcoming pair is submitted. The
        // canonical player validates contiguity and adopts returned occurrence IDs.
        const order = action === -1 ? [entryId, neighbor.entryId] : [neighbor.entryId, entryId];
        await source.edit(snapshot.revision, {action: 'reorder', entryIds: order}, lifetime);
      }
    } catch (failure) {
      if (current()) error = 'queue_edit_failed';
      // An uncertain edit never force-refreshes or adopts a replacement output.
      throw failure;
    } finally {sending = false;if (current()) notify();}
  };
  return Object.freeze({getSnapshot, isCurrent: () => getSnapshot().current,
    subscribe(listener: () => void) {listeners.add(listener);const off = source.subscribe(listener);return () => {listeners.delete(listener);off();};},
    play: (snapshot: WebQueueListSnapshot, entryId: string, lifetime: WebQueueListLifetime) => execute(snapshot, entryId, 'play', lifetime),
    remove: (snapshot: WebQueueListSnapshot, entryId: string, lifetime: WebQueueListLifetime) => execute(snapshot, entryId, 'remove', lifetime),
    move: (snapshot: WebQueueListSnapshot, entryId: string, direction: -1 | 1, lifetime: WebQueueListLifetime) => direction === -1 || direction === 1 ? execute(snapshot, entryId, direction, lifetime) : Promise.reject(changed()),
  });
}
