/**
 * The last known document, shown at once.
 *
 * A screen's document (the service that read it, with what it has loaded and where the person
 * was in it) is kept after the screen closes. Opening the same thing again shows it immediately;
 * it is read again in the background only if something it depends on moved while it was away
 * (`FreshnessHub` kept listening on its behalf). Pressing Back therefore fetches nothing unless
 * there is something new to show.
 *
 * Kept documents belong to one signed-in viewer on one server: the pool is made for that viewer
 * and disposed with them. It is bounded; the least recently used document that no screen is
 * showing goes first.
 */
import type {FreshDocument, FreshDocumentOptions, FreshnessDependency, FreshnessHub, RevalidateContext} from './freshness-hub.ts';

export type PooledDocument<S> = Readonly<{
  service: S;
  document: FreshDocument;
  /** This call made the service: nothing has been loaded yet, so the screen starts its first load. */
  created: boolean;
  /** Small things the screen wants to find again with the service (which route it last selected). */
  memo: Record<string, unknown>;
  /** This screen is showing the document (true) or is covered or leaving (false). */
  setOpen(open: boolean): void;
  /** The screen is gone. The document stays in the pool for the next one. */
  release(): void;
}>;

type Entry = {service: {dispose?(): void}; document: FreshDocument; stop: () => void; memo: Record<string, unknown>; users: Set<object>; open: Set<object>};

export class DocumentPool {
  private entries = new Map<string, Entry>();
  private readonly max: number;
  private disposed = false;

  readonly hub: FreshnessHub;
  constructor(hub: FreshnessHub, options: {max?: number} = {}) { this.hub = hub; this.max = Math.max(1, options.max ?? 24); }

  /**
   * The document kept under `key`, or a new one from `create`. `create` returns the service, how
   * the hub reads it again, and optionally `watch`: a subscription set up once the document
   * exists (it returns the way to end it).
   */
  acquire<S extends {dispose?(): void}>(key: string, create: () => {service: S; document: FreshDocumentOptions; watch?(document: FreshDocument): () => void}): PooledDocument<S> {
    let entry = this.entries.get(key), created = false;
    if (entry) this.entries.delete(key);
    else {
      const made = create();
      const document = this.hub.document(made.document);
      entry = {service: made.service, document, stop: made.watch?.(document) ?? (() => {}), memo: {}, users: new Set(), open: new Set()};
      created = true;
    }
    if (this.disposed) { const lone = entry; return {service: lone.service as S, document: lone.document, created, memo: lone.memo, setOpen: () => {}, release: () => this.end(lone)}; }
    this.entries.set(key, entry);
    const user = {}, held = entry;
    held.users.add(user);
    this.trim();
    return {
      service: held.service as S, document: held.document, created, memo: held.memo,
      setOpen: open => {
        if (!held.users.has(user)) return;
        if (open) held.open.add(user); else held.open.delete(user);
        held.document.setOpen(held.open.size > 0);
      },
      release: () => {
        if (!held.users.delete(user)) return;
        held.open.delete(user);
        held.document.setOpen(held.open.size > 0);
        if (this.entries.get(key) !== held && !held.users.size) this.end(held); else this.trim();
      },
    };
  }

  /**
   * The same as `acquire` for a screen that is still being drawn and may yet be abandoned: the
   * document is found or made and kept for a moment, without a screen being counted as showing
   * it. The screen calls `acquire` once it is really there.
   */
  peek<S extends {dispose?(): void}>(key: string, create: () => {service: S; document: FreshDocumentOptions; watch?(document: FreshDocument): () => void}, holdMs = 5000): Pick<PooledDocument<S>, 'service' | 'document' | 'memo' | 'created'> {
    const held = this.acquire(key, create);
    this.hub.timers.setTimer(() => held.release(), holdMs);
    return {service: held.service, document: held.document, memo: held.memo, created: held.created};
  }

  /** Forget one document (it was deleted, or the screen knows it can never be shown again). */
  evict(key: string): void {
    const entry = this.entries.get(key);
    if (!entry) return;
    this.entries.delete(key);
    if (!entry.users.size) this.end(entry);
  }

  /** Forget every document whose key starts with `prefix`. */
  evictPrefix(prefix: string): void { for (const key of [...this.entries.keys()]) if (key.startsWith(prefix)) this.evict(key); }

  get size(): number { return this.entries.size; }

  private end(entry: Entry): void {
    try { entry.stop(); } catch { /* the subscription is already gone */ }
    entry.document.dispose();
    try { entry.service.dispose?.(); } catch { /* already disposed */ }
  }

  private trim(): void {
    for (const [key, entry] of this.entries) {
      if (this.entries.size <= this.max) break;
      if (entry.users.size) continue;
      this.entries.delete(key);
      this.end(entry);
    }
  }

  dispose(): void {
    if (this.disposed) return;
    this.disposed = true;
    for (const [key, entry] of [...this.entries]) { this.entries.delete(key); if (!entry.users.size) this.end(entry); }
  }
}

/** What a client-core document service offers the hub: its state, a quiet read, and a way to try a failed first load again. */
export type QuietService = {
  getSnapshot(): {phase: string; error?: unknown};
  subscribe(listener: () => void): () => void;
  /** Read again without touching `phase`; show the answer in place; reject when the read failed. */
  revalidate(context?: RevalidateContext): Promise<void>;
  retry?(): Promise<void>;
  dispose?(): void;
};

const hasContentByPhase = (service: QuietService) => { const phase = service.getSnapshot().phase; return phase === 'ready' || phase === 'refresh-required'; };

/**
 * How the hub keeps one client-core service current. A first load that failed with nothing to
 * show is tried again by the hub on its usual waits, so a screen opened while the server was
 * away fills in by itself when it returns.
 */
export function serviceDocument<S extends QuietService>(service: S, dependencies: readonly FreshnessDependency[] | (() => readonly FreshnessDependency[]), options: {hasContent?(service: S): boolean} = {}): {service: S; document: FreshDocumentOptions; watch(document: FreshDocument): () => void} {
  const filled = () => (options.hasContent ? options.hasContent(service) : hasContentByPhase(service));
  return {
    service,
    document: {
      dependencies,
      hasContent: filled,
      revalidate: context => service.revalidate(context),
      ...(service.retry ? {recover: async () => {
        if (service.getSnapshot().phase !== 'error') return;
        await service.retry!();
        const after = service.getSnapshot();
        if (after.phase === 'error') throw after.error ?? new Error('The document could not be read.');
      }} : {}),
    },
    watch: document => {
      let was = filled(), failed = false;
      return service.subscribe(() => {
        const now = filled(), phase = service.getSnapshot().phase;
        if (now && !was) document.check();
        // A first load that ended in an error with nothing on screen: ask the hub to try it again.
        if (!now && phase === 'error' && !failed) document.invalidate();
        failed = !now && phase === 'error';
        was = now;
      });
    },
  };
}
