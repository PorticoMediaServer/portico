import {routePath} from '../../../../../packages/contracts/src/routes';
import {parseChannelGuide, type GuideRoute} from '../../../../../packages/client-core/src/channel-guide';
import {legacyChannel, legacyChannelRow, legacyProgramme, legacyProgram} from '../../../../../packages/client-core/src/guide/legacy-source';
import type {GuideChannelRow, GuideProgram} from '../../../../../packages/client-core/src/guide/types';
import {defaultRecordingOptions, type DVRCommandLifetime, type Recording} from '../../../../../packages/client-core/src/dvr';
import type {ChannelProgramme} from '../../../../../packages/client-core/src/playback/linear-protocol';
import type {ChannelPlaybackFence} from '../../../../../packages/player-core/src';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {nativeLiveWatchPort, subscribeNativeLiveWatchPort, type NativeLiveWatchPort} from '../settings-live/watch-port';
import {seriesFormDefaults, type SeriesForm} from '../settings-live/recording-rule-presentation';
import type {NativeTVLiveGuide} from './live-guide';
import {captureLiveActionSource, type NativeLiveActionSource} from './live-action-source';
import {nativeShellRoutes} from '../../shell/route-registry';

type Source = NativeLiveActionSource;
export type NativeLiveActionOffer = Readonly<{id: number; channel: GuideChannelRow; programme: GuideProgram}>;
export type NativeLiveActionSheet = Readonly<{id: number; kind: 'more' | 'series' | 'details'; offer: NativeLiveActionOffer;
  port: NativeLiveWatchPort; lifetime: DVRCommandLifetime; defaults?: SeriesForm}>;
export type NativeLiveActionSnapshot = Readonly<{fence?: ChannelPlaybackFence; offer?: NativeLiveActionOffer; sheet?: NativeLiveActionSheet; busy: boolean;
  recording?: Recording; error?: unknown; confirmed?: 'recording' | 'series'}>;
const empty: NativeLiveActionSnapshot = Object.freeze({busy: false});
export const emptyLiveActions = () => empty;
export const noLiveActionsSubscription = (_listener: () => void) => () => {};
const retired = () => Object.assign(new Error('Live action opening retired'), {name: 'AbortError'});

/** An original Watch programme borrows the genuine viewer DVR controller. Reads
 * name one channel; every command carries its own source/route/occurrence scope. */
