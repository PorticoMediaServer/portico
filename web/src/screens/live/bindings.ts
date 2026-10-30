import {GuideWindowStore, legacyChannel, legacyGuideSource, type GuideChannelRow} from '@core/guide/index.ts';
import {fetchGuideSourceSummaries} from '@core/guide/directory.ts';
import type {LegacyGuideOptions} from '@core/guide/legacy-source.ts';
import {channelOperationId, guideWindow, parseChannelGuide, saveChannelPreference, type ChannelKind} from '@core/channel-guide.ts';
import {setServerChannelHidden} from '@core/server-channel-visibility.ts';
import {routePath} from '../../../../packages/contracts/src/routes.ts';
import {screenLifetime, type ScreenContext} from '../detail/context';

/** Only the mounted viewport starts reads. No source listing or guide walk at import. */
export function createLiveBindings(context: ScreenContext, options: LegacyGuideOptions) {
  const life = screenLifetime(context);
  let store: GuideWindowStore;
  const stores = new Set<GuideWindowStore>();
  let failure: unknown;
  const listeners = new Set<() => void>();
  const api = {...life.api, async request<T>(...args: Parameters<typeof life.api.request>) {
    try {return await life.api.request<T>(...args);}
    catch (error) {
      const status = (error as {status?: number})?.status;
      if (status === 401 || status === 403) {failure = error;life.dispose();for (const value of stores) value.dispose();for (const listener of listeners) listener();}
      throw error;
    }
  }};
  const source = legacyGuideSource(api, context.scope.serverId, Object.freeze({...options}));
  store = new GuideWindowStore({source, channelPageSize: 50, maxChannelPages: 6, maxTiles: 12, maxInFlight: 1, marginPages: 0, marginBlocks: 0});
  stores.add(store);
  return {store, life, failure: {getSnapshot: () => failure, subscribe: (listener: () => void) => {listeners.add(listener);return () => {listeners.delete(listener);};}},
    /** This second order is evaluated by the server over the same filtered
     * source, never by sorting the loaded Guide page. Construction is inert. */
    onNow() {
      life.assert();
      const source = legacyGuideSource(api, context.scope.serverId, Object.freeze({...options, sort: 'favorites'}));
      const current = new GuideWindowStore({source, channelPageSize: 50, maxChannelPages: 6, maxTiles: 6, maxInFlight: 1, marginPages: 0, marginBlocks: 0});
      stores.add(current);
      return {store: current, dispose() {current.dispose();stores.delete(current);}};
    },
    async sources() {
      const value = await fetchGuideSourceSummaries(api, context.scope.serverId, options.timezone, life.signal(), options.includeServerHidden);
      life.assert();return value;
    },
    async refresh(signal?: AbortSignal) {
      const active = life.signal(signal);active.throwIfAborted();
      const value = await fetchGuideSourceSummaries(api, context.scope.serverId, options.timezone, active, options.includeServerHidden);
      life.assert();active.throwIfAborted();
      // Membership, marks and favorites order may all have changed. Forget the
      // prior revision and re-read only each store's current bounded viewport.
      for (const current of stores) current.reset();
      return value;
    },
    async selectedChannel(target: Readonly<{sourceId: string; channelId: string; provenance: ChannelKind}>, signal?: AbortSignal) {
      const route = {kind: target.provenance, sourceId: target.sourceId, ...(options.includeServerHidden ? {includeServerHidden: true} : {}), timezone: options.timezone, search: '', ...guideWindow()};
      const params = new URLSearchParams({...route, includeServerHidden: String(options.includeServerHidden === true), channels: target.channelId, limit: '1'});
      const value = parseChannelGuide(await api.request(routePath('get_guide') + '?' + params, 'GET', undefined, life.signal(signal)), context.scope.serverId, route);
      life.assert();signal?.throwIfAborted();
      return value.channels.find(channel => channel.id === target.channelId && channel.sourceId === target.sourceId && channel.provenance === target.provenance);
    },
    /** The viewer's favorite for one drawn row, retaining its existing profile hidden
     * preference at that row's revision. The caller re-reads the guide afterwards; nothing is patched locally. */
    async channelPreference(row: GuideChannelRow, values: Readonly<{favorite: boolean; hidden: boolean}>) {
      const channel = legacyChannel(row);
      if (!channel) throw new Error('This channel is no longer in the guide.');
      life.assert();
      const operation = await channelOperationId(() => Promise.resolve(crypto.randomUUID()));
      await saveChannelPreference(api, context.scope.serverId, channel, operation, values, life.signal());
      life.assert();
    },
    async channelHidden(row: GuideChannelRow, hidden: boolean, expected?: Readonly<{revision: number; hidden: boolean}>) {
      const channel = legacyChannel(row);
      if (!channel || row.kind === 'library') throw new Error('invalid_channel_kind');
      return setServerChannelHidden(api, context.scope.serverId, channel.sourceId, channel.id, hidden, () => crypto.randomUUID(), life.signal(), () => context.isCurrent(), expected);
    },
    dispose() {life.dispose();for (const value of stores) value.dispose();stores.clear();listeners.clear();failure = undefined;},
  };
}
export type LiveBindings = ReturnType<typeof createLiveBindings>;
