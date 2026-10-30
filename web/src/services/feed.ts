import type {AccountNotification, AccountNotificationsClient, NotificationPreferences} from '@core/account-notifications.ts';

export type FeedSnapshot = Readonly<{items: readonly AccountNotification[]; unread: number; cursor?: string; preferences?: NotificationPreferences; loading: boolean; busy: boolean; error?: unknown}>;
/** Presentation pagination only. The existing client parses genuine Hosted records and owns routes. */
export class ServicesFeed {
  private state: FeedSnapshot = {items: [], unread: 0, loading: true, busy: false};
  private listeners = new Set<() => void>();
  private operation?: AbortController;
  private disposed = false;
  constructor(readonly client: AccountNotificationsClient) {}
  getSnapshot = () => this.state;
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  private publish(value: Partial<FeedSnapshot>) { if (this.disposed) return; this.state = {...this.state, ...value}; this.listeners.forEach(listener => listener()); }
  dispose() { this.disposed = true; this.operation?.abort(); this.listeners.clear(); }
  private async run(work: (signal: AbortSignal) => Promise<void>) {
    if (this.disposed || this.operation) return;
    const controller = new AbortController(); this.operation = controller; this.publish({busy: true, error: undefined});
    try { await work(controller.signal); }
    catch (error) { if (!controller.signal.aborted) this.publish({error}); }
    finally { if (this.operation === controller) this.operation = undefined; if (!controller.signal.aborted) this.publish({loading: false, busy: false}); }
  }
  refresh() {
    return this.run(async signal => {
      const [page, preferences] = await Promise.allSettled([this.client.page(undefined, signal), this.client.preferences(signal)]);
      if (signal.aborted) return;
      if (preferences.status === 'fulfilled') this.publish({preferences: preferences.value});
      if (page.status === 'rejected') throw page.reason;
      this.publish({items: page.value.items, cursor: page.value.nextCursor, unread: page.value.unread});
      if (preferences.status === 'rejected') throw preferences.reason;
    });
  }
  older() {
    return this.run(async signal => {
      const cursor = this.state.cursor; if (!cursor) return;
      const page = await this.client.page(cursor, signal); if (signal.aborted) return;
      const known = new Set(this.state.items.map(item => item.id));
      this.publish({items: [...this.state.items, ...page.items.filter(item => !known.has(item.id))], cursor: page.nextCursor, unread: page.unread});
    });
  }
  markRead(ids: readonly string[] | 'all') {
    return this.run(async signal => {
      await this.client.markRead(ids); if (signal.aborted) return;
      // Read back the authority's unread count, including changes made on other devices.
      const page = await this.client.page(undefined, signal); if (signal.aborted) return;
      this.publish({items: page.items, cursor: page.nextCursor, unread: page.unread});
    });
  }
  savePreferences(serverOffline: boolean) {
    return this.run(async signal => {
      const preferences = await this.client.savePreferences({serverOffline});
      if (!signal.aborted) this.publish({preferences});
    });
  }
}