export class NativeTVLiveActions {
  private state = empty;
  private listeners = new Set<() => void>();
  private off: (() => void)[];
  private disposed = false;
  private sequence = 0;
  private confirmationTimer?: ReturnType<typeof setTimeout>;
  private command?: AbortController;
  private preparingServices?: AbortController;
  private sheet?: NativeLiveActionSheet;
  private sheetController?: AbortController;
  private seriesEntry?: {sheet: NativeLiveActionSheet};
  private favoriteCommand?: {sheet: NativeLiveActionSheet; channel: GuideChannelRow; favorite: boolean; hidden: boolean};
  private opening?: {source: Source; projected: ChannelProgramme; port: NativeLiveWatchPort; controller: AbortController; viewerFence?: string};
  constructor(private context: NativeShellBootstrapContext, private guide: NativeTVLiveGuide, private presented: () => boolean,
    subscribePresented: (listener: () => void) => () => void) {
    this.off = [context.playback.subscribe(this.update), guide.subscribe(this.update), context.navigation.subscribe(this.update), context.subscribeAuthority(this.update),
      subscribePresented(this.update), subscribeNativeLiveWatchPort(context, this.update)];
    this.update();
  }
  getSnapshot = () => this.state;
  subscribe = (listener: () => void) => {this.listeners.add(listener);return () => {this.listeners.delete(listener);};};
  private publish(state: NativeLiveActionSnapshot) {this.state = Object.freeze(state);for (const listener of [...this.listeners]) listener();}
  private current(opening: NonNullable<NativeTVLiveActions['opening']>) {
    const programme = this.context.playback.getSnapshot().playback.linear?.programme;
    return !this.disposed && this.opening === opening && !opening.controller.signal.aborted && opening.source.current()
      && opening.port.current() && nativeLiveWatchPort(this.context) === opening.port && !!programme
      && programme.id === opening.projected.id && programme.startMs === opening.projected.startMs && programme.endMs === opening.projected.endMs
      && Date.now() >= programme.startMs && Date.now() < programme.endMs;
  }
  private async read(opening: NonNullable<NativeTVLiveActions['opening']>, signal: AbortSignal) {
    if (!this.current(opening) || signal.aborted) throw retired();
    const target = opening.source.target, options = opening.source.query, now = Date.now();
    const route: GuideRoute = {kind: target.kind, sourceId: target.sourceId, timezone: options.timezone, search: options.search ?? '',
      start: new Date(now).toISOString(), end: new Date(now + 3 * 3600_000).toISOString(),
      ...(options.group ? {group: options.group} : {}), ...(options.favorites !== undefined ? {favorites: options.favorites} : {}), ...(options.includeHidden ? {includeHidden: true} : {})};
    const query = new URLSearchParams({kind: route.kind, sourceId: route.sourceId, timezone: route.timezone, search: route.search,
      start: route.start, end: route.end, channels: target.channelId, limit: '1', cursor: ''});
    if (route.favorites !== undefined) query.set('favorites', String(route.favorites));if (route.includeHidden) query.set('includeHidden', 'true');if (route.group) query.set('group', route.group);
    const response = await opening.source.api.request<unknown>(`${routePath('get_guide')}?${query}`, 'GET', undefined, signal);
    if (!this.current(opening) || signal.aborted) throw retired();
    const guide = parseChannelGuide(response, opening.source.authority.serverId, route);
    const rows = guide.channels.filter(row => row.id === target.channelId && row.sourceId === target.sourceId && row.provenance === target.kind && row.generation === target.generation);
    if (rows.length !== 1 || guide.channels.length !== 1 || opening.viewerFence !== undefined && guide.viewerFence !== opening.viewerFence) throw new Error('channel_source_unavailable');
    const channel = rows[0]!, programme = channel.programmes.find(row => row.id === opening.projected.id
      && Date.parse(row.start) === opening.projected.startMs && Date.parse(row.end) === opening.projected.endMs);
    if (!programme || programme.channelId !== channel.id) throw new Error('channel_programme_unavailable');
    return {channel: legacyChannelRow(channel), programme: legacyProgram(programme), viewerFence: guide.viewerFence};
  }
  private update = () => {
    if (this.disposed || this.preparingServices || this.opening && this.current(this.opening)) return;
    this.command?.abort();this.command = undefined;this.sheetController?.abort();this.sheetController = undefined;this.sheet = undefined;
    clearTimeout(this.confirmationTimer);this.confirmationTimer = undefined;
    this.favoriteCommand = undefined;
    this.seriesEntry = undefined;
    this.opening?.controller.abort();this.opening = undefined;
    const source = captureLiveActionSource(this.context, this.guide, this.presented), projected = this.context.playback.getSnapshot().playback.linear?.programme;
    if (!source || !projected || !Number.isFinite(projected.startMs) || !Number.isFinite(projected.endMs) || projected.endMs <= projected.startMs) {this.publish(empty);return;}
    const controller = new AbortController();let port = nativeLiveWatchPort(this.context);
    if (!port?.current()) {
      this.preparingServices = controller;let prepared = false;
      try {prepared = nativeShellRoutes.prepareDestination(this.context, {kind: 'live'}, source.route, Object.freeze({signal: controller.signal,
        isCurrent: () => !this.disposed && this.preparingServices === controller && source.current()}));}
      catch {prepared = false;}
      finally {if (this.preparingServices === controller) this.preparingServices = undefined;}
      if (!prepared) {controller.abort();this.publish(empty);return;}
      port = nativeLiveWatchPort(this.context);
    }
    if (!port?.current() || !source.current()) {controller.abort();this.publish(empty);return;}
    const opening: NonNullable<NativeTVLiveActions['opening']> = {source, port, projected: Object.freeze({...projected}), controller};this.opening = opening;
    this.publish(empty);
    if (!this.current(opening)) return;
    void this.read(opening, opening.controller.signal).then(value => {
      if (!this.current(opening)) return;
      opening.viewerFence = value.viewerFence;
      this.publish({fence: source.fence, offer: Object.freeze({id: ++this.sequence, channel: value.channel, programme: value.programme}), busy: false});
    }, () => {}); // No offer without the actual permitted channel/programme projection.
  };
  restoreProof = (expected: NativeLiveActionSnapshot) => {
    const opening = this.state === expected && expected.sheet ? this.opening : undefined;
    return () => !!opening && this.current(opening);
  };
  record = async (expected: NativeLiveActionSnapshot, onError: (error: unknown) => void) => {
    const opening = this.opening, offer = expected.offer;
    if (this.state !== expected || !opening || !offer || !this.current(opening) || this.guide.getSnapshot().open || expected.busy || expected.sheet || offer.channel.kind !== 'live' || !offer.channel.recordAvailable || offer.programme.recording || expected.recording) return;
    const controller = new AbortController();this.command = controller;
    const current = () => this.current(opening) && this.command === controller && !controller.signal.aborted;
    const stop = () => controller.abort();opening.controller.signal.addEventListener('abort', stop, {once: true});
    this.publish({...this.state, busy: true, error: undefined, confirmed: undefined});
    try {
      const fresh = await this.read(opening, controller.signal), channel = legacyChannel(fresh.channel), programme = legacyProgramme(fresh.programme);
      if (!current()) return;
      if (!channel || !programme || channel.provenance !== 'live-source' || !channel.recordAvailable) throw new Error('channel_recording_unavailable');
      if (fresh.programme.recording) {this.publish({...this.state, offer: Object.freeze({...offer, channel: fresh.channel, programme: fresh.programme})});return;}
      const result = await opening.port.recordings.schedule({channel, programme, series: false}, defaultRecordingOptions, Object.freeze({signal: controller.signal, isCurrent: current}));
      if (!current()) return;
      if (!result) throw new Error('recording_action_unavailable');
      const occurrence = result.value.occurrence;
      if (occurrence.sourceId !== channel.sourceId || occurrence.channelId !== channel.id || occurrence.generation !== channel.generation || occurrence.programmeId !== programme.id) throw new Error('channel_recording_unavailable');
      this.publish({...this.state, recording: result.value});if (current()) this.confirm(opening, 'recording');
    } catch (error) {if (current()) {this.publish({...this.state, error});if (current()) onError(error);}}
    finally {opening.controller.signal.removeEventListener('abort', stop);if (this.command === controller) {this.command = undefined;if (this.current(opening)) this.publish({...this.state, busy: false});}}
  };
  openMore = (expected: NativeLiveActionSnapshot) => {if (this.state === expected && expected.offer && !expected.busy && !expected.sheet && !this.guide.getSnapshot().open) this.openSheet(expected.offer, 'more');};
  private openSheet(offer: NativeLiveActionOffer, kind: NativeLiveActionSheet['kind'], defaults?: SeriesForm) {
    const opening = this.opening;if (!opening || !this.current(opening) || this.state.offer !== offer || this.state.busy || this.seriesEntry) return;
    this.sheetController?.abort();if (!this.current(opening) || this.state.offer !== offer) return;
    this.favoriteCommand = undefined;
    const controller = new AbortController();this.sheetController = controller;
    const sheet: NativeLiveActionSheet = Object.freeze({id: ++this.sequence, kind, offer, port: opening.port, defaults,
      lifetime: Object.freeze({signal: controller.signal, isCurrent: () => this.sheet === sheet && !controller.signal.aborted && this.current(opening)})});
    this.sheet = sheet;this.publish({...this.state, sheet, error: undefined});
  }
  close = (expected?: NativeLiveActionSheet) => {
    if (!this.sheet || expected && this.sheet !== expected) return false;
    this.seriesEntry = undefined;
    this.favoriteCommand = undefined;
    this.sheetController?.abort();this.sheetController = undefined;this.sheet = undefined;this.publish({...this.state, sheet: undefined, busy: false});return true;
  };
  private sheetAvailable(sheet: NativeLiveActionSheet) {return this.sheet === sheet && sheet.kind === 'more' && sheet.lifetime.isCurrent() && !this.state.busy && !this.seriesEntry;}
  details = (sheet: NativeLiveActionSheet) => {if (this.sheetAvailable(sheet)) this.openSheet(sheet.offer, 'details');};
  dismissError = (sheet: NativeLiveActionSheet) => {if (this.sheet === sheet && sheet.lifetime.isCurrent()) this.publish({...this.state, error: undefined});};
  favorite = async (sheet: NativeLiveActionSheet, onError: (error: unknown) => void) => {
    const opening = this.opening;
    const current = () => !!opening && this.current(opening) && this.sheet === sheet && sheet.lifetime.isCurrent();
    if (!opening || !current() || !this.sheetAvailable(sheet)) return;
    this.publish({...this.state, busy: true, error: undefined});
    try {
      if (this.favoriteCommand?.sheet !== sheet) {
        const fresh = await this.read(opening, sheet.lifetime.signal), raw = legacyChannel(fresh.channel);
        if (!current()) return;
        if (!raw) throw new Error('channel_source_unavailable');
        this.favoriteCommand = {sheet, channel: fresh.channel, favorite: !sheet.offer.channel.favorite, hidden: raw.hidden};
      }
      const command = this.favoriteCommand;
      if (!current() || !command || command.sheet !== sheet) return;
      const channel = await sheet.port.live.channelPreference(command.channel, {favorite: command.favorite, hidden: command.hidden}, sheet.port.requestId, sheet.lifetime);
      if (!current()) return;
      if (!channel) throw new Error('channel_preference_unavailable');
      this.publish({...this.state, offer: Object.freeze({...sheet.offer, channel})});
      if (current()) this.close(sheet);
    } catch (error) {if (current()) {this.publish({...this.state, error});if (current()) onError(error);}}
    finally {if (current()) this.publish({...this.state, busy: false});}
  };
  series = async (sheet: NativeLiveActionSheet) => {
    if (!this.sheetAvailable(sheet) || !sheet.offer.channel.recordAvailable || sheet.offer.channel.kind !== 'live') return;
    const entry = {sheet};this.seriesEntry = entry;this.publish({...this.state, busy: true, error: undefined});
    try {
      if (this.seriesEntry !== entry || this.sheet !== sheet || !sheet.lifetime.isCurrent()) return;
      let defaults: SeriesForm | undefined;
      try {defaults = seriesFormDefaults(await sheet.port.owner.dvrSettings());} catch {defaults = undefined;}
      if (this.seriesEntry !== entry || this.sheet !== sheet || !sheet.lifetime.isCurrent()) return;
      this.seriesEntry = undefined;this.publish({...this.state, busy: false});
      if (this.sheetAvailable(sheet)) this.openSheet(sheet.offer, 'series', defaults);
    } finally {
      if (this.seriesEntry === entry) {this.seriesEntry = undefined;if (this.sheet === sheet && sheet.lifetime.isCurrent()) this.publish({...this.state, busy: false});}
    }
  };
  seriesCreated = (sheet: NativeLiveActionSheet) => {
    const opening = this.opening;
    if (opening && this.sheet === sheet && sheet.lifetime.isCurrent()) {this.close(sheet);if (this.current(opening)) this.confirm(opening, 'series');}
  };
  private confirm(opening: NonNullable<NativeTVLiveActions['opening']>, confirmed: 'recording' | 'series') {
    clearTimeout(this.confirmationTimer);this.publish({...this.state, confirmed});
    if (!this.current(opening)) return;
    this.confirmationTimer = setTimeout(() => {if (this.current(opening)) this.publish({...this.state, confirmed: undefined});}, 6000);
  }
  dispose() {this.disposed = true;this.preparingServices?.abort();this.preparingServices = undefined;this.seriesEntry = undefined;clearTimeout(this.confirmationTimer);this.command?.abort();this.sheetController?.abort();this.opening?.controller.abort();for (const off of this.off) off();this.listeners.clear();}
}
