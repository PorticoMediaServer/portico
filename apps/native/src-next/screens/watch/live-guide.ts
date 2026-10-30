import {sameFence, type ChannelPlaybackFence} from '../../../../../packages/player-core/src';
import {legacyChannel, legacyChannelRow, legacyGuideSource, type LegacyGuideOptions} from '../../../../../packages/client-core/src/guide/legacy-source';
import {parseChannelGuide, type GuideRoute} from '../../../../../packages/client-core/src/channel-guide';
import {routePath} from '../../../../../packages/contracts/src/routes';
import {fetchGuideSourceSummaries} from '../../../../../packages/client-core/src/guide/directory';
import type {GuideChannelRow, GuideProgram} from '../../../../../packages/client-core/src/guide/types';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeChannelGuideOrigin} from '../../platform/playback/types';
import {sameAuthority} from '../../shell/model';

export type NativeLiveGuideRow = Readonly<{channel: GuideChannelRow; step: number; programmes: readonly GuideProgram[]}>;
export type NativeLiveGuideSnapshot = Readonly<{fence?: ChannelPlaybackFence; phase: 'unavailable' | 'loading' | 'ready'; open: boolean;
  rows: readonly NativeLiveGuideRow[]; previous?: NativeLiveGuideRow; next?: NativeLiveGuideRow; busy: boolean; error?: unknown; timezone?: string}>;
const empty: NativeLiveGuideSnapshot = Object.freeze({phase: 'unavailable', open: false, rows: [], busy: false});
export const emptyLiveGuide = () => empty;
export const noLiveGuideSubscription = (_listener: () => void) => () => {};
const identity = (row: GuideChannelRow) => {const raw = legacyChannel(row);return raw ? JSON.stringify([raw.provenance, raw.sourceId, raw.id]) : '';};
const sameTarget = (a: GuideChannelRow, b: GuideChannelRow) => identity(a) !== '' && identity(a) === identity(b)
  && a.number === b.number && a.name === b.name && legacyChannel(a)?.generation === legacyChannel(b)?.generation;
const aborted = () => Object.assign(new Error('Live guide opening retired'), {name: 'AbortError'});

/** Four bounded neighbor reads, never a lineup scan. One opening owns its reads,
 * advertised targets and tune; a successor cannot inherit any of its callbacks. */
