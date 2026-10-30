/** Cursor ownership for one original music section. Rows are held in three
 * bounded chunks; cursor bookmarks allow revisiting earlier chunks without
 * treating a displayed window as whole-container membership. */
export type MusicSection<T> = Readonly<{
  phase: 'idle' | 'loading' | 'ready' | 'error' | 'refresh-required';
  items: readonly T[]; nextCursor: string; offset: number; revision?: string;
  canNext: boolean; canPrevious: boolean; total?: number; totalIsLowerBound?: boolean;
  error?: unknown;
}>;
export type MusicSectionPage<T> = Readonly<{items: readonly T[]; nextCursor: string; revision?: string; total?: number; totalIsLowerBound?: boolean; offset?: number}>;
export const emptyMusicSection = <T>(): MusicSection<T> => Object.freeze({phase: 'idle', items: [], nextCursor: '', offset: 0, canNext: false, canPrevious: false});
const changed = () => Object.assign(new Error('music_section_changed'), {code: 'stale_continuation'});
type Chunk<T> = Readonly<{index: number; offset: number; page: MusicSectionPage<T>}>;
export class MusicSectionPager<T extends {id: string}> {
  private state = emptyMusicSection<T>();
  private chunks: Chunk<T>[] = [];
  private issued = new WeakSet<MusicSection<T>>([this.state]);
  private cursors = new Map<number, Readonly<{cursor: string; offset: number}>>([[0, {cursor: '', offset: 0}]]);
  private revision?: string;
  private total?: number;
  private lowerBound?: boolean;
  private controller = new AbortController();
  private pending?: Promise<void>;
  private pendingFrom?: MusicSection<T>;
  private failedIndex?: number;
  private focused?: T;
  private options: Readonly<{
    current: () => boolean; read: (cursor: string, signal: AbortSignal) => Promise<MusicSectionPage<T>>;
    publish: (section: MusicSection<T>) => void;
  }>;
  constructor(options: MusicSectionPager<T>['options']) {this.options=options;}
  getSnapshot = () => this.state;
  private current = () => !this.controller.signal.aborted && this.options.current();
  private publish(patch: Partial<MusicSection<T>>) {
    if (!this.current()) return;
    this.state = Object.freeze({...this.state, ...patch}); this.issued.add(this.state); this.options.publish(this.state);
  }
  load = () => this.read(0, this.state);
  next = (expected: MusicSection<T>) => {
    if (this.pending && (expected === this.pendingFrom || expected === this.state)) return this.pending;
    if (expected !== this.state || !expected.canNext || expected.phase !== 'ready') return Promise.resolve();
    if(this.chunks.length===3&&this.focused&&this.chunks[0].page.items.includes(this.focused))return Promise.resolve();
    return this.read((this.chunks.at(-1)?.index ?? -1) + 1, expected);
  };
  previous = (expected: MusicSection<T>) => {
    if (this.pending && (expected === this.pendingFrom || expected === this.state)) return this.pending;
    if (expected !== this.state || !expected.canPrevious || expected.phase !== 'ready') return Promise.resolve();
    const before = [...this.cursors.keys()].filter(index => index < this.chunks[0].index);
    return this.read(Math.max(...before), expected);
  };
  retry = (expected: MusicSection<T>) => {
    if (this.pending && (expected === this.pendingFrom || expected === this.state)) return this.pending;
    if (expected !== this.state || expected.phase !== 'error' || this.failedIndex === undefined) return Promise.resolve();
    return this.read(this.failedIndex, expected);
  };
  focus = (item:T, expected:MusicSection<T>) => {
    if(this.current()&&this.issued.has(expected)&&expected.items.includes(item)&&this.state.items.includes(item))this.focused=item;
  };
  blur = (item:T, expected:MusicSection<T>) => {
    if(this.current()&&this.issued.has(expected)&&expected.items.includes(item)&&this.focused===item)this.focused=undefined;
  };
  private read(index: number, expected: MusicSection<T>): Promise<void> {
    if (!this.current() || expected !== this.state) return Promise.resolve();
    if (this.pending) return this.pending;
    const bookmark = this.cursors.get(index);
    if (!bookmark) return Promise.resolve();
    const {cursor, offset} = bookmark;
    this.pendingFrom = expected;
    this.publish({phase: 'loading', error: undefined});
    // Defer the producer until pending is installed, including synchronous failures.
    const task = Promise.resolve().then(async () => {
      if (!this.current()) return;
      try {
        const page = await this.options.read(cursor, this.controller.signal);
        if (!this.current()) return;
        if (index === 0 && !this.chunks.length) {
          this.revision = page.revision; this.total = page.total; this.lowerBound = page.totalIsLowerBound;
        } else if (page.revision !== this.revision || this.lowerBound !== true && page.total !== this.total) throw changed();
        if (page.nextCursor && (page.nextCursor === cursor || [...this.cursors].some(([at, value]) => value.cursor === page.nextCursor && at !== index + 1))) throw changed();
        if (this.cursors.has(index + 1) && this.cursors.get(index + 1)!.cursor !== page.nextCursor) throw changed();
        if (page.nextCursor && !page.items.length) throw changed();
        if (page.offset !== undefined && page.offset !== offset) throw changed();
        if (!page.nextCursor && page.total !== undefined && !page.totalIsLowerBound && offset + page.items.length !== page.total) throw changed();
        const chunk = {index, offset, page};
        const previous = this.chunks[0]?.index;
        const adjacent = previous !== undefined && index === previous - 1;
        const chunks = previous !== undefined && index < previous
          ? (adjacent ? [chunk, ...this.chunks].slice(0, 3) : [chunk]) : [...this.chunks, chunk].slice(-3);
        if(this.focused&&!chunks.some(chunk=>chunk.page.items.includes(this.focused!))){
          // Focus may have moved back while the page was in flight. Keep that
          // actual host; the same offered cursor remains available after Blur.
          this.failedIndex=undefined;
          const first=this.chunks[0],last=this.chunks.at(-1);
          this.publish({phase:'ready',error:undefined,canNext:!!last?.page.nextCursor,canPrevious:!!first&&first.index>0});return;
        }
        const ids = new Set<string>();
        for (const item of chunks.flatMap(value => value.page.items)) {if (ids.has(item.id)) throw changed(); ids.add(item.id);}
        if (page.nextCursor) this.cursors.set(index + 1, {cursor: page.nextCursor, offset: offset + page.items.length});
        // Keep the original first page plus the nearest 63 real bookmarks.
        // When an old bookmark ages out, Previous returns to the original
        // first page; its genuine offered cursors still reach every window.
        while (this.cursors.size > 64) {const oldest = [...this.cursors.keys()].filter(key => key !== 0 && key !== index && key !== index + 1).sort((a,b) => a-b)[0]; this.cursors.delete(oldest);}
        this.chunks = chunks; this.failedIndex = undefined;
        const first = chunks[0], last = chunks.at(-1)!;
        this.publish({phase: 'ready', items: Object.freeze(chunks.flatMap(value => value.page.items)), offset: first.offset, revision: this.revision,
          nextCursor: last.page.nextCursor, canNext: !!last.page.nextCursor, canPrevious: first.index > 0,
          total: this.total, totalIsLowerBound: this.lowerBound, error: undefined});
      } catch (error) {
        if (!this.current()) return;
        const code = (error as {code?: unknown})?.code;
        const refresh = ['stale_continuation', 'browse_revision_changed', 'permission_changed', 'cursor_expired', 'invalid_cursor'].includes(String(code));
        this.failedIndex = refresh ? undefined : index;
        this.publish({phase: refresh ? 'refresh-required' : 'error', error, canNext: false});
      }
    }).finally(() => {if (this.pending === task) {this.pending = undefined; this.pendingFrom = undefined;}});
    this.pending = task; return task;
  }
  dispose = () => {this.controller.abort(); this.chunks = []; this.cursors.clear();};
}
