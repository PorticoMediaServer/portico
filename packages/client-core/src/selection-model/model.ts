import {browseSelectionKey, selectionCapabilityNames, selectionKey, summarizeSelection} from './capabilities.ts';
import {summarizeQuerySelection} from './query-summary.ts';
import {catalogueID, checkedBrowseContext, checkedBrowseQuery, checkedContext, checkedItem, checkedLimits, checkedQuery, requireSelection, selectionCount, SelectionInputError} from './validation.ts';
import type {
  BrowseSelectionContext, ExplicitSelectionTarget, SelectedItem, SelectionChange, SelectionContext, SelectionLimits,
  SelectionItem, SelectionKind, SelectionOperation, SelectionQuery, SelectionRange, SelectionReadTicket, SelectionRegistration,
  SelectionSnapshot, SelectionTarget, SelectionWindow,
} from './types.ts';

type Position = Readonly<{sectionId: string; sectionOrder: number; index: number}>;
type Pick = SelectedItem & Readonly<{position: Position}>;
type RegisteredWindow = Readonly<{value: SelectionWindow; items: readonly SelectedItem[]}>;
type Section = {order: number; queryId?: string; positions: Map<number, SelectedItem>};
type Registry = {
  sections: Map<string, Section>;
  items: Map<string, SelectedItem>;
  positions: Map<string, Position>;
  queryMembers: Map<string, Set<string>>;
  itemCount: number;
};
const success: SelectionChange = Object.freeze({ok: true});
const fail = <R extends string>(reason: R): Readonly<{ok: false; reason: R}> => Object.freeze({ok: false, reason});
const comparePosition = (a: Position, b: Position): number => a.sectionOrder - b.sectionOrder
  || (a.sectionId < b.sectionId ? -1 : a.sectionId > b.sectionId ? 1 : 0) || a.index - b.index;
const same = (a: unknown, b: unknown): boolean => JSON.stringify(a) === JSON.stringify(b);
const emptyRegistry = (): Registry => ({sections: new Map(), items: new Map(), positions: new Map(), queryMembers: new Map(), itemCount: 0});

/**
 * Ephemeral, synchronous selection for every renderer. Only mounted/windowed data
 * is registered; explicit picks survive window eviction. Whole sets retain a
 * selector and authoritative aggregate, never a list of catalogue membership.
 * No request, timer, global store, React dependency or credential is owned here.
 */
type SelectionContextPolicy<C> = Readonly<{
  check(input: C): C;
  key(context: C, item: SelectionItem): string;
  query(input: SelectionQuery, context: C): SelectionQuery;
}>;

/** Shared bounded window/pick algorithm. Source-specific context validation
 * never converts one source's continuity token into another's authority facts. */
class WindowedSelectionModel<C> {
  private context: C;
  private policy: SelectionContextPolicy<C>;
  private limits: SelectionLimits;
  private windows = new Map<string, RegisteredWindow>();
  private queries = new Map<string, SelectionQuery>();
  private registry: Registry = emptyRegistry();
  private chosen = new Map<string, Pick>();
  private whole: SelectionQuery | null = null;
  private wholeKind: SelectionKind | null = null;
  private excluded = new Map<string, SelectedItem>();
  private anchor: Readonly<{key: string; position: Position}> | null = null;
  private lastSectionId: string | null = null;
  private disposed = false;
  private version = 0;
  private readGeneration = 0;
  private operationGeneration = 0;
  private readTickets = new WeakMap<SelectionReadTicket<C>, number>();
  private operations = new WeakMap<SelectionOperation<C>, number>();
  private listeners = new Set<() => void>();
  private snapshot!: SelectionSnapshot<C>;

  constructor(context: C, policy: SelectionContextPolicy<C>, limits: Partial<SelectionLimits> = {}) {
    this.policy = policy;
    this.context = policy.check(context);
    this.limits = checkedLimits(limits);
    this.publish();
  }

