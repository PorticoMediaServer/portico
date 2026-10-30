import {forwardIdentity} from '../detail/context';
import {subscribeFacts} from '../../shell/fact-source';
import {subscribePresentation, useShellFacts} from '../../shell/fact-hooks';
import {useLayoutEffect, useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import {DVRClient, defaultRecordingOptions, type Recording} from '@core/dvr.ts';
import type {ChannelReference} from '@core/index.ts';
import {channelTimeline} from '@core/playback/channel-timeline.ts';
import {legacyChannel, legacyChannelRow, legacyGuideSource, legacyProgramme, type GuideChannelRow} from '@core/guide/index.ts';
import type {GuideChannel, GuideProgramme} from '@core/channel-guide.ts';
import type {ChannelPlaybackFence} from '../../../../packages/player-core/src/index.ts';
import type {WebPlaybackRuntimeSnapshot, WebShellPlaybackRuntime} from '../../platform/playback';
import {fenceKey as shellFenceKey} from '../../shell/playback-model';
import type {ShellAssemblyContext} from '../../shell/bootstrap';
import {router} from '../../router';
import {screenLifetime, type ScreenContext} from '../detail/context';
import {createLiveBindings, type LiveBindings} from './bindings';
import {liveChannelCurrent, liveNavigationAllowed, liveRecordCommand} from './live-chrome-model';
import type {LiveChromeChannel, LiveChromeGuideChannel, LiveChromeGuideTarget, LiveChromePort, LiveChromeProgramme} from './live-chrome-types';

const randomHex = async () => Array.from(crypto.getRandomValues(new Uint8Array(24)), value => value.toString(16).padStart(2, '0')).join('');
const recordingStates: ReadonlySet<string> = new Set(['scheduled', 'conflicted', 'waiting-source', 'waiting-guide', 'preparing', 'recording', 'finalizing', 'completed', 'incomplete-playable', 'failed', 'cancelled', 'pending-delete', 'deleted']);
const recordingState = (programme: GuideProgramme | undefined): LiveChromeProgramme['recording'] => {
  if (!programme?.recordingId || programme.recordingState === undefined || !recordingStates.has(programme.recordingState)) return undefined;
  return programme.recordingState as LiveChromeProgramme['recording'];
};
const asChromeChannel = (channel: GuideChannel): LiveChromeChannel => ({id: channel.id, sourceId: channel.sourceId, kind: channel.provenance,
  generation: channel.generation, number: channel.number, name: channel.name, ...(channel.mark ? {mark: {text: channel.mark.text, seed: channel.mark.seed}} : {}),
  favorite: channel.favorite, recordAvailable: channel.recordAvailable, tuneAvailable: channel.tuneAvailable});
const asChromeProgramme = (programme: GuideProgramme): LiveChromeProgramme => ({id: programme.id, title: programme.title,
  ...(programme.subtitle ? {subtitle: programme.subtitle} : {}), ...(programme.episode ? {episode: programme.episode} : {}),
  startMs: Date.parse(programme.start), endMs: Date.parse(programme.end), ...(recordingState(programme) ? {recording: recordingState(programme)} : {})});
const asMini = (row: GuideChannelRow, programme: GuideProgramme | undefined): LiveChromeGuideChannel | undefined => {
  const channel = legacyChannel(row);
  if (!channel) return undefined;
  return {channel: asChromeChannel(channel), ...(programme ? {programme: asChromeProgramme(programme)} : {}),
    ...(programme ? {nextStartMs: Date.parse(programme.end)} : {})};
};

/** The shell fence plus the exact programme input this read was opened for. */
const guideReadKey = (fence: ChannelPlaybackFence, programme: {id: string; startMs: number; endMs: number} | null | undefined) =>
  JSON.stringify([shellFenceKey(fence), programme?.id ?? '', programme?.startMs ?? 0, programme?.endMs ?? 0]);
type GuideFacts = Readonly<{chromeId: string; readId: string; fenceKey: string; current: GuideChannel; rows: readonly GuideChannelRow[]; programmes: ReadonlyMap<string, GuideProgramme>}>;
type ChromeAuthority = {id: string; scopeKey: string; context: ShellAssemblyContext; live: boolean;
  screen: ScreenContext; life: ReturnType<typeof screenLifetime>; client: DVRClient};
type ChromeRead = {id: string; chrome: ChromeAuthority; fence: ChannelPlaybackFence; channel: ChannelReference; fenceKey: string; runtime: WebShellPlaybackRuntime; live: boolean;
  bindings: LiveBindings; source: ReturnType<typeof legacyGuideSource>};
type ChromeAction = {id: string; chrome: ChromeAuthority; read: ChromeRead; fence: ChannelPlaybackFence; channel: ChannelReference; fenceKey: string;
  mediaAuthority: WebPlaybackRuntimeSnapshot['authority']; runtime: WebShellPlaybackRuntime; live: boolean};
type RecordClaim = {id: string; actionId: string; readId: string; programmeId: string; live: boolean};

/** The authority tuple a committed chrome opening owns; a later shell
 * presentation publication does not replace it. */
function chromeScope(context: ShellAssemblyContext) {
  const session = context.session();
  return JSON.stringify([session.sessionFamilyId, session.viewer.serverId, session.viewer.authority, session.viewer.accountId, session.viewer.profileId, session.viewer.role]);
}
function chromeScreen(context: ShellAssemblyContext): ScreenContext {
  const session = context.session();
  return {api: context.api, events: context.events, artwork: context.artwork, get identity() {return context.facts.identity;}, subscribeIdentity: listener => subscribeFacts(context, listener),
    scope: {serverId: session.viewer.serverId, viewerId: JSON.stringify([session.viewer.authority, session.viewer.accountId, session.viewer.profileId])},
    isCurrent: context.isCurrent,
    preferences: {getSnapshot: () => context.preferences?.getSnapshot() ?? context.facts.preferences, subscribe: listener => subscribePresentation(context, listener)},
    effects: {reportError: () => {}, openEntry: () => {}, openSettings: () => {}, openHome: () => {}, play: async () => {}}};
}

/**
 * Builds the drawn Live chrome's narrow port from the actual channel session and the
 * authority-local guide. Construction is inert; a mounted player starts one bounded read
 * for the current channel and its nearby rows. The authority, guide facts and exact
 * action witness are layout-owned: expanding or collapsing the dock changes only the
 * action witness, never the source or the facts it already read. No second player,
 * channel controller or store is created, and no guide fact is guessed from the VOD
 * presentation.
 */
export function useLiveChromePort(context: ShellAssemblyContext | undefined, runtime: WebShellPlaybackRuntime | undefined, snapshot: WebPlaybackRuntimeSnapshot | undefined,
  skips: Readonly<{back: number; forward: number}>): LiveChromePort | undefined {
  useShellFacts(context);
  const scopeKey = context ? chromeScope(context) : '';
  const [publishedChrome, publishChrome] = useState<ChromeAuthority>();
  useLayoutEffect(() => {
    if (!context || !scopeKey) return;
    let chromeLive = true;
    const base = chromeScreen(context);
    const screen: ScreenContext = forwardIdentity(base, {isCurrent: () => chromeLive && context.isCurrent()});
    const life = screenLifetime(screen);
    const client = new DVRClient(life.api, screen.scope.serverId, randomHex);
    const held: ChromeAuthority = {id: crypto.randomUUID(), scopeKey, context, live: true, screen, life, client};
    publishChrome(held);
    return () => {chromeLive = false;held.live = false;client.dispose();life.dispose();};
  }, [context, scopeKey]);
  const chrome = publishedChrome?.live && publishedChrome.context === context && publishedChrome.scopeKey === scopeKey
    && publishedChrome.screen.isCurrent() ? publishedChrome : undefined;

  const expanded = snapshot?.dock.local?.expanded === true;
  const linear = snapshot?.playback.linear, presentation = snapshot?.presentation;
  const fence = snapshot?.fence?.kind === 'channel' ? snapshot.fence as ChannelPlaybackFence : undefined;
  const channel = linear?.channel;
  const fenceKey = fence && channel ? guideReadKey(fence, linear?.programme) : '';

  const [facts, setFacts] = useState<GuideFacts>();
  const [publishedRead, publishRead] = useState<ChromeRead>();
  const [refresh, setRefresh] = useState(0);
  // Publish only at commit, then sample once for this setup's original lifetime.
  const setupPorts = useRef({fence, channel});
  useLayoutEffect(() => {setupPorts.current = {fence, channel};});
  useLayoutEffect(() => {
    const {fence, channel} = setupPorts.current;
    if (!chrome || !fence || !channel || !fenceKey || !runtime) return;
    const options = {kind: channel.kind, sourceId: channel.sourceId, timezone: defaultI18n.region.timeZone ?? Intl.DateTimeFormat().resolvedOptions().timeZone, search: '', sort: 'number' as const};
    let readLive = true;
    const readScreen: ScreenContext = {...chrome.screen, isCurrent: () => readLive && chrome.screen.isCurrent()};
    const bindings = createLiveBindings(readScreen, options);
    const readLife = screenLifetime(readScreen);
    const source = legacyGuideSource(readLife.api, chrome.screen.scope.serverId, options);
    const held: ChromeRead = {id: crypto.randomUUID(), chrome, fence, channel, fenceKey, runtime, live: true, bindings, source};
    publishRead(held);
    const abort = new AbortController();
    const admitted = () => {
      if (!readLive || !held.live || !chrome.live || !chrome.screen.isCurrent() || abort.signal.aborted) return false;
      const value = runtime.getSnapshot(), currentFence = value.fence, currentChannel = value.playback.linear?.channel;
      return !!currentFence && currentFence.kind === 'channel' && !!currentChannel
        && guideReadKey(currentFence, value.playback.linear?.programme) === held.fenceKey
        && currentChannel.channelId === held.channel.channelId && currentChannel.kind === held.channel.kind
        && currentChannel.sourceId === held.channel.sourceId && currentChannel.generation === held.channel.generation;
    };
    const run = async () => {
      const current = await held.bindings.selectedChannel({sourceId: held.channel.sourceId, channelId: held.channel.channelId, provenance: held.channel.kind}, abort.signal);
      if (!current || current.generation !== held.channel.generation || !admitted()) return;
      const row = legacyChannelRow(current);
      const down = source.neighbor ? await source.neighbor(row, -1, abort.signal).catch(() => undefined) : undefined;
      if (!admitted()) return;
      const up: GuideChannelRow[] = [];
      let anchor: GuideChannelRow | undefined = row;
      for (let index = 0; index < 3 && anchor; index++) {
        const next: GuideChannelRow | undefined = source.neighbor ? await source.neighbor(anchor, 1, abort.signal).catch(() => undefined) : undefined;
        if (!admitted()) return;
        if (!next) break;
        up.push(next);anchor = next;
      }
      const rows = [down, row, ...up].filter((item): item is GuideChannelRow => !!item);
      const programs = await source.programs(rows.map(item => item.id), Date.now(), Date.now() + 1, abort.signal);
      if (!admitted()) return;
      const programmes = new Map<string, GuideProgramme>();
      for (const item of rows) {
        const raw = legacyChannel(item); if (!raw) continue;
        const page = programs[item.id]?.find(programme => programme.start <= Date.now() && Date.now() < programme.end);
        const rawProgramme = page ? legacyProgramme(page) : undefined;
        if (rawProgramme) programmes.set(item.id, rawProgramme);
      }
      if (!admitted()) return;
      setFacts({chromeId: chrome.id, readId: held.id, fenceKey: held.fenceKey, current, rows, programmes});
    };
    void run().catch(() => {});
    return () => {readLive = false;held.live = false;abort.abort();bindings.dispose();readLife.dispose();};
    // expanded is presentation-only and deliberately absent from the source/facts lifetime.
  }, [chrome, fenceKey, refresh, runtime]);
  const read = publishedRead?.live && publishedRead.chrome === chrome && publishedRead.fenceKey === fenceKey && publishedRead.runtime === runtime ? publishedRead : undefined;
  const liveFacts = facts && chrome && read && facts.chromeId === chrome.id && facts.readId === read.id && facts.fenceKey === fenceKey ? facts : undefined;
  const [recorded, setRecorded] = useState<Readonly<{programmeId: string; recording: Recording}>>();
  const recordingClaim = useRef<RecordClaim | undefined>(undefined);
  useLayoutEffect(() => {setRecorded(undefined);}, [fenceKey]);
  const [publishedAction, publishAction] = useState<ChromeAction>();
  // Publish only at commit, then sample once for this setup's original lifetime.
  const setupPorts1 = useRef({snapshot});
  useLayoutEffect(() => {setupPorts1.current = {snapshot};});
  useLayoutEffect(() => {
    const {snapshot} = setupPorts1.current;
    if (!chrome || !read || !expanded || !runtime || !snapshot) return;
    const held: ChromeAction = {id: crypto.randomUUID(), chrome, read, fence: read.fence, channel: read.channel, fenceKey: read.fenceKey,
      mediaAuthority: snapshot.authority, runtime, live: true};
    publishAction(held);
    return () => {held.live = false;};
    // expanded is the action witness only; authority/read/facts stay alive across it.
  }, [chrome, read, expanded, runtime, snapshot?.authority]);
  const action = publishedAction?.live && publishedAction.chrome === chrome && publishedAction.read === read && publishedAction.runtime === runtime ? publishedAction : undefined;

  const isCurrent = () => {
    if (!action || !chrome || !read) return false;
    return action.live && chrome.live && chrome.screen.isCurrent() && read.live
      && liveChannelCurrent(action.fence, action.mediaAuthority, action.runtime.getSnapshot());
  };
  if (!chrome || !read || !action || !runtime || !snapshot || !expanded || !fence || !linear || !presentation || !fenceKey) return undefined;

  const currentFence = action.fence;
  const shownChannel = !!liveFacts ? asChromeChannel(liveFacts.current) : undefined;
  const currentGuide = liveFacts?.rows.find(row => row.id === currentFence.channelId);
  const currentGuideProgramme = liveFacts?.programmes.get(currentFence.channelId);
  const guideProgramme = liveFacts && linear.programme
    ? (currentGuideProgramme?.id === linear.programme.id ? currentGuideProgramme : liveFacts.current.programmes.find(programme => programme.id === linear.programme!.id)) : undefined;
  const recordKnown = liveRecordCommand(!!guideProgramme?.recordingId, guideProgramme?.recordingState) !== 'none';
  const authoritative: LiveChromeChannel = shownChannel && currentGuide ? {...shownChannel, recordAvailable: shownChannel.recordAvailable && !!guideProgramme && recordKnown} : {id: currentFence.channelId, sourceId: linear.channel.sourceId, kind: currentFence.channelKind, generation: currentFence.channelGeneration,
    number: '', name: linear.name, favorite: false, recordAvailable: false, tuneAvailable: true};
  const factsRecording = recordingState(guideProgramme);
  const localRecording = recorded && recorded.programmeId === linear.programme?.id ? recorded.recording.state : undefined;
  const currentRecording = factsRecording ?? localRecording;
  const programme: LiveChromeProgramme | undefined = linear.programme ? {id: linear.programme.id, title: linear.programme.title,
    startMs: linear.programme.startMs, endMs: linear.programme.endMs, ...(currentRecording ? {recording: currentRecording} : {})} : undefined;
  const position = presentation.positionSeconds;
  const timeline = channelTimeline(linear, position), origin = linear.media.originMs;
  const miniGuide: LiveChromeGuideChannel[] = liveFacts ? liveFacts.rows.map(row => asMini(row,
    row.id === currentFence.channelId ? guideProgramme : liveFacts.programmes.get(row.id))).filter((row): row is LiveChromeGuideChannel => !!row) : [];
  const currentIndex = liveFacts ? liveFacts.rows.findIndex(row => row.id === currentFence.channelId) : -1;
  const downRow = currentIndex > 0 ? liveFacts!.rows[currentIndex - 1] : undefined;
  const upRow = currentIndex >= 0 ? liveFacts!.rows[currentIndex + 1] : undefined;
  const down = downRow ? asMini(downRow, liveFacts!.programmes.get(downRow.id)) : undefined;
  const up = upRow ? asMini(upRow, liveFacts!.programmes.get(upRow.id)) : undefined;
  const guideTarget = (target: LiveChromeGuideTarget): GuideChannel | undefined => {
    // The facts' own native row: the display row maps a library channel's
    // sourceId to "library", so compare the source, generation and provenance
    // the native row actually carries, never a later store row.
    const row = liveFacts?.rows.find(item => {
      const raw = legacyChannel(item);
      return !!raw && item.id === target.channelId && raw.sourceId === target.sourceId
        && raw.generation === target.generation && raw.provenance === target.kind;
    });
    const raw = row ? legacyChannel(row) : undefined;
    return raw && raw.tuneAvailable ? raw : undefined;
  };
  const tuneTarget = async (target: LiveChromeGuideTarget): Promise<void> => {
    if (!isCurrent()) return;
    const raw = guideTarget(target);
    if (!raw) return;
    await action.runtime.playFromGuide(raw, {signal: read.bindings.life.signal(), isCurrent: () => isCurrent() && read.live});
  };
  const guideRowTarget = (row: LiveChromeGuideChannel): LiveChromeGuideTarget => ({sourceId: row.channel.sourceId, channelId: row.channel.id,
    generation: row.channel.generation, kind: row.channel.kind});
  const record = async (): Promise<void> => {
    const guideChannel = liveFacts?.current, programme = guideProgramme;
    if (!isCurrent() || !guideChannel || !programme) return;
    const active = recordingClaim.current;
    if (active?.live && active.actionId === action.id && active.readId === read.id && active.programmeId === programme.id) return;
    const claim: RecordClaim = {id: crypto.randomUUID(), actionId: action.id, readId: read.id, programmeId: programme.id, live: true};
    recordingClaim.current = claim;
    const settle = () => {claim.live = false;if (recordingClaim.current === claim) recordingClaim.current = undefined;};
    const claimCurrent = () => recordingClaim.current === claim && claim.live && isCurrent();
    const local = recorded?.programmeId === programme.id ? recorded.recording : undefined;
    try {
      if (programme.recordingId) {
        if (liveRecordCommand(true, programme.recordingState) !== 'cancel') return;
        const detail = await chrome.client.detail(programme.recordingId);
        if (!claimCurrent()) return;
        await chrome.client.cancel(detail.recording);
        if (!claimCurrent()) return;
        setRecorded(undefined);setRefresh(value => value + 1);return;
      }
      if (local) {
        if (liveRecordCommand(true, local.state) !== 'cancel') return;
        await chrome.client.cancel(local);
        if (!claimCurrent()) return;
        setRecorded(undefined);setRefresh(value => value + 1);return;
      }
      const command = {signal: AbortSignal.any([chrome.life.signal(), read.bindings.life.signal()]), isCurrent: claimCurrent};
      const scheduled = await chrome.client.schedule({channel: guideChannel, programme, series: false}, defaultRecordingOptions, command);
      if (!claimCurrent()) return;
      setRecorded({programmeId: programme.id, recording: scheduled});
      setRefresh(value => value + 1);
    } finally {settle();}
  };
  const goLive = async (): Promise<void> => { if (isCurrent()) action.runtime.goLive(currentFence); };
  const port: LiveChromePort = {
    fence: currentFence, channel: authoritative, ...(programme ? {programme} : {}),
    ...(linear.next ? {nextProgramme: {title: linear.next.title, startMs: linear.next.startMs, endMs: linear.next.endMs}} : {}),
    window: {...(linear.programme ? {programmeStartMs: linear.programme.startMs, programmeEndMs: linear.programme.endMs} : {}),
      mediaOriginMs: origin, seekStartMs: origin + timeline.start * 1000, seekEndMs: origin + timeline.end * 1000, liveEdgeMs: origin + timeline.edge * 1000,
      positionMs: origin + position * 1000, behindSeconds: timeline.behind, atLive: timeline.behind <= 2},
    ...(down ? {down} : {}), ...(up ? {up} : {}), miniGuide, skipBackSeconds: skips.back, skipForwardSeconds: skips.forward,
    isCurrent, record, goLive,
    tune: async direction => { const row = direction === -1 ? down : up; if (row) await tuneTarget(guideRowTarget(row)); },
    tuneTo: tuneTarget,
    openFullGuide: () => { if (liveNavigationAllowed(chrome.screen.isCurrent(), isCurrent())) void router.navigate({to: '/live', search: {tab: 'guide'}}).catch(() => {}); },
    openRecordings: () => { if (liveNavigationAllowed(chrome.screen.isCurrent(), isCurrent())) void router.navigate({to: '/live', search: {tab: 'recordings', dvr: 'upcoming'}}).catch(() => {}); },
  };
  return port;
}
