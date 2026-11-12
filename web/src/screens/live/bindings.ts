import {GuideWindowStore, type GuideChannelRow} from '@core/guide/index.ts';
import {findChannel, nextGuideSource, nextGuideSummaries, nextOnNowSource} from '@core/guide/next-source.ts';
import type {LegacyGuideOptions} from '@core/guide/legacy-source.ts';
import {readSelectedChannelGuideResponse} from '@core/query/browse-reads.ts';
import {guideWindow, parseChannelGuide, type ChannelKind} from '@core/channel-guide.ts';
import {markChannel, type ChannelChoice} from '@core/live-tv.ts';
import type {setServerChannelHidden} from '@core/server-channel-visibility.ts';
import {screenLifetime, type ScreenContext} from '../detail/context';

/** The page's filters as the new server's channel choice: one source, one group, favourites, hidden ones too. */
const channelChoice = (options: LegacyGuideOptions): ChannelChoice => ({...(options.favorites ? {favorites: true} : {}), ...(options.includeHidden ? {hidden: true} : {}),
  ...(options.sourceId ? {source: options.sourceId} : {}), ...(options.group ? {group: options.group} : {})});

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
  const source = nextGuideSource(api, channelChoice(options));
  store = new GuideWindowStore({source, channelPageSize: 50, maxChannelPages: 6, maxTiles: 12, maxInFlight: 1, marginPages: 0, marginBlocks: 0});
  stores.add(store);
  return {store, life, failure: {getSnapshot: () => failure, subscribe: (listener: () => void) => {listeners.add(listener);return () => {listeners.delete(listener);};}},
    /** This second order is evaluated by the server over the same filtered
     * source, never by sorting the loaded Guide page. Construction is inert. */
    onNow() {
      life.assert();
      // On now is the server's own order: favourites first, then by number.
      const source = nextOnNowSource(api);
      const current = new GuideWindowStore({source, channelPageSize: 50, maxChannelPages: 6, maxTiles: 6, maxInFlight: 1, marginPages: 0, marginBlocks: 0});
      stores.add(current);
      return {store: current, dispose() {current.dispose();stores.delete(current);}};
    },
    async sources() {
      const value = await nextGuideSummaries(api, life.signal());
      life.assert();return value;
    },
    async refresh(signal?: AbortSignal) {
      const active = life.signal(signal);active.throwIfAborted();
      const value = await nextGuideSummaries(api, active);
      life.assert();active.throwIfAborted();
      // Membership, marks and favorites order may all have changed. Forget the
      // prior revision and re-read only each store's current bounded viewport.
      for (const current of stores) current.reset();
      return value;
    },
    /** A channel a link names, with what is on it now. */
    async selectedChannel(target: Readonly<{channelId: string}>, signal?: AbortSignal) {
      const value = await findChannel(api, target.channelId, Date.now(), life.signal(signal));
      life.assert();signal?.throwIfAborted();
      return value;
    },
    /** The player's live chrome reads its channel the old server's way. It moves with Watch live, in the player's area: until
     * then the chrome is not used against the new server. */
    async chromeChannel(target: Readonly<{sourceId: string; channelId: string; provenance: ChannelKind}>, signal?: AbortSignal) {
      const route = {kind: target.provenance, sourceId: target.sourceId, ...(options.includeServerHidden ? {includeServerHidden: true} : {}), timezone: options.timezone, search: '', ...guideWindow()};
      const value = parseChannelGuide(await readSelectedChannelGuideResponse(api, route, target.channelId, options.includeServerHidden === true, life.signal(signal)), context.scope.serverId, route);
      life.assert();signal?.throwIfAborted();
      return value.channels.find(channel => channel.id === target.channelId && channel.provenance === target.provenance
        && (channel.sourceId === target.sourceId || target.sourceId === 'library' && channel.provenance === 'library-channel'));
    },
    /** The viewer's favorite for one drawn row, retaining its existing profile hidden
     * preference at that row's revision. The caller re-reads the guide afterwards; nothing is patched locally. */
    async channelPreference(row: GuideChannelRow, values: Readonly<{favorite: boolean; hidden: boolean}>) {
      life.assert();
      await markChannel(api, row.id, values, life.signal());
      life.assert();
    },
    /** Hiding a channel for everyone is the owner's, in Live TV settings: it moves with the owner's Live TV screens. */
    async channelHidden(_row: GuideChannelRow, _hidden: boolean, _expected?: Readonly<{revision: number; hidden: boolean}>): ReturnType<typeof setServerChannelHidden> {
      throw Object.assign(new Error('This is not available on this server yet.'), {code: 'not_on_server'});
    },
    dispose() {life.dispose();for (const value of stores) value.dispose();stores.clear();listeners.clear();failure = undefined;},
  };
}
export type LiveBindings = ReturnType<typeof createLiveBindings>;