  getSnapshot = (): SelectionSnapshot<C> => this.snapshot;
  subscribe = (listener: () => void): (() => void) => {
    if (this.disposed) return () => {};
    this.listeners.add(listener);
    return () => { this.listeners.delete(listener); };
  };

  /** Capture before an asynchronous read; register its results with this exact ticket. */
  captureContext(): SelectionReadTicket<C> {
    const ticket = Object.freeze({context: this.context});
    if (!this.disposed) this.readTickets.set(ticket, this.readGeneration);
    return ticket;
  }

  isContextCurrent(ticket: SelectionReadTicket<C>): boolean {
    return !this.disposed && this.readTickets.get(ticket) === this.readGeneration;
  }

  /** A route/query/sort, principal, restriction, catalog or viewer revision change ends selection. */
  setContext(input: C): boolean {
    if (this.disposed) return false;
    const next = this.policy.check(input);
    if (same(next, this.context)) return false;
    this.context = next;
    this.readGeneration++;
    this.operationGeneration++;
    this.windows.clear(); this.queries.clear(); this.registry = emptyRegistry();
    this.resetPicks();
    this.publish();
    return true;
  }

  registerWindow(ticket: SelectionReadTicket<C>, input: SelectionWindow): SelectionRegistration {
    if (this.disposed) return fail('disposed');
    if (!this.isContextCurrent(ticket)) return fail('stale-context');
    requireSelection(input && catalogueID(input.id) && catalogueID(input.sectionId)
      && selectionCount(input.sectionOrder) && selectionCount(input.start)
      && (input.queryId === undefined || catalogueID(input.queryId))
      && Array.isArray(input.items), 'invalid-window');
    requireSelection(input.items.length <= this.limits.maxWindowItems, 'registration-limit');
    requireSelection(Number.isSafeInteger(input.start + input.items.length), 'invalid-window');
    requireSelection(input.queryId === undefined || this.queries.has(input.queryId), 'invalid-window');
    const items = Object.freeze(input.items.map(item => {
      const value = checkedItem(item);
      return Object.freeze({key: this.policy.key(this.context, value), item: value});
    }));
    const value: SelectionWindow = Object.freeze({
      id: input.id, sectionId: input.sectionId, sectionOrder: input.sectionOrder, start: input.start,
      ...(input.queryId === undefined ? {} : {queryId: input.queryId}),
      items: Object.freeze(items.map(entry => entry.item)),
    });
    const registration = Object.freeze({value, items});
    const windows = new Map(this.windows);
    windows.set(value.id, registration);
    requireSelection(windows.size <= this.limits.maxWindows, 'registration-limit');
    const registry = this.buildRegistry(windows);
    this.validateQueryWindows(registry);
    // Commit only after all bounds and overlapping-window checks succeed.
    this.windows = windows; this.registry = registry;
    this.reconcilePicks();
    this.publish();
    return Object.freeze({ok: true, unregister: () => {
      // A replaced registration or a previous view's cleanup cannot remove the current one.
      if (this.disposed || this.windows.get(value.id) !== registration) return;
      const remaining = new Map(this.windows); remaining.delete(value.id);
      this.registry = this.buildRegistry(remaining); this.windows = remaining;
      this.reconcilePicks(); this.publish();
    }});
  }

  registerQuery(ticket: SelectionReadTicket<C>, input: SelectionQuery): SelectionRegistration {
    if (this.disposed) return fail('disposed');
    if (!this.isContextCurrent(ticket)) return fail('stale-context');
    const query = this.policy.query(input, this.context);
    requireSelection(this.queries.has(query.id) || this.queries.size < this.limits.maxQueries, 'registration-limit');
    const old = this.queries.get(query.id);
    if (old && !same(old, query)) {
      // Descriptor replacement cannot relabel old filtered results as members of the new set.
      this.dropQueryWindows(query.id);
      this.readGeneration++;
    }
    if (this.whole?.id === query.id) {
      if (!same(old, query)) { this.resetPicks(); this.operationGeneration++; }
      else this.whole = query;
    }
    this.queries.set(query.id, query);
    this.publish();
    return Object.freeze({ok: true, unregister: () => {
      if (this.disposed || this.queries.get(query.id) !== query) return;
      this.queries.delete(query.id);
      this.dropQueryWindows(query.id); this.readGeneration++;
      if (this.whole?.id === query.id) { this.resetPicks(); this.operationGeneration++; }
      this.publish();
    }});
  }

