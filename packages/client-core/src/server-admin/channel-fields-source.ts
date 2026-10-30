import {linkAbortSignals, throwIfAborted} from '../abort-lifetime.ts';
import type {Library} from '../index.ts';
import type {ChannelApi} from '../channel-guide.ts';
import {browseCapabilitiesPath, browseContextFacetsPath, browsePath, parseBrowseCapabilities, parseBrowseFacets, parseBrowseResult, type BrowseFieldCapability, type BrowseFacetValue} from '../browse.ts';
import {parseMediaItem} from '../media-item.ts';
import {channelConditionFields} from '../server-admin/channel-conditions.ts';
import {encodedRoutePath} from '../../../contracts/src/route-paths.ts';

export type ChannelField = Readonly<{libraryId: string; pivot: 'movies' | 'shows'; field: BrowseFieldCapability}>;
export type PinTitle = Readonly<{id: string; title: string; kind: string; libraryId: string}>;
type Snapshot = Readonly<{phase: 'idle' | 'loading' | 'ready' | 'error'; fields: readonly ChannelField[]}>;
const empty: Snapshot = Object.freeze({phase: 'idle', fields: Object.freeze([])});
const terminal = (error: unknown) => {const value = (error && typeof error === 'object' ? error : {}) as {status?: number; code?: string};return value.status === 401 || value.status === 403 || ['unauthorized', 'forbidden', 'not_permitted'].includes(value.code ?? '');};
const ended = () => Object.assign(new Error('The channel field scope ended.'),{name:'AbortError'});

/** The editor's shared metadata vocabulary. Every URL is a fixed registered
 * browse/item route; server-authored facet endpoint strings are never fetched. */
