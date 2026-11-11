import {selectionQuerySelector} from '@core/selection-model/index.ts';
import {fetchDestinationAuthority, parseHomeDestinationPicker, readQueryDestinationPickerResponse} from '@core/home-selection/destination-picker.ts';
import {DetailService} from '@core/detail.ts';
import {HomeSelectionDestinations} from '@core/home-selection/destinations.ts';
import type {HomeSelectionPicker} from '@core/home-selection/types.ts';
import type {CatalogueSelectionIdentity, SelectionItem, SelectedItem, SelectionTarget, QuerySelectionTarget, BrowseSelectionContext} from '@core/selection-model/index.ts';
import type {EntryActionPorts, EntryDestination} from '../../shell/entry-actions';

export type LibraryPickerTarget = readonly SelectionItem[] | QuerySelectionTarget<BrowseSelectionContext>;
const queryTarget = (target: LibraryPickerTarget): target is QuerySelectionTarget<BrowseSelectionContext> => 'mode' in target;

export function queryDestinationTarget<C>(target: QuerySelectionTarget<C>, kind: EntryDestination): boolean {
  if (!target.total || !target.summary.every[kind] || target.summary.library.kind !== 'single'
    || !/^browse_[a-f0-9]{64}$/.test(target.expected.catalogRevision)
    || !target.summary.types.length || target.summary.types.some(type => !['movie', 'episode', 'song', 'audiobook_file'].includes(type.kind))) return false;
  const checked = selectionQuerySelector(target);
  return checked.ok && 'query' in checked.selector && checked.selector.query.libraryId === target.summary.library.libraryId;
}