export class NativeTVLiveGuide {
  private state = empty;
  private listeners = new Set<() => void>();
  private off: (() => void)[];
  private disposed = false;
  private panel?: AbortController;
  private command?: {controller: AbortController; fromMini: boolean};
  private opening?: {controller: AbortController; fence: ChannelPlaybackFence; origin?: NativeChannelGuideOrigin; options: LegacyGuideOptions;
    route: ReturnType<NativeShellBootstrapContext['navigation']['getSnapshot']>; principal: string; authority: ReturnType<NativeShellBootstrapContext['authority']>;
    viewerFence?: string; busy: boolean};
  constructor(private readonly context: NativeShellBootstrapContext, private readonly touch = false) {
    this.off = [context.playback.subscribe(this.update), context.navigation.subscribe(this.update), context.subscribeAuthority(this.update)];
    this.update();
  }
  getSnapshot = () => this.state;
  /** Borrow only this exact source/route opening for other live-player reads. */
  captureSource = () => {
    const opening = this.opening;
    if (!opening?.origin || !this.current(opening)) return undefined;
    return Object.freeze({fence: opening.fence, origin: opening.origin, authority: opening.authority, route: opening.route,
      signal: opening.controller.signal, current: () => this.current(opening), api: this.source(opening).api});
  };
  restoreProof = (expected: NativeLiveGuideSnapshot) => {
    const opening = this.state === expected && expected.open ? this.opening : undefined;
    return () => !!opening && this.current(opening);
  };
  subscribe = (listener: () => void) => {this.listeners.add(listener);return () => {this.listeners.delete(listener);};};
  private publish(state: NativeLiveGuideSnapshot) {this.state = Object.freeze(state);for (const listener of [...this.listeners]) listener();}
  private principal() {const session = this.context.session();return session ? JSON.stringify([session.sessionFamilyId, session.tokenGeneration, session.authorizationHorizon, session.deviceId ?? null, session.installationId ?? null]) : '';}
  private current(opening: NonNullable<NativeTVLiveGuide['opening']>) {
    const value = this.context.playback.getSnapshot(), output = value.playback.session;
    return !this.disposed && this.opening === opening && !opening.controller.signal.aborted && this.context.current()
      && sameAuthority(opening.authority, this.context.authority()) && opening.principal === this.principal()
      && this.context.navigation.getSnapshot() === opening.route && !value.retired && sameFence(value.fence, opening.fence)
      && sameAuthority(value.authority, opening.authority) && value.dock.local?.expanded === true
      && output?.id === opening.fence.sessionId && output.generation === opening.fence.generation;
  }
  private source(opening: NonNullable<NativeTVLiveGuide['opening']>) {
    const api = {request: async <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
      if (!this.current(opening) || signal?.aborted) throw aborted();
      const value = await this.context.api.request<T>(path, method, body, signal);
      if (!this.current(opening) || signal?.aborted) throw aborted();
      return value;
    }};
    return {api, source: legacyGuideSource(api, opening.authority.serverId, opening.options)};
  }
  private update = () => {
    if (this.disposed || this.opening && this.current(this.opening)) return;
    this.panel?.abort();this.panel = undefined;
    this.command?.controller.abort();this.command = undefined;
    this.opening?.controller.abort();this.opening = undefined;
    const value = this.context.playback.getSnapshot(), fence = value.fence, origin = value.channelGuide, route = this.context.navigation.getSnapshot();
    const raw = origin && legacyChannel(origin.row);
    if (!this.context.current() || !this.principal() || value.retired || value.dock.local?.expanded !== true || fence?.kind !== 'channel'
      || !this.touch && (route.route.kind !== 'channel' || route.route.sourceId !== fence.sourceId || route.route.channelId !== fence.channelId)
      || !this.touch && (!origin || !raw) || origin && (!raw || raw.id !== fence.channelId || raw.sourceId !== fence.sourceId || raw.provenance !== fence.channelKind || raw.generation !== fence.channelGeneration)) {
      this.publish(empty);return;
    }
    let options = origin?.options;
    if (!options) {
      // A Home/direct start has no guide query to inherit. Resolve its one actual
      // accepted channel, then ask the server for neighbors in that source.
      if (!fence.sourceId) {this.publish(empty);return;}
      const channel = value.playback.channel, linear = value.playback.linear?.channel;
      if (!channel || channel.kind !== fence.channelKind || channel.sourceId !== fence.sourceId || channel.channelId !== fence.channelId || channel.generation !== fence.channelGeneration
        || !linear || linear.kind !== fence.channelKind || linear.sourceId !== fence.sourceId || linear.channelId !== fence.channelId || linear.generation !== fence.channelGeneration) {this.publish(empty);return;}
      try {
        const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone;
        if (!timezone) {this.publish(empty);return;}
        new Intl.DateTimeFormat(undefined, {timeZone: timezone});
        options = Object.freeze({kind: fence.channelKind, sourceId: fence.sourceId, timezone});
      } catch {this.publish(empty);return;}
    }
    const opening: NonNullable<NativeTVLiveGuide['opening']> = {controller: new AbortController(), fence, origin, options, route, principal: this.principal(), authority: this.context.authority(), busy: false};
    this.opening = opening;
    if (!this.current(opening)) {opening.controller.abort();this.opening = undefined;this.publish(empty);return;}
    this.publish({fence, phase: 'loading', open: false, rows: [], busy: false, timezone: options.timezone});
    void this.load(opening);
  };
  private async load(opening: NonNullable<NativeTVLiveGuide['opening']>) {
    const {api, source} = this.source(opening), signal = opening.controller.signal;
    try {
      const directory = await fetchGuideSourceSummaries(api, opening.authority.serverId, opening.options.timezone, signal);
      if (!directory.sources.some(row => row.id === opening.fence.sourceId && row.provenance === opening.fence.channelKind && row.generation === opening.fence.channelGeneration)) throw new Error('channel_source_unavailable');
      opening.viewerFence = directory.viewerFence;
      if (!opening.origin) {
        const sourceId = opening.fence.sourceId;
        if (!sourceId) throw new Error('channel_source_unavailable');
        const now = Date.now(), route: GuideRoute = {kind: opening.fence.channelKind, sourceId, timezone: opening.options.timezone, search: '',
          start: new Date(now).toISOString(), end: new Date(now + 3 * 3600_000).toISOString()};
        const query = new URLSearchParams({kind: route.kind, sourceId, timezone: route.timezone, search: route.search, start: route.start, end: route.end, channels: opening.fence.channelId, limit: '1', cursor: ''});
        const guide = parseChannelGuide(await api.request<unknown>(`${routePath('get_guide')}?${query}`, 'GET', undefined, signal), opening.authority.serverId, route);
        if (!this.current(opening)) return;
        const channel = guide.channels[0];
        if (guide.viewerFence !== directory.viewerFence || guide.channels.length !== 1 || !channel || channel.id !== opening.fence.channelId
          || channel.sourceId !== opening.fence.sourceId || channel.provenance !== opening.fence.channelKind || channel.generation !== opening.fence.channelGeneration
          || !channel.tuneAvailable || channel.hidden) throw new Error('channel_source_unavailable');
        opening.origin = Object.freeze({row: legacyChannelRow(channel), options: opening.options});
      }
      const rows: NativeLiveGuideRow[] = [{channel: opening.origin.row, step: 0, programmes: []}], seen = new Set([identity(opening.origin.row)]);
      for (const direction of [-1, 1] as const) {
        let anchor = opening.origin.row;
        for (let distance = 1; distance <= 2; distance++) {
          const channel = await source.neighbor!(anchor, direction, signal);
          if (!channel || seen.has(identity(channel))) break;
          seen.add(identity(channel));rows.push({channel, step: direction * distance, programmes: []});anchor = channel;
        }
      }
      rows.sort((a, b) => a.step - b.step);
      if (!this.current(opening)) return;
      this.publish({...this.state, phase: 'ready', rows, previous: rows.find(row => row.step === -1), next: rows.find(row => row.step === 1)});
      if (this.panel && this.state.open) void this.programmes(opening, this.panel);
    } catch (error) {if (this.current(opening)) {this.panel?.abort();this.panel = undefined;this.publish({...empty, fence: opening.fence, error});}}
  }
  private async programmes(opening: NonNullable<NativeTVLiveGuide['opening']>, panel: AbortController) {
    const current = () => this.current(opening) && this.panel === panel && !panel.signal.aborted && this.state.open;
    const {api} = this.source(opening), signal = panel.signal, rows = this.state.rows;
    const stop = () => panel.abort();opening.controller.signal.addEventListener('abort', stop, {once: true});
    try {
      const complete = await Promise.all(rows.map(async row => {
        const raw = legacyChannel(row.channel)!;
        const named = legacyGuideSource(api, opening.authority.serverId, {...opening.options, kind: raw.provenance, sourceId: raw.sourceId});
        // Only the five named rows and a bounded current programme window.
        try {
          const now = Date.now(), programmes = await named.programs([raw.id], now, now + 3 * 60 * 60 * 1000, signal);
          return {...row, programmes: programmes[raw.id] ?? []};
        } catch (error) {if (current()) this.publish({...this.state, error});return row;}
      }));
      if (!current()) return;
      complete.sort((a, b) => a.step - b.step);
      this.publish({...this.state, phase: 'ready', rows: complete, previous: complete.find(row => row.step === -1), next: complete.find(row => row.step === 1)});
    } finally {opening.controller.signal.removeEventListener('abort', stop);}
  }
  open = () => {
    const opening = this.opening;
    if (!opening || !this.current(opening) || this.state.phase === 'unavailable') return false;
    if (this.state.open) return true;
    this.panel?.abort();const panel = new AbortController();this.panel = panel;
    this.publish({...this.state, open: true});
    if (this.state.phase === 'ready') void this.programmes(opening, panel);
    return true;
  };
  guideAction = () => {
    const route = this.context.navigation.getSnapshot(), authority = this.context.authority(), fence = this.context.playback.getSnapshot().fence;
    return () => {
      if (!this.context.current() || !sameAuthority(authority, this.context.authority()) || this.context.navigation.getSnapshot() !== route
        || !fence || !sameFence(this.context.playback.getSnapshot().fence, fence)) return;
      if (!this.open()) this.context.navigation.open({kind: 'shell', destination: {kind: 'live'}}, authority);
    };
  };
  fullGuide = (expected: NativeLiveGuideSnapshot) => {
    const opening = this.opening;
    if (this.state !== expected || !expected.open || !opening || !this.current(opening)) return;
    this.context.navigation.open({kind: 'shell', destination: {kind: 'live'}}, opening.authority);
  };
  close = (expected?: NativeLiveGuideSnapshot) => {
    if (!this.state.open || expected && this.state !== expected) return false;
    this.panel?.abort();this.panel = undefined;
    if (this.command?.fromMini) {this.command.controller.abort();this.command = undefined;if (this.opening) this.opening.busy = false;}
    this.publish({...this.state, open: false, busy: this.opening?.busy ?? false});return true;
  };
  tune = async (advertised: NativeLiveGuideRow, expected: NativeLiveGuideSnapshot, onError?: (error: unknown) => void) => {
    const opening = this.opening;
    if (this.state !== expected || !opening?.origin || !this.current(opening) || opening.busy || this.state.phase !== 'ready' || !this.state.rows.includes(advertised)) return;
    if (advertised.step === 0) {this.close();return;}
    const command = {controller: new AbortController(), fromMini: this.state.open};this.command = command;
    const stop = () => command.controller.abort();opening.controller.signal.addEventListener('abort', stop, {once: true});
    const current = () => this.current(opening) && this.command === command && !command.controller.signal.aborted;
    opening.busy = true;this.publish({...this.state, busy: true, error: undefined});
    try {
      const {api, source} = this.source(opening), signal = command.controller.signal;
      let target: GuideChannelRow | undefined = opening.origin.row;
      for (let step = 0; step < Math.abs(advertised.step) && target; step++) target = await source.neighbor!(target, advertised.step < 0 ? -1 : 1, signal);
      if (!target || !sameTarget(target, advertised.channel)) throw new Error('channel_lineup_changed');
      const raw = legacyChannel(target)!;
      const directory = await fetchGuideSourceSummaries(api, opening.authority.serverId, opening.options.timezone, signal);
      if (directory.viewerFence !== opening.viewerFence
        || !directory.sources.some(row => row.id === opening.fence.sourceId && row.provenance === opening.fence.channelKind && row.generation === opening.fence.channelGeneration)
        || !directory.sources.some(row => row.id === raw.sourceId && row.provenance === raw.provenance && row.generation === raw.generation)) throw new Error('channel_source_unavailable');
      if (!current()) return;
      const route = {kind: 'channel' as const, sourceId: raw.sourceId, channelId: raw.id,
        guide: Object.freeze({row: target, options: opening.options}), tune: {fence: opening.fence, sourceGeneration: raw.generation}};
      // Touch consumes each accepted launch page and restores the original route;
      // TV keeps its channel page and replaces that exact opening when zapping.
      if (this.touch) this.context.navigation.open(route, opening.authority);
      else this.context.navigation.replaceChannel(opening.route, route, opening.authority);
    } catch (error) {
      if (current()) {
        this.publish({...this.state, error});
        if (!command.fromMini && current()) onError?.(error);
      }
    }
    finally {
      opening.controller.signal.removeEventListener('abort', stop);
      if (this.command === command) {this.command = undefined;opening.busy = false;if (this.current(opening)) this.publish({...this.state, busy: false});}
    }
  };
  dispose() {this.disposed = true;this.panel?.abort();this.panel = undefined;this.command?.controller.abort();this.command = undefined;this.opening?.controller.abort();this.opening = undefined;for (const off of this.off) off();this.listeners.clear();}
}
