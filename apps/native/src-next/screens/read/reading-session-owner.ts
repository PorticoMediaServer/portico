import type {ReadingHostSession} from '../../../../../packages/client-core/src/reading-host';
import type {ReadingProgress, ReadingSession} from '../../../../../packages/client-core/src/reading';

type Session = ReadingHostSession & Pick<ReadingSession, 'close'>;
type Accepted = Readonly<{session: Session; release: () => void}>;
type Snapshot = Readonly<{phase: 'idle' | 'opening' | 'ready' | 'failed' | 'retired'; session?: Session;
  manifest?: Session['manifest']; confirmedProgress?: ReadingProgress; error?: unknown}>;
type Source = Readonly<{current: () => boolean; subscribe: (listener: () => void) => () => void;
  acquire: (signal: AbortSignal, current: () => boolean) => Promise<Accepted>}>;
const retired = () => Object.assign(new Error('reading_retired'), {name: 'AbortError', code: 'cancelled'});

const cleanups = new WeakMap<Accepted, Promise<void>>();
export const closeReadingResource = (owned: Accepted): Promise<void> => {
  const existing = cleanups.get(owned); if (existing) return existing;
  const task = Promise.resolve().then(async () => {
    const deadline = new AbortController(); let timer: ReturnType<typeof setTimeout> | undefined;
    const limit = new Promise<void>(resolve => {timer = setTimeout(() => {deadline.abort(); resolve();}, 2000);});
    const closing = Promise.resolve().then(() => owned.session.close(deadline.signal));
    try {await Promise.race([closing, limit]);} finally {if (timer) clearTimeout(timer); owned.release();}
  });
  cleanups.set(owned, task); return task;
};

/** One original occurrence owns one session. Guest tickets never acquire or close it. */
export function createReadingSessionOwner(source: Source) {
  let ended = false, accepted: Accepted | undefined, opening: Promise<Snapshot> | undefined, close: Promise<void> | undefined;
  let state: Snapshot = Object.freeze({phase: 'idle'}), sequence = 0;
  const life = new AbortController(), listeners = new Set<() => void>(), tickets = new Map<number, () => void>();
  let off = () => {};
  const emit = () => {for (const listener of [...listeners]) {try {listener();} catch { /* An observer cannot revive this owner. */ }}};
  const update = (next: Snapshot) => {state = Object.freeze(next); emit();};
  const current = () => {if (ended || life.signal.aborted) return false;
    try {if (!source.current() || ended || life.signal.aborted || accepted?.session.signal.aborted) return false; return true;} catch {return false;}};
  const assert = () => {if (!current()) throw retired();};
  const dispose = () => {
    if (ended) return close ?? Promise.resolve();
    ended = true;
    for (const cleanup of [...tickets.values()]) {try {cleanup();} catch { /* Continue retiring the original owner. */ }}
    tickets.clear(); life.abort(); off();
    const owned = accepted; accepted = undefined;
    update({phase: 'retired'}); listeners.clear();
    close = owned ? closeReadingResource(owned) : Promise.resolve();
    return close;
  };
  const changed = () => {if (!current()) void dispose().catch(() => {});};
  let started = false;
  const start = () => {if (started || ended) return; started = true; off = source.subscribe(changed);
    if (ended) off(); else if (!current()) void dispose().catch(() => {});};
  const ensure = (): Promise<Snapshot> => {
    start(); assert();
    if (opening) return opening;
    if (accepted) return Promise.resolve(state);
    if (state.phase === 'failed') return Promise.reject(state.error);
    opening = Promise.resolve().then(async () => {
      assert();
      let acquired: Accepted | undefined, transferred = false;
      try {
        acquired = await source.acquire(life.signal, current); assert();
        if (acquired.session.signal.aborted) throw retired();
        accepted = acquired; transferred = true;
        const owned = acquired;
        const onAbort = () => {void dispose().catch(() => {});};
        owned.session.signal.addEventListener('abort', onAbort, {once: true});
        if (owned.session.signal.aborted) {onAbort(); throw retired();}
        const progress = await owned.session.progress(life.signal); assert();
        update({phase: 'ready', session: owned.session, manifest: owned.session.manifest, confirmedProgress: progress});
        return state;
      } catch (error) {
        if (acquired && !transferred) {try {await closeReadingResource(acquired);} catch { /* Preserve the original acquisition error. */ }}
        if (!ended) update({phase: 'failed', ...(accepted ? {session: accepted.session, manifest: accepted.session.manifest} : {}), error});
        throw error;
      }
    });
    update({phase: 'opening'});
    return opening;
  };
  const attach = () => {
    assert(); const owner = accepted;
    if (!owner || state.phase !== 'ready' || !state.confirmedProgress) throw retired();
    const ticket = ++sequence; tickets.set(ticket, () => {});
    const valid = () => tickets.has(ticket) && current() && tickets.has(ticket) && accepted === owner;
    const check = () => {if (!valid()) throw retired();};
    const original = owner.session;
    const session: ReadingHostSession = {
      manifest: original.manifest, signal: original.signal,
      headPDF: (...args) => {check(); return original.headPDF(...args);},
      readRange: (...args) => {check(); return original.readRange(...args);},
      readAsset: (...args) => {check(); return original.readAsset(...args);},
      page: (...args) => {check(); return original.page(...args);},
      ...(original.openSequential ? {openSequential: (...args: Parameters<NonNullable<ReadingHostSession['openSequential']>>) => {check(); return original.openSequential!(...args);}} : {}),
      ...(original.annotations ? {annotations: (...args: Parameters<NonNullable<ReadingHostSession['annotations']>>) => {check(); return original.annotations!(...args);}} : {}),
      ...(original.mutateAnnotation ? {mutateAnnotation: (...args: Parameters<NonNullable<ReadingHostSession['mutateAnnotation']>>) => {check(); return original.mutateAnnotation!(...args);}} : {}),
      progress: async (...args) => {check(); const result = await original.progress(...args); check(); update({...state, confirmedProgress: result}); return result;},
      saveProgress: async (...args) => {check(); const result = await original.saveProgress(...args); check();
        if (result.saved) update({...state, confirmedProgress: {progress: result.progress}}); return result;},
    };
    return Object.freeze({session, initialProgress: state.confirmedProgress, current: valid,
      onRetire: (cleanup: () => void) => {if (!valid()) {cleanup(); return;} tickets.set(ticket, cleanup);},
      release: () => {tickets.delete(ticket);}});
  };
  return Object.freeze({current, ensure, attach, snapshot: () => state, subscribe: (listener: () => void) => {if (ended) return () => {}; listeners.add(listener); return () => {listeners.delete(listener);};}, dispose});
}
