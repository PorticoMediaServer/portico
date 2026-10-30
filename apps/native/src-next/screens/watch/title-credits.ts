import type {CreditPage, DetailCredit} from '../../../../../packages/client-core/src/detail';

export type NativeTitleCreditsSnapshot = Readonly<{
  credits: readonly DetailCredit[];
  hasMore: boolean;
  loading: boolean;
  waitingId: string;
  fulfilled?: Readonly<{waitingId: string; firstItemId?: string}>;
  error?: unknown;
}>;
const changed = () => Object.assign(new Error('stale_continuation'), {code: 'stale_continuation'});

export type NativeTitleCreditsOptions = Readonly<{
  id: string; revision: CreditPage['revision']; credits: readonly DetailCredit[];
  totals?: Readonly<{cast: number; crew: number}>;
  current: () => boolean;
  read: (group: 'cast' | 'crew', cursor: string | undefined, signal: AbortSignal) => Promise<CreditPage>;
  changed: () => void;
}>;

/** One detail revision's cast and crew. The initial mixed preview has no cursor:
 * read each group's real first page, then follow only its returned cursors. */
export class NativeTitleCredits {
  private readonly pages = new Map<'cast' | 'crew', CreditPage>();
  private readonly cursors = new Map<'cast' | 'crew', Set<string>>();
  private state: NativeTitleCreditsSnapshot;
  private request?: AbortController;
  private pending?: Promise<void>;
  private disposed = false;
  private readonly options: NativeTitleCreditsOptions;
  constructor(options: NativeTitleCreditsOptions) {
    this.options = options;
    const credits = Object.freeze([...new Map(options.credits.map(credit => [credit.id, credit])).values()]);
    this.state = Object.freeze({credits, loading: false, waitingId: this.waitingId('cast'),
      hasMore: !options.totals || credits.length < options.totals.cast + options.totals.crew});
  }
  getSnapshot = () => this.state;
  private current = () => !this.disposed && this.options.current();
  private waitingId(group: 'cast' | 'crew', cursor?: string) {return JSON.stringify(['title-credits', this.options.id, this.options.revision, group, cursor ?? null]);}
  private nextGroup(): 'cast' | 'crew' | undefined {
    for (const group of ['cast', 'crew'] as const) {
      const page = this.pages.get(group);
      if (!page || page.nextCursor) return group;
    }
  }
  private publish(patch: Partial<NativeTitleCreditsSnapshot>) {
    if (!this.current()) return;
    this.state = Object.freeze({...this.state, ...patch}); this.options.changed();
  }
  next = (expected: NativeTitleCreditsSnapshot): Promise<void> => {
    if (!this.current() || expected !== this.state) return Promise.resolve();
    if (this.pending) return this.pending;
    if (this.state.loading) return Promise.resolve();
    const group = this.nextGroup();
    if (!this.state.hasMore || !group) return Promise.resolve();
    const previous = this.pages.get(group), cursor = previous?.nextCursor, waitingId = this.state.waitingId;
    const request = new AbortController(); this.request = request;
    const current = () => this.current() && this.request === request && !request.signal.aborted;
    this.publish({loading: true, error: undefined});
    const task = Promise.resolve().then(async () => {
      if (!current()) return;
      try {
        const page = await this.options.read(group, cursor, request.signal);
        if (!current()) return;
        if (page.group !== group || page.revision.catalog !== this.options.revision.catalog || page.revision.viewer !== this.options.revision.viewer) throw changed();
        const seen = this.cursors.get(group) ?? new Set<string>();
        if (page.nextCursor && (page.nextCursor === cursor || seen.has(page.nextCursor))) throw changed();
        if (cursor) seen.add(cursor); this.cursors.set(group, seen);
        this.pages.set(group, page);
        const known = new Set(this.state.credits.map(credit => credit.id)), added = page.credits.filter(credit => {
          if (known.has(credit.id)) return false; known.add(credit.id); return true;
        });
        const credits = Object.freeze([...this.state.credits, ...added]);
        const next = this.nextGroup(), nextCursor = next && this.pages.get(next)?.nextCursor;
        this.publish({credits, loading: false, hasMore: !!next, waitingId: next ? this.waitingId(next, nextCursor) : waitingId,
          fulfilled: {waitingId, ...(added[0] ? {firstItemId: added[0].id} : {})}});
      } catch (error) {if (current()) this.publish({loading: false, error});}
    }).finally(() => {if (this.request === request) this.request = undefined; if (this.pending === task) this.pending = undefined;});
    this.pending = task; return task;
  };
  cancel = () => {
    this.request?.abort(); this.request = undefined; this.pending = undefined;
    if (this.state.loading) this.publish({loading: false});
  };
  dispose = () => {if (this.disposed) return; this.disposed = true; this.cancel();};
}
