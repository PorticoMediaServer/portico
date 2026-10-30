/**
 * A small callback driver for tests that do not make lifecycle claims.
 *
 * This is not a React reconciler. Setters/dispatchers are recreated, layout and
 * passive effects share a phase, and external stores subscribe during render.
 * Its manual replay is not StrictMode. Use helpers/fabric-host.mjs for ownership,
 * effect ordering, subscription races, keyed identity and unmount behavior.
 * Keep this driver only for calculations/callbacks whose assertions do not rely
 * on those semantics; changing it into another reconciler would obscure the
 * actual lifecycle boundary.
 */

import * as React from 'react';

const internals = (
  React as unknown as {
    __CLIENT_INTERNALS_DO_NOT_USE_OR_WARN_USERS_THEY_CANNOT_UPGRADE: {H: unknown};
  }
).__CLIENT_INTERNALS_DO_NOT_USE_OR_WARN_USERS_THEY_CANNOT_UPGRADE;

if (!internals || !Object.hasOwn(internals, 'H')) {
  throw new Error('The hook driver does not support this React version. Update the driver before running hook tests.');
}

type Deps = readonly unknown[] | undefined;
type Destructor = (() => void) | void;

type StateCell = {kind: 'state'; value: unknown};
type RefCell = {kind: 'ref'; ref: {current: unknown}};
type MemoCell = {kind: 'memo'; deps: Deps; value: unknown};
type EffectCell = {kind: 'effect'; deps: Deps; create?: () => Destructor; destroy: Destructor};
type StoreCell = {kind: 'store'; unsubscribe: (() => void) | null; subscribe?: (onChange: () => void) => () => void};
type Cell = StateCell | RefCell | MemoCell | EffectCell | StoreCell;

const sameDeps = (a: Deps, b: Deps) =>
  !!a && !!b && a.length === b.length && a.every((value, i) => Object.is(value, b[i]));

/** Live hosts, so the exported `act` can flush whatever a test has mounted. */
const hosts = new Set<HookHost>();

class HookHost {
  private cells: Cell[] = [];
  private cursor = 0;
  private pending: {cell: EffectCell; create: () => Destructor; deps: Deps}[] = [];
  private scheduled = false;
  private mounted = false;
  private rendering = false;

  private body: () => void;

  constructor(body: () => void) {
    this.body = body;
  }

  private dispatcher = {
    useState: <S,>(initial: S | (() => S)) => {
      const cell = this.cell<StateCell>(() => ({
        kind: 'state',
        value: typeof initial === 'function' ? (initial as () => S)() : initial,
      }));
      const set = (next: S | ((previous: S) => S)) => {
        const value =
          typeof next === 'function' ? (next as (p: S) => S)(cell.value as S) : next;
        if (Object.is(value, cell.value)) return;
        cell.value = value;
        this.schedule();
      };
      return [cell.value as S, set] as const;
    },
    useReducer: <S, A>(reducer: (state: S, action: A) => S, initial: S) => {
      const cell = this.cell<StateCell>(() => ({kind: 'state', value: initial}));
      const dispatch = (action: A) => {
        const value = reducer(cell.value as S, action);
        if (Object.is(value, cell.value)) return;
        cell.value = value;
        this.schedule();
      };
      return [cell.value as S, dispatch] as const;
    },
    useRef: <S,>(initial: S) =>
      this.cell<RefCell>(() => ({kind: 'ref', ref: {current: initial}})).ref as {current: S},
    useMemo: <S,>(create: () => S, deps: Deps) => {
      const cell = this.cell<MemoCell>(() => ({kind: 'memo', deps: undefined, value: undefined}));
      if (!cell.deps || !sameDeps(cell.deps, deps)) {
        cell.value = create();
        cell.deps = deps;
      }
      return cell.value as S;
    },
    useCallback: <S,>(fn: S, deps: Deps) => this.dispatcher.useMemo(() => fn, deps),
    useEffect: (create: () => Destructor, deps: Deps) => {
      const cell = this.cell<EffectCell>(() => ({kind: 'effect', deps: undefined, destroy: undefined}));
      if (!this.seen.has(cell) || !sameDeps(cell.deps, deps)) {
        this.pending.push({cell, create, deps});
      }
      return undefined;
    },
    useLayoutEffect: (create: () => Destructor, deps: Deps) =>
      this.dispatcher.useEffect(create, deps),
    useInsertionEffect: () => undefined,
    // No reconciler: unprovided contexts expose their default value.
    useContext: <T,>(context: {_currentValue: T}) => context._currentValue,
    useDebugValue: () => undefined,
    useId: () => 'driver-id',
    useSyncExternalStore: <S,>(
      subscribe: (onChange: () => void) => () => void,
      getSnapshot: () => S,
    ) => {
      const cell = this.cell<StoreCell>(() => ({kind: 'store', unsubscribe: null}));
      if (cell.subscribe !== subscribe) {
        cell.unsubscribe?.();
        cell.subscribe = subscribe;
        cell.unsubscribe = subscribe(() => this.schedule());
      }
      return getSnapshot();
    },
  };