export class ChannelFieldsSource {
  private state = empty;
  private readonly lifetime = new AbortController();
  private readonly listeners = new Set<() => void>();
  private scope?: AbortController;
  private selected: readonly Library[] = [];
  private key = '';
  private searchRead?: AbortController;
  private readonly searchMembers = new WeakMap<PinTitle, AbortController>();
  private readonly titles = new Map<string, PinTitle>();
  private readonly valueLabels = new Map<string, string>();
  constructor(private readonly api: ChannelApi, private readonly allowed: readonly Library[], private readonly isCurrent: () => boolean, private readonly reportError: (error: unknown) => void) {}
  private async request<T = unknown>(signals: readonly (AbortSignal | undefined)[], path: string, method?: string, body?: unknown): Promise<T> {
    const lifetime = linkAbortSignals(signals);
    try {throwIfAborted(lifetime.signal); return await this.api.request<T>(path, method, body, lifetime.signal);}
    finally {lifetime.dispose();}
  }
  private current() {return !this.lifetime.signal.aborted && this.isCurrent();}
  private owns(scope: AbortController) {return this.current() && this.scope === scope && !scope.signal.aborted;}
  private publish(state: Snapshot) {if (this.current()) {this.state = Object.freeze(state);for (const listener of this.listeners) listener();}}
  private failure(error: unknown) {if (this.current() && terminal(error)) {this.dispose();this.reportError(error);}}
  getSnapshot = () => this.current() ? this.state : empty;
  subscribe = (listener: () => void) => {this.listeners.add(listener);return () => {this.listeners.delete(listener);};};
  select = async (ids: readonly string[], force = false): Promise<void> => {
    if (!this.current()) return;
    const selected = this.allowed.filter(library => ids.slice(0, 64).includes(library.id) && ['movie', 'tv', 'anime'].includes(library.kind));
    const key = JSON.stringify(selected.map(library => library.id));
    if (!force && key === this.key && this.scope) return;
    this.scope?.abort();this.cancelSearch();const scope = new AbortController();this.scope = scope;this.selected = selected;this.key = key;
    this.publish({phase: 'loading', fields: Object.freeze([])});
    if (!this.owns(scope)) return;
    const representatives = [selected.find(library => library.kind === 'movie'), selected.find(library => library.kind !== 'movie')].filter((library): library is Library => !!library);
    try {
      const lists = await Promise.all(representatives.map(async library => {
        if (!this.owns(scope)) throw ended();
        const pivot = library.kind === 'movie' ? 'movies' as const : 'shows' as const;
        const raw = await this.request<unknown>([scope.signal, this.lifetime.signal], browseCapabilitiesPath(library.id, pivot), 'GET', undefined);
        if (!this.owns(scope)) throw ended();
        const capabilities = parseBrowseCapabilities(raw);
        if (capabilities.library.id !== library.id) throw new Error('invalid_channel_field_scope');
        return channelConditionFields(capabilities.fields).map(field => Object.freeze({libraryId: library.id, pivot, field}));
      }));
      if (!this.owns(scope)) return;
      const fields = new Map<string, ChannelField>();for (const field of lists.flat()) if (!fields.has(field.field.id)) fields.set(field.field.id, field);
      this.publish({phase: 'ready', fields: Object.freeze([...fields.values()])});
    } catch (error) {if (this.owns(scope)) {this.publish({phase: 'error', fields: Object.freeze([])});this.failure(error);}}
  };
  refresh = () => this.select(this.selected.map(library => library.id), true);
  values = async (entry: ChannelField, query: string, caller: AbortSignal): Promise<readonly BrowseFacetValue[]> => {
    const scope = this.scope;
    if (!scope || !this.owns(scope) || caller.aborted || !this.state.fields.includes(entry) || query.length > 200) throw ended();
    const signals = [scope.signal, this.lifetime.signal, caller];
    try {
    const raw = await this.request<unknown>(signals, browseContextFacetsPath(entry.libraryId, entry.field.id, entry.pivot, undefined, query, 200, 0), 'GET', undefined);
    if (!this.owns(scope) || caller.aborted || !this.state.fields.includes(entry)) throw ended();
    const page = parseBrowseFacets(raw, entry.field.id);
    for (const value of page.values) {this.valueLabels.set(`${entry.libraryId}/${entry.field.id}/${value.value}`, value.label);if (this.valueLabels.size > 4096) this.valueLabels.delete(this.valueLabels.keys().next().value!);}
    return Object.freeze([...page.values]);
    } catch (error) {if (this.owns(scope) && !caller.aborted) this.failure(error);throw error;}
  };
  valueLabel = (entry: ChannelField, value: string) => this.current() ? this.valueLabels.get(`${entry.libraryId}/${entry.field.id}/${value}`) : undefined;
  cancelSearch = () => {this.searchRead?.abort();this.searchRead = undefined;};
  search = async (query: string, caller: AbortSignal): Promise<readonly PinTitle[]> => {
    if (!this.current() || caller.aborted) return [];
    this.cancelSearch();const scope = this.scope;
    if (!scope || !this.owns(scope) || caller.aborted || query.trim().length < 2 || query.length > 200 || /[\x00-\x1f\x7f]/.test(query)) return [];
    const search = new AbortController();this.searchRead = search;
    const current = () => this.owns(scope) && this.searchRead === search && !search.signal.aborted && !caller.aborted;
    const signals = [scope.signal, search.signal, this.lifetime.signal, caller];
    const selected = [...this.selected], results: PinTitle[][] = selected.map(() => []);let offset = 0;
    try {
      await Promise.all(Array.from({length: Math.min(4, selected.length)}, async () => {
        for (;;) {
          const index = offset++, library = selected[index];if (!library || !current()) return;
          const raw = await this.request<unknown>(signals, browsePath(library.id), 'POST', {pivot: library.kind === 'movie' ? 'movies' : 'shows', query: {field: 'title', operator: 'contains', value: query.trim()}, limit: 6});
          if (!current()) return;
          const page = parseBrowseResult(raw);
          for (const entry of page.entries) {
            if (!['movie', 'show'].includes(entry.kind) || !entry.id || !entry.title || entry.title.length > 512 || entry.libraryId && entry.libraryId !== library.id) continue;
            const title = Object.freeze({id: entry.id, title: entry.title, kind: entry.kind, libraryId: library.id});results[index]!.push(title);this.remember(title);
          }
        }
      }));
      if (!current()) return [];
      const seen = new Set<string>(), page = results.flat().filter(title => !seen.has(title.id) && !!seen.add(title.id)).slice(0, 10);
      for (const title of page) this.searchMembers.set(title, search);
      return Object.freeze(page);
    } catch (error) {if (current()) this.failure(error);throw error;}
  };
  isSearchResult = (title: PinTitle) => this.current() && this.searchRead !== undefined && !this.searchRead.signal.aborted && this.searchMembers.get(title) === this.searchRead;
  private remember(title: PinTitle) {this.titles.delete(title.id);this.titles.set(title.id, title);if (this.titles.size > 2048) this.titles.delete(this.titles.keys().next().value!);}
  title = (id: string) => this.current() ? this.titles.get(id) : undefined;
  /** At most twenty selected titles per explicit page, with four requests in flight. */
  labels = async (ids: readonly string[], caller: AbortSignal): Promise<void> => {
    const scope = this.scope;if (!scope || !this.owns(scope) || caller.aborted) return;
    const pending = ids.slice(0, 20).filter(id => /^[A-Za-z0-9_-]{1,128}$/.test(id) && !this.titles.has(id));let offset = 0;
    const current = () => this.owns(scope) && !caller.aborted;
    const signals = [scope.signal, this.lifetime.signal, caller];
    await Promise.all(Array.from({length: Math.min(4, pending.length)}, async () => {
      for (;;) {
        const id = pending[offset++];if (!id || !current()) return;
        try {
          const raw = await this.request<unknown>(signals, encodedRoutePath('get_items_id', {id: encodeURIComponent(id)}), 'GET', undefined);
          if (!current()) return;
          const item = parseMediaItem(raw);
          if (item.id !== id || !this.allowed.some(library => library.id === item.libraryId) || !['movie', 'show', 'episode'].includes(item.kind) || !item.title || item.title.length > 512) continue;
          this.remember(Object.freeze({id, title: item.title, kind: item.kind, libraryId: item.libraryId}));
        } catch (error) {if (current()) this.failure(error);}
      }
    }));
  };
  dispose = () => {this.scope?.abort();this.cancelSearch();this.lifetime.abort();this.selected = [];this.titles.clear();this.valueLabels.clear();this.state = empty;this.listeners.clear();};
}
