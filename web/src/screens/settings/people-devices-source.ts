import type {AccessDevice, AdministrationPage} from '@core/server-administration.ts';

export type PeopleDevicesApi = Readonly<{
  list: (accountId: string, cursor: string, signal: AbortSignal) => Promise<AdministrationPage<AccessDevice>>;
  signOut: (accountId: string, deviceId: string | undefined, signal: AbortSignal) => Promise<void>;
}>;
type Snapshot = Readonly<{items: readonly AccessDevice[]; nextCursor: string; loading: boolean; busy: boolean; loaded: boolean; error?: unknown}>;
const empty: Snapshot = Object.freeze({items: [], nextCursor: '', loading: false, busy: false, loaded: false});

/** One person's tab owns its requests and confirmations. A new person, closed
 * tab or retired authority cannot publish data or sign out a former target. */
export class PersonDevicesSource {
  private state: Snapshot = empty;
  private readonly listeners = new Set<() => void>();
  private request?: AbortController;
  private closed = false;
  private readonly cursors = new Set<string>();
  constructor(readonly accountId: string, private readonly api: PeopleDevicesApi, private readonly isCurrent: () => boolean, private readonly canSignOut: () => boolean) {}
  getSnapshot = () => this.state;
  subscribe = (listener: () => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  private current() {return !this.closed && this.isCurrent();}
  private publish(state: Snapshot) {if (!this.current()) return; this.state = Object.freeze(state); for (const listener of this.listeners) listener();}
  private check(request: AbortController) {if (!this.current() || this.request !== request || request.signal.aborted) throw new DOMException('Person devices retired', 'AbortError');}
  private validate(page: AdministrationPage<AccessDevice>, cursor: string) {
    if (!Number.isSafeInteger(page.limit) || page.limit < 1 || page.limit > 100 || page.items.length > page.limit ||
      page.items.some(device => device.accountId !== this.accountId) || new Set(page.items.map(device => device.id)).size !== page.items.length ||
      page.nextCursor && (page.nextCursor === cursor || this.cursors.has(page.nextCursor))) throw new Error('invalid_person_devices');
  }
  async load(more = false): Promise<void> {
    if (!this.current() || this.request || more && (!this.state.loaded || !this.state.nextCursor)) return;
    const cursor = more ? this.state.nextCursor : '', before = this.state;
    if (!more) this.cursors.clear();
    const request = this.request = new AbortController();
    this.publish({...before, loading: true, error: undefined});
    try {
      const page = await this.api.list(this.accountId, cursor, request.signal); this.check(request); this.validate(page, cursor);
      if (cursor) this.cursors.add(cursor);
      const existing = new Set(more ? before.items.map(device => device.id) : []);
      this.publish({items: Object.freeze([...(more ? before.items : []), ...page.items.filter(device => !existing.has(device.id))]), nextCursor: page.nextCursor, loaded: true, loading: false, busy: false});
    } catch (error) {if (this.current() && !request.signal.aborted) this.publish({...before, loading: false, error}); throw error;}
    finally {if (this.request === request) this.request = undefined;}
  }
  async signOut(device?: AccessDevice): Promise<void> {
    if (!this.current() || !this.canSignOut() || this.request || !this.state.loaded || device && !this.state.items.includes(device)) throw new Error('person_device_changed');
    const before = this.state, request = this.request = new AbortController();
    this.publish({...before, busy: true, error: undefined});
    try {
      this.check(request);
      await this.api.signOut(this.accountId, device?.id, request.signal); this.check(request);
      const page = await this.api.list(this.accountId, '', request.signal); this.check(request);
      this.cursors.clear(); this.validate(page, '');
      this.publish({items: page.items, nextCursor: page.nextCursor, loaded: true, loading: false, busy: false});
    } catch (error) {if (this.current() && !request.signal.aborted) this.publish({...before, busy: false, error}); throw error;}
    finally {if (this.request === request) this.request = undefined;}
  }
  dispose() {this.closed = true; this.request?.abort(); this.request = undefined; this.state = empty; this.listeners.clear(); this.cursors.clear();}
}
