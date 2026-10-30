import {DetailService} from '@core/detail.ts';
import {HomeSelectionDestinations} from '@core/home-selection/destinations.ts';
import type {HomeSelectionPicker} from '@core/home-selection/types.ts';
import type {SelectionTarget} from '@core/selection-model/index.ts';
import type {EntryActionPorts, EntryActionTarget, EntryDestination} from './entry-actions';

/** A private checked destination session, never Saved's visible route or store. */
export class EntryPickerProducer {
  private readonly lifetime = new AbortController();
  private readonly detail: DetailService;
  private session?: HomeSelectionDestinations;
  private flight?: Promise<void>;
  private state?: HomeSelectionPicker;
  constructor(private readonly target: EntryActionTarget, private readonly kind: EntryDestination,
    private readonly ports: EntryActionPorts, private readonly canCreate: boolean, private readonly publish: (state: HomeSelectionPicker) => void) {
    const api = {request: async <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
      this.check();
      const value = await ports.api.request<T>(path, method, body, signal ? AbortSignal.any([signal, this.lifetime.signal]) : this.lifetime.signal);
      this.check(); return value;
    }};
    this.api = api;
    this.detail = new DetailService({api, scope: {serverId: ports.serverId, viewerId: ports.viewerId}, requestId: ports.requestId});
  }
  private readonly api: EntryActionPorts['api'];
  private current = () => !this.lifetime.signal.aborted && this.ports.isCurrent();
  private check() {if (!this.current()) throw new DOMException('Picker authority changed', 'AbortError');}
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
    await this.detail.select({libraryId: this.target.libraryId, itemId: this.target.itemId}); this.check();
    const data = this.detail.getSnapshot().data;
    if (this.detail.getSnapshot().phase !== 'ready' || !data || data.item.id !== this.target.itemId || data.scope.libraryId !== this.target.libraryId
      || data.item.kind !== this.target.kind || !['movie', 'episode', 'song', 'audiobook_file'].includes(data.item.kind)) throw new Error('picker-leaf-required');
    const playlist = data.actions.some(action => action.id === 'add_to_playlist' && action.enabled);
    const collection = data.actions.some(action => action.id === 'add_to_collection' && action.enabled);
    if (!(this.kind === 'playlist' ? playlist : collection)) throw new Error('picker-action-not-permitted');
    const item = Object.freeze({kind: this.target.kind, id: this.target.itemId, libraryId: this.target.libraryId,
      capabilities: Object.freeze({play: false, queue: false, playlist, collection, myList: false, watched: false, favorite: false, download: false, edit: false, refresh: false, delete: false})});
    const pick = Object.freeze({key: JSON.stringify([item.libraryId, item.kind, item.id]), item});
    const target: SelectionTarget = Object.freeze({mode: 'explicit', items: Object.freeze([pick]), displayItems: Object.freeze([pick]), context: {
      scope: {serverId: this.ports.serverId, authority: identity.authority, accountId: identity.accountId, profileId: identity.profileId,
        viewId: JSON.stringify(['entry-picker', this.target.libraryId, this.target.itemId]), privacyFence: data.scope.viewerFence},
      revision: {catalogRevision: String(data.revision.catalog), viewerRevision: String(data.revision.viewer)}}});
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