  getQuery(id: string): SelectionQuery | null { return this.queries.get(id) ?? null; }

  /** In query mode membership is asked only for registered, explicitly bound window entries. */
  isSelected(key: string): boolean {
    if (this.disposed) return false;
    if (!this.whole) return this.chosen.has(key);
    const item = this.registry.items.get(key)?.item;
    return this.registry.queryMembers.get(this.whole.id)?.has(key) === true
      && (this.wholeKind === null || item?.kind === this.wholeKind) && !this.excluded.has(key);
  }

  previewRange(windowId: string, key: string): SelectionRange {
    if (this.disposed) return fail('disposed');
    const target = this.windowPosition(windowId, key);
    if (!target) return fail('unregistered');
    if (this.whole && !this.isQueryMember(windowId, key)) return fail('not-query-member');
    const anchor = this.anchor;
    if (!anchor) return fail('no-anchor');
    if (anchor.position.sectionId !== target.sectionId) return fail('different-section');
    const section = this.registry.sections.get(target.sectionId);
    if (!section || section.positions.get(anchor.position.index)?.key !== anchor.key) return fail('no-anchor');
    const low = Math.min(anchor.position.index, target.index), high = Math.max(anchor.position.index, target.index);
    const length = high - low + 1;
    // Reject a huge sparse span before examining its bounded loaded positions.
    if (length > section.positions.size) return fail('range-not-loaded');
    const range = [...section.positions].filter(([index]) => index >= low && index <= high).sort(([a], [b]) => a - b);
    if (range.length !== length) return fail('range-not-loaded');
    const keys = [...new Set(range.map(([, entry]) => entry.key))];
    if (this.whole && keys.some(value => !this.registry.queryMembers.get(this.whole!.id)?.has(value)
      || this.wholeKind !== null && this.registry.items.get(value)!.item.kind !== this.wholeKind)) return fail('not-query-member');
    return Object.freeze({ok: true, keys: Object.freeze(keys)});
  }

  toggle(windowId: string, key: string, range = false): SelectionChange {
    if (this.disposed) return fail('disposed');
    const position = this.windowPosition(windowId, key);
    if (!position) return fail('unregistered');
    if (this.whole) {
      if (!this.isQueryMember(windowId, key)) return fail('not-query-member');
      if (range && this.anchor) {
        const preview = this.previewRange(windowId, key);
        if (!preview.ok) return preview;
        for (const rangeKey of preview.keys) this.excluded.delete(rangeKey);
      } else {
        if (this.excluded.has(key)) this.excluded.delete(key);
        else {
          if (this.excluded.size >= this.limits.maxQueryExclusions) return fail('exclusion-limit');
          this.excluded.set(key, this.registry.items.get(key)!);
        }
        this.anchor = Object.freeze({key, position});
      }
      this.lastSectionId = position.sectionId;
      if (this.querySummary().count === 0) this.resetPicks();
      this.operationGeneration++; this.publish();
      return success;
    }
    if (range && this.anchor) {
      const preview = this.previewRange(windowId, key);
      if (!preview.ok) return preview;
      for (const rangeKey of preview.keys) this.addPick(rangeKey, this.registry.positions.get(rangeKey)!);
    } else {
      if (this.chosen.has(key)) this.chosen.delete(key);
      else this.addPick(key, position);
      this.anchor = Object.freeze({key, position});
    }
    this.lastSectionId = position.sectionId;
    if (!this.chosen.size) this.resetPicks();
    this.operationGeneration++; this.publish();
    return success;
  }

