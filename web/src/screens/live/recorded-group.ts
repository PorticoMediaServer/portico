import {NextDVRClient} from '@core/next-dvr.ts';
import {commandRefusal} from './command-refusal';
import {dvrMessage, playableRecording, type DVRPage, type Recording} from '@core/dvr.ts';
import {screenLifetime, type ScreenContext} from '../detail/context';

const cancelled = () => new DOMException('Recorded show closed', 'AbortError');

export type RecordedGroupSnapshot = Readonly<{phase: 'idle' | 'loading' | 'ready' | 'error' | 'retired';
  recordings: readonly Recording[]; nextCursor?: string; busy: boolean; error?: string;
  /** The recording a refused command was for, so its row says it. */
  errorRecordingId?: string}>;

/**
 * One show's own recordings, read by the server's recorded-group key. The main
 * Recordings door keeps its page and summary; this reader is a separate, bounded
 * cursor over the same DVR client and is retired with the panel that opened it.
 */
export function createRecordedGroupReader(context: ScreenContext, group: string, sort: 'recent' | 'title' | 'oldest', everyone = false) {
  const life = screenLifetime(context), listeners = new Set<() => void>();
  const client = new NextDVRClient(life.api);
  client.everyone = everyone;
  let closed = false, generation = 0, pending: Promise<void> | undefined, pendingToken: object | undefined;
  let page: DVRPage | undefined;
  let state: RecordedGroupSnapshot = Object.freeze({phase: 'idle', recordings: [], busy: false});
  const current = () => !closed && life.current();
  const assert = () => { if (!current()) throw cancelled(); };
  const publish = (patch: Partial<RecordedGroupSnapshot>) => {
    if (closed) return;
    state = Object.freeze({...state, ...patch});
    for (const listener of [...listeners]) listener();
  };
  const read = async (more = false): Promise<void> => {
    assert();
    if (pending) return pending;
    if (more && !page?.nextCursor) return;
    const ticket = generation, cursor = more ? page!.nextCursor : '';
    const token = {};
    const task = (async () => {
      try {
        const result = await client.list('recorded', cursor, sort, group);
        assert();if (ticket !== generation) throw cancelled();
        page = result;
        const merged = more ? [...state.recordings, ...result.recordings.filter(item => !state.recordings.some(old => old.id === item.id))] : result.recordings;
        publish({phase: 'ready', recordings: merged, nextCursor: result.nextCursor || undefined, error: undefined});
      } catch (error) {
        if (current() && ticket === generation) publish({phase: state.phase === 'ready' ? 'ready' : 'error', error: dvrMessage(error)});
        throw error;
      } finally { if (pendingToken === token) pending = undefined; }
    })();
    pending = task;pendingToken = token;return task;
  };
  const command = async (work: (target: Recording) => Promise<unknown>, target: Recording): Promise<boolean> => {
    assert();
    if (state.busy || !state.recordings.includes(target)) return false;
    publish({busy: true, error: undefined, errorRecordingId: undefined});
    try { await work(target); assert(); await read(); return true; }
    catch (error) { if (current()) publish({error: commandRefusal(error), errorRecordingId: target.id}); return false; }
    finally { publish({busy: false}); }
  };
  return {
    getSnapshot: () => state,
    subscribe(listener: () => void) { if (closed) return () => {};listeners.add(listener);return () => {listeners.delete(listener);}; },
    async load() { assert();generation++;publish({phase: 'loading', recordings: [], error: undefined});await read(); },
    more() { return read(true); },
    keep(recording: Recording): Promise<boolean> { return command(target => client.keep(target), recording); },
    remove(recording: Recording): Promise<boolean> {
      if (!playableRecording(recording) && !['completed', 'incomplete-playable', 'failed'].includes(recording.state)) return Promise.resolve(false);
      return command(async target => {const preview = await client.deletePreview(target);await client.delete(target, preview);}, recording);
    },
    dispose() {
      if (closed) return;
      closed = true;generation++;life.dispose();client.dispose();listeners.clear();
    },
  };
}
export type RecordedGroupReader = ReturnType<typeof createRecordedGroupReader>;