/** A private checked destination session, never Saved's visible route or store. */
export class LibraryPickerProducer {
  private readonly lifetime = new AbortController();
  private readonly detail: DetailService;
  private session?: HomeSelectionDestinations;
  private flight?: Promise<void>;
  private state?: HomeSelectionPicker;
  constructor(private readonly targets: LibraryPickerTarget, private readonly kind: EntryDestination,
    private readonly ports: EntryActionPorts, private readonly canCreate: boolean, private readonly publish: (state: HomeSelectionPicker) => void) {
    const api = {request: async <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
      this.check();
      const value = await ports.api.request<T>(path, method, body, signal ? AbortSignal.any([signal, this.lifetime.signal]) : this.lifetime.signal);
      this.check(); return value;
    }};
    this.api = api;
    this.detail = new DetailService({queries: ports.queries, api, scope: {serverId: ports.serverId, viewerId: ports.viewerId}, requestId: ports.requestId});
  }
  private readonly api: EntryActionPorts['api'];
  private current = () => !this.lifetime.signal.aborted && this.ports.isCurrent();
  private check() {
    if (!this.current()) throw new DOMException('Picker authority changed', 'AbortError');
    if (this.kind === 'playlist' && queryTarget(this.targets) && this.targets.summary.unavailableActions?.playlist) throw new Error('query-destination-unavailable');
  }
  private update = (state: HomeSelectionPicker) => {if (this.current()) {this.state = state; this.publish(state);}};
  private run(work: () => Promise<void>): Promise<void> {
    this.check();
    if (this.flight) return this.flight;
    this.flight = Promise.resolve().then(async () => {try {this.check(); await work(); this.check();} finally {this.flight = undefined;}});
    return this.flight;
  }
  open() {return this.run(async () => {
    const identity = this.ports.pickerIdentity;
    if (!identity) throw new Error('picker-identity-required');
    if (queryTarget(this.targets)) {
      if (!queryDestinationTarget(this.targets, this.kind)) throw new Error('query-destination-unavailable');
      const selected = structuredClone(this.targets);
      const request = {kind: this.kind, query: '', limit: 40};
      const raw = await readQueryDestinationPickerResponse(this.api, request, this.lifetime.signal); this.check();
      if (!raw || typeof raw !== 'object' || !('viewerFence' in raw) || typeof raw.viewerFence !== 'string' || !/^[a-f0-9]{64}$/.test(raw.viewerFence)) throw new Error('picker-authority-required');
      const page = parseHomeDestinationPicker(raw, request, this.ports.serverId, raw.viewerFence);
      const viewId = await this.ports.requestId(); this.check();
      const target: SelectionTarget = {...structuredClone(selected), context: {
        scope: {serverId: this.ports.serverId, authority: identity.authority, accountId: identity.accountId, profileId: identity.profileId,
          viewId, privacyFence: raw.viewerFence}, revision: {catalogRevision: String(page.revision.catalog), viewerRevision: String(page.revision.viewer)}}};
      this.session = new HomeSelectionDestinations({api: this.api, events: identity.events, requestId: this.ports.requestId}, target, this.kind,
        this.lifetime.signal, this.current, this.update);
      await this.session.load();return;
    }
    const targets = this.targets;
    if (!targets.length || targets.length > 256) throw new Error('picker-target-required');
    if (!targets.every((item): item is SelectionItem & CatalogueSelectionIdentity => item.kind !== 'channel' && item.kind !== 'person' && 'libraryId' in item)) throw new Error('picker-action-not-permitted');
    if (new Set(targets.map(item => item.id)).size !== targets.length) throw new Error('picker-target-required');
    const read = async (selected: SelectionItem & CatalogueSelectionIdentity, fresh: boolean) => {
      await this.detail.select({libraryId: selected.libraryId, itemId: selected.id}, fresh ? {fresh: true} : {}); this.check();
      const data = this.detail.getSnapshot().data;
      if (this.detail.getSnapshot().phase !== 'ready' || !data || data.item.id !== selected.id || data.scope.libraryId !== selected.libraryId
        || data.item.kind !== selected.kind || !['movie', 'episode', 'song', 'audiobook_file'].includes(data.item.kind)) throw new Error('picker-leaf-required');
      return data;
    };
    type Leaf = {selected: SelectionItem & CatalogueSelectionIdentity; data?: Awaited<ReturnType<typeof read>>};
    const members: Leaf[] = [];
    for (const selected of targets) {
      if (!('libraryId' in selected) || selected.capabilities[this.kind] !== true) throw new Error('picker-action-not-permitted');
      members.push(['show', 'season', 'album', 'artist', 'book', 'collection'].includes(selected.kind) ? {selected} : {selected, data: await read(selected, false)});
    }
    // The leaves' reads must agree with each other. When they don't (the viewer's access was decided again while they
    // were read, or the cache held an older page of one of them), every leaf is read again once, from the server, and
    // only then compared: no read is trusted as the newest. Leaves that still disagree show picker-selection-changed.
    const answer = (_leaf: Leaf) => '';
    const leaves = members.filter(leaf => leaf.data), agree = () => leaves.every(leaf => answer(leaf) === answer(leaves[0]!));
    if (!agree()) {
      for (const leaf of leaves) leaf.data = await read(leaf.selected, true);
      if (!agree()) throw new Error('picker-selection-changed');
    }
    const picks: SelectedItem[] = members.map(({selected, data}) => {
      if (!data) return Object.freeze({key: JSON.stringify([selected.libraryId, selected.kind, selected.id]), item: selected});
      const playlist = data.actions.some(action => action.id === 'add_to_playlist' && action.enabled);
      const collection = data.actions.some(action => action.id === 'add_to_collection' && action.enabled);
      if (!(this.kind === 'playlist' ? playlist : collection)) throw new Error('picker-action-not-permitted');
      const item = Object.freeze({...selected, capabilities: Object.freeze({...selected.capabilities, playlist, collection})});
      return Object.freeze({key: JSON.stringify([item.libraryId, item.kind, item.id]), item});
    });
    if (!picks.length) throw new Error('picker-target-required');
    // The detail reads above only proved each leaf may be added (and agree with each other). The destination session
    // holds the fence the destination read itself issues: never a detail's, the Search token or a descendant read
    // (walk J-11, J-13: substituting the detail's fence made every add say "That list could not be updated.").
    const authority = await fetchDestinationAuthority(this.api, this.kind, this.ports.serverId, this.lifetime.signal); this.check();
    const viewId = await this.ports.requestId(); this.check();
    const target: SelectionTarget = Object.freeze({mode: 'explicit', items: Object.freeze(picks), displayItems: Object.freeze(picks), context: {
      scope: {serverId: this.ports.serverId, authority: identity.authority, accountId: identity.accountId, profileId: identity.profileId,
        viewId, privacyFence: authority.viewerFence},
      revision: {catalogRevision: String(authority.revision.catalog), viewerRevision: String(authority.revision.viewer)}}});
    this.session = new HomeSelectionDestinations({api: this.api, events: identity.events, requestId: this.ports.requestId}, target, this.kind,
      this.lifetime.signal, this.current, this.update);
    await this.session.load();
  });}
  private required() {this.check(); if (!this.session) throw new Error('picker-not-ready'); return this.session;}
  search(query: string) {return this.run(() => this.required().search(query));}
  page(previous = false) {return this.run(() => this.required().page(previous));}
  choose(id: string) {return this.run(async () => {
    if (!this.state?.rows.some(row => row.id === id && row.canAdd)) throw new Error('destination-not-offered');
    await this.required().choose(id);
  });}
  create(name: string) {return this.run(async () => {
    if (this.kind === 'collection' && !this.canCreate) throw new Error('collection-owner-only');
    await this.required().create(name);
  });}
  retry() {return this.session ? this.run(() => this.required().retry()) : this.open();}
  close() {this.lifetime.abort(); this.session?.dispose(); this.detail.dispose();}
}