  /** Cmd/Ctrl-A and section Select all: only current registered windows, never the whole query. */
  selectLoaded(sectionId?: string): SelectionChange {
    if (this.disposed) return fail('disposed');
    if (sectionId !== undefined && !this.registry.sections.has(sectionId)) return fail('unregistered');
    // This intent means bounded loaded content, even after a whole-query selection.
    if (this.whole) this.resetPicks();
    const sections = [...this.registry.sections].filter(([id]) => sectionId === undefined || id === sectionId)
      .sort(([idA, a], [idB, b]) => comparePosition({sectionId: idA, sectionOrder: a.order, index: 0}, {sectionId: idB, sectionOrder: b.order, index: 0}));
    for (const [id, section] of sections) {
      for (const [index, entry] of [...section.positions].sort(([a], [b]) => a - b)) {
        this.addPick(entry.key, {sectionId: id, sectionOrder: section.order, index});
      }
    }
    if (sectionId !== undefined) this.lastSectionId = sectionId;
    this.operationGeneration++; this.publish();
    return success;
  }

  /** Home's Select row means the section of the last actual pick, not every row on Home. */
  selectRow(): SelectionChange {
    if (this.disposed) return fail('disposed');
    return this.lastSectionId === null ? fail('unregistered') : this.selectLoaded(this.lastSectionId);
  }

  /** Only this explicit intent can turn registered content into a whole-result selection. */
  selectWholeQuery(queryId: string): SelectionChange {
    if (this.disposed) return fail('disposed');
    const query = this.queries.get(queryId);
    if (!query) return fail('unknown-query');
    this.resetPicks();
    if (query.total > 0) this.whole = query;
    this.operationGeneration++; this.publish();
    return success;
  }

  keepOnly(kind: SelectionKind): SelectionChange {
    if (this.disposed) return fail('disposed');
    if (this.whole) {
      if (this.wholeKind === kind) return success;
      // Keep only trims the current selection; it cannot bring an earlier type back.
      if (this.wholeKind !== null || !this.whole.summary.types.some(type => type.kind === kind)) this.resetPicks();
      else {
        if (this.whole.summary.types.length > 1) this.wholeKind = kind;
        for (const [key, value] of this.excluded) if (value.item.kind !== kind) this.excluded.delete(key);
        this.anchor = null;
        if (this.querySummary().count === 0) this.resetPicks();
      }
      this.operationGeneration++; this.publish();
      return success;
    }
    for (const [key, pick] of this.chosen) if (pick.item.kind !== kind) this.chosen.delete(key);
    if (this.anchor && !this.chosen.has(this.anchor.key)) this.anchor = null;
    if (!this.chosen.size) this.resetPicks();
    this.operationGeneration++; this.publish();
    return success;
  }

  /** Esc, Done, Cancel and Back use this same transition. Registrations remain usable. */
  clear(): void {
    if (this.disposed) return;
    this.resetPicks(); this.operationGeneration++; this.publish();
  }

  /** Capture the exact target before awaiting work; check again before applying its result. */
  captureOperation(): SelectionOperation<C> | null {
    if (this.disposed || this.snapshot.target === null) return null;
    const operation = Object.freeze({target: this.snapshot.target});
    this.operations.set(operation, this.operationGeneration);
    return operation;
  }

  isOperationCurrent(operation: SelectionOperation<C>): boolean {
    return !this.disposed && this.operations.get(operation) === this.operationGeneration;
  }

  /** A caller can cancel pending reads/results without pretending to undo a server write. */
  cancelPending(): void {
    if (this.disposed) return;
    this.readGeneration++; this.operationGeneration++;
  }

  dispose(): void {
    if (this.disposed) return;
    this.disposed = true;
    this.readGeneration++; this.operationGeneration++;
    this.windows.clear(); this.queries.clear(); this.registry = emptyRegistry(); this.resetPicks();
    this.publish(); this.listeners.clear();
  }

  private resetPicks(): void {
    this.chosen.clear(); this.whole = null; this.wholeKind = null; this.excluded.clear();
    this.anchor = null; this.lastSectionId = null;
  }

