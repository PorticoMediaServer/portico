import type {V1QueuePlayer} from '@core/playback-v1/v1-queue-player.ts';
import type {MusicMixesController, MusicMixesSnapshot, MusicMixLifetime, MusicMixRow} from './music-mixes';

type Runtime = Readonly<{cataloguePlayback: () => Pick<V1QueuePlayer, 'playSegments'>}>;
const retired: MusicMixesSnapshot = Object.freeze({phase: 'retired', rows: Object.freeze([])});
const cancelled = () => new DOMException('Music Mixes view changed', 'AbortError');

/** Profile data may be shared; a command always keeps the original route and
 * installed runtime. Another screen's lease can never revive this caller. */
export function bindMusicMixes(source: MusicMixesController, caller: MusicMixLifetime, runtime: () => Runtime | undefined) {
  const life = new AbortController(), subscriptions = new Set<Readonly<{listener: () => void; off: () => void}>>();
  let closed = false;
  const retire = (silent = false) => {
    if (closed) return;
    closed = true;caller.signal.removeEventListener('abort', dispose);life.abort();
    const listeners = new Set([...subscriptions].map(value => value.listener));
    for (const subscription of subscriptions) subscription.off();subscriptions.clear();
    if (!silent) for (const listener of listeners) listener();
  };
  const dispose = () => retire();
  const current = () => {if (closed) return false;if (caller.signal.aborted || !caller.isCurrent()) {dispose();return false;}return true;};
  const assert = () => {if (!current()) throw cancelled();};
  const signal = AbortSignal.any([caller.signal, life.signal]);
  if (caller.signal.aborted) dispose();else caller.signal.addEventListener('abort', dispose, {once: true});
  return Object.freeze({
    source,
    getSnapshot: () => {
      if (!closed && (caller.signal.aborted || !caller.isCurrent())) retire(true);
      return closed ? retired : source.getSnapshot();
    },
    subscribe(listener: () => void) {
      if (!current()) return () => {};
      const subscription = Object.freeze({listener, off: source.subscribe(() => {if (current()) listener();})});subscriptions.add(subscription);
      return () => {subscription.off();subscriptions.delete(subscription);};
    },
    contains: (row: MusicMixRow) => current() && source.contains(row),
    canPlay: (row: MusicMixRow) => current() && source.contains(row) && !!runtime(),
    async load() {assert();return source.load();},
    async play(row: MusicMixRow, shuffle = false) {
      assert();const owner = runtime();
      if (!owner) return Promise.reject(Object.assign(new Error('music_mix_playback_unavailable'), {code: 'music_mix_playback_unavailable'}));
      return source.play(row, owner.cataloguePlayback(), {signal, isCurrent: () => current() && runtime() === owner}, shuffle);
    },
    async save(row: MusicMixRow) {assert();return source.save(row, {signal, isCurrent: current});},
    dispose,
  });
}
export type MusicMixesBinding = ReturnType<typeof bindMusicMixes>;