  /** Effect cells that have already run at least once. */
  private seen = new WeakSet<EffectCell>();

  private cell<T extends Cell>(create: () => T): T {
    const existing = this.cells[this.cursor];
    if (existing) {
      this.cursor++;
      return existing as T;
    }
    const made = create();
    this.cells[this.cursor] = made;
    this.cursor++;
    return made;
  }

  private schedule() {
    this.scheduled = true;
  }

  private render() {
    const previous = internals.H;
    internals.H = this.dispatcher;
    try {
      // A render-phase state adjustment retries the body before anything is committed. Effects
      // queued by a discarded pass are discarded with it, exactly as React discards that render.
      for (let pass = 0; ; pass++) {
        if (pass > 100) throw new Error('hook-driver: render-phase update did not settle');
        // The update that scheduled this render is spent by entering it; only an adjustment
        // made while the body runs may retry. Clearing it after the body instead would render
        // every ordinary update twice.
        this.scheduled = false;
        this.pending = [];
        this.cursor = 0;
        this.rendering = true;
        try {
          this.body();
        } finally {
          this.rendering = false;
        }
        if (!this.scheduled) break;
      }
    } finally {
      internals.H = previous;
    }
  }

  /** Commit: every changed effect's previous cleanup runs first, then the replacements.
   * React runs all layout cleanups before any layout setup in the same commit, so one
   * effect's cleanup is observable to another effect's setup. */
  private commit() {
    const queued = this.pending;
    this.pending = [];
    for (const {cell} of queued) {
      if (this.seen.has(cell) && typeof cell.destroy === 'function') cell.destroy();
    }
    for (const {cell, create, deps} of queued) {
      cell.deps = deps;
      cell.create = create;
      cell.destroy = create();
      this.seen.add(cell);
    }
  }

  mount() {
    if (this.mounted) throw new Error('hook-driver: already mounted');
    this.mounted = true;
    hosts.add(this);
    this.render();
    this.commit();
  }

  rerender() {
    if (!this.mounted) throw new Error('hook-driver: not mounted');
    this.render();
    this.commit();
  }

  unmount() {
    if (!this.mounted) return;
    this.mounted = false;
    hosts.delete(this);
    for (const cell of this.cells) {
      if (cell.kind === 'effect' && typeof cell.destroy === 'function') cell.destroy();
      if (cell.kind === 'store') cell.unsubscribe?.();
    }
    this.cells = [];
    this.pending = [];
  }

  /** Manual callback replay only; this does not model React StrictMode. */
  replay() {
    if (!this.mounted) throw new Error('hook-driver: not mounted');
    const effects = this.cells.filter((cell): cell is EffectCell =>
      cell.kind === 'effect' && this.seen.has(cell) && typeof cell.create === 'function');
    for (const cell of effects) if (typeof cell.destroy === 'function') cell.destroy();
    for (const cell of effects) cell.destroy = cell.create!();
  }

  /** One settle pass: render if something scheduled one, then run its effects. */
  flush() {
    if (!this.mounted) return false;
    if (this.rendering) throw new Error('hook-driver: flush during render');
    if (!this.scheduled && !this.pending.length) return false;
    if (this.scheduled) this.render();
    this.commit();
    return true;
  }
}

export function createHookHost(body: () => void) {
  return new HookHost(body);
}

/**
 * Runs `work`, then settles every mounted host: repeatedly drains the
 * microtask queue and re-renders until no host has pending work. This flushes only this driver; real lifecycle tests use Fabric settlement.
 */
export async function act(work: () => void | Promise<void>): Promise<void> {
  await work();
  for (let pass = 0; pass < 200; pass++) {
    await Promise.resolve();
    await new Promise<void>(done => setImmediate(done));
    let moved = false;
    for (const host of [...hosts]) if (host.flush()) moved = true;
    if (!moved) return;
  }
  throw new Error('hook-driver: act did not settle');
}