  private querySummary() {
    return summarizeQuerySelection(this.whole!.summary.types, this.wholeKind, this.excluded.values(), this.whole!.summary.unavailableActions);
  }

  private isQueryMember(windowId: string, key: string): boolean {
    const window = this.windows.get(windowId);
    const item = this.registry.items.get(key)?.item;
    return window?.value.queryId === this.whole?.id && item !== undefined
      && (this.wholeKind === null || item.kind === this.wholeKind);
  }

  private windowPosition(windowId: string, key: string): Position | null {
    const window = this.windows.get(windowId);
    if (!window) return null;
    const offset = window.items.findIndex(entry => entry.key === key);
    return offset < 0 ? null : Object.freeze({sectionId: window.value.sectionId, sectionOrder: window.value.sectionOrder, index: window.value.start + offset});
  }

  private addPick(key: string, position: Position): void {
    if (this.chosen.has(key)) return;
    const entry = this.registry.items.get(key)!;
    this.chosen.set(key, Object.freeze({...entry, position: Object.freeze(position)}));
  }

  private buildRegistry(windows: ReadonlyMap<string, RegisteredWindow>): Registry {
    const registry = emptyRegistry();
    for (const window of windows.values()) {
      const value = window.value;
      registry.itemCount += window.items.length;
      requireSelection(registry.itemCount <= this.limits.maxRegisteredItems, 'registration-limit');
      let section = registry.sections.get(value.sectionId);
      if (!section) {
        section = {order: value.sectionOrder, queryId: value.queryId, positions: new Map()};
        registry.sections.set(value.sectionId, section);
      }
      requireSelection(section.order === value.sectionOrder && section.queryId === value.queryId, 'conflicting-windows');
      for (let offset = 0; offset < window.items.length; offset++) {
        const entry = window.items[offset], index = value.start + offset;
        const occupant = section.positions.get(index), duplicate = registry.items.get(entry.key);
        requireSelection(!occupant || occupant.key === entry.key, 'conflicting-windows');
        requireSelection(!duplicate || same(duplicate.item, entry.item), 'conflicting-windows');
        section.positions.set(index, entry); registry.items.set(entry.key, entry);
        const position = Object.freeze({sectionId: value.sectionId, sectionOrder: value.sectionOrder, index});
        const first = registry.positions.get(entry.key);
        if (!first || comparePosition(position, first) < 0) registry.positions.set(entry.key, position);
        if (value.queryId !== undefined) {
          let members = registry.queryMembers.get(value.queryId);
          if (!members) { members = new Set(); registry.queryMembers.set(value.queryId, members); }
          members.add(entry.key);
        }
      }
    }
    return registry;
  }

  private validateQueryWindows(registry: Registry): void {
    for (const [queryId, keys] of registry.queryMembers) {
      const query = this.queries.get(queryId)!;
      const known = new Map([...keys].map(key => [key, registry.items.get(key)!]));
      // Evicted exclusions still consume aggregate facts. New windows cannot make
      // an impossible negative count or silently restore a deselected identity.
      if (this.whole?.id === queryId) {
        for (const [key, entry] of this.excluded) if (!known.has(key)) known.set(key, entry);
      }
      requireSelection(known.size <= query.total, 'invalid-window');
      for (const {item} of known.values()) {
        requireSelection(item.kind !== 'channel' && item.kind !== 'person'
          && query.summary.types.some(row => row.kind === item.kind), 'invalid-window');
      }
      for (const type of query.summary.types) {
        const items = [...known.values()].map(entry => entry.item).filter(item => item.kind === type.kind);
        requireSelection(items.length <= type.count, 'invalid-window');
        const libraries = new Map(type.libraries.map(row => [row.libraryId, row.count]));
        const observedLibraries = new Map<string, number>();
        for (const item of items) {
          requireSelection(item.kind !== 'channel' && item.kind !== 'person', 'invalid-window');
          const count = (observedLibraries.get(item.libraryId) ?? 0) + 1;
          observedLibraries.set(item.libraryId, count);
          requireSelection(count <= (libraries.get(item.libraryId) ?? 0), 'invalid-window');
        }
        for (const name of selectionCapabilityNames) {
          const offered = items.filter(item => item.capabilities[name]).length;
          requireSelection(offered <= type.capabilityCounts[name]
            && items.length - offered <= type.count - type.capabilityCounts[name], 'invalid-window');
        }
      }
    }
  }

