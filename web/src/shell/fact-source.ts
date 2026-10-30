import type {ShellFacts} from './bootstrap';

/** Display observations belong to the admitted shell, never to its authority key. */
export type ShellFactsSource = Readonly<{
  getSnapshot(): ShellFacts;
  subscribe(listener: () => void): () => void;
}>;
type FactContext = Readonly<{facts: ShellFacts; factsSource?: ShellFactsSource}>;

export function subscribeFacts(context: FactContext, listener: () => void): () => void {
  return context.factsSource?.subscribe(listener) ?? (() => {});
}

/** Spreading a getter copies its value. Derived owners must forward the source instead. */
export function forwardFacts<T extends FactContext, E extends object>(context: T, extra: E): T & E {
  return {...context, ...extra, factsSource: context.factsSource,
    get facts() {return context.factsSource?.getSnapshot() ?? context.facts;}} as T & E;
}

function equal(a: unknown, b: unknown): boolean {return a === b || JSON.stringify(a) === JSON.stringify(b);}
function immutable<T>(value: T): T {
  if (value && typeof value === 'object' && !Object.isFrozen(value)) {
    for (const child of Object.values(value)) immutable(child);
    Object.freeze(value);
  }
  return value;
}

/** One last confirmed projection, with unchanged branches shared and no response history. */
export function createShellFactsSource(initial: ShellFacts) {
  let snapshot = immutable(initial), closed = false;
  const listeners = new Set<() => void>();
  return {
    getSnapshot: () => snapshot,
    subscribe(listener: () => void) {if (closed) return () => {}; listeners.add(listener); return () => {listeners.delete(listener);};},
    update(patch: Partial<ShellFacts>) {
      if (closed) return;
      const candidate = {...snapshot, ...patch};
      const identity = Object.fromEntries(Object.entries(candidate.identity).map(([key, value]) => [key,
        equal(snapshot.identity[key as keyof typeof snapshot.identity], value) ? snapshot.identity[key as keyof typeof snapshot.identity] : value])) as ShellFacts['identity'];
      const next = {identity: equal(snapshot.identity, identity) ? snapshot.identity : identity,
        preferences: equal(snapshot.preferences, candidate.preferences) ? snapshot.preferences : candidate.preferences,
        serverName: candidate.serverName};
      if (next.identity === snapshot.identity && next.preferences === snapshot.preferences && next.serverName === snapshot.serverName) return;
      snapshot = immutable(next);
      for (const listener of [...listeners]) listener();
    },
    dispose() {closed = true; listeners.clear();},
  };
}