  private dropQueryWindows(queryId: string): void {
    const windows = new Map([...this.windows].filter(([, window]) => window.value.queryId !== queryId));
    this.registry = this.buildRegistry(windows); this.windows = windows;
    this.reconcilePicks();
  }

  private reconcilePicks(): void {
    let changed = false;
    for (const [key, pick] of this.chosen) {
      const loaded = this.registry.items.get(key), position = this.registry.positions.get(key);
      if (loaded && position && (!same(loaded.item, pick.item) || comparePosition(position, pick.position) !== 0)) {
        this.chosen.set(key, Object.freeze({...loaded, position})); changed = true;
      }
    }
    for (const [key, entry] of this.excluded) {
      const loaded = this.registry.items.get(key);
      if (loaded && !same(loaded.item, entry.item)) {
        this.excluded.set(key, loaded); changed = true;
      }
    }
    if (this.anchor && this.registry.sections.get(this.anchor.position.sectionId)?.positions.get(this.anchor.position.index)?.key !== this.anchor.key) this.anchor = null;
    if (changed) this.operationGeneration++;
  }

  private publish(): void {
    let target: SelectionTarget<C> | null = null;
    let summary;
    if (this.whole) {
      const query = this.whole;
      summary = this.querySummary();
      target = Object.freeze({
        mode: 'query', context: this.context, queryId: query.id, selector: query.selector,
        total: summary.count, sourceTotal: query.total, summary,
        refinement: Object.freeze({kind: this.wholeKind, excludedItems: Object.freeze([...this.excluded.values()])}),
        expected: Object.freeze({catalogRevision: query.catalogRevision}),
      });
    } else {
      const picks = [...this.chosen.values()];
      const items = Object.freeze(picks.map(({key, item}) => Object.freeze({key, item})));
      summary = summarizeSelection(items.map(entry => entry.item));
      if (items.length > 0) {
        const displayItems = Object.freeze([...picks].sort((a, b) => comparePosition(a.position, b.position))
          .map(({key, item}) => Object.freeze({key, item})));
        const explicit: ExplicitSelectionTarget<C> = Object.freeze({mode: 'explicit', context: this.context, items, displayItems});
        target = explicit;
      }
    }
    this.snapshot = Object.freeze({
      version: ++this.version, context: this.context, active: summary.count > 0, summary, target,
      selectedKeys: Object.freeze([...this.chosen.keys()]), lastSectionId: this.lastSectionId,
      registeredWindowCount: this.windows.size, registeredItemCount: this.registry.itemCount,
    });
    for (const listener of [...this.listeners]) listener();
  }
}

/** Existing separate catalog/viewer revision source, retained unchanged. */
export class SelectionModel extends WindowedSelectionModel<SelectionContext> {
  constructor(context: SelectionContext, limits: Partial<SelectionLimits> = {}) {
    super(context, {check: checkedContext, key: (value, item) => selectionKey(value.scope, item), query: checkedQuery}, limits);
  }
}

/** Construct only after an actual nonempty Browse pageInfo.revision arrives.
 * Same-source explicit picks survive window eviction. Caller currentness and
 * cancellation still fence execution; no server aggregate is inferred here. */
export class BrowseSelectionModel extends WindowedSelectionModel<BrowseSelectionContext> {
  constructor(context: BrowseSelectionContext, limits: Partial<SelectionLimits> = {}) {
    super(context, {check: checkedBrowseContext, key: (value, item) => browseSelectionKey(value.scope, item),
      // A whole-view query is lawful only when the server's exact descriptor names this
      // page's own revision; checkedBrowseQuery refuses every other revision.
      query: checkedBrowseQuery}, limits);
  }
}
