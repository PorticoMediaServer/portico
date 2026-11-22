import React, {useCallback, useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {ScrollView, View} from 'react-native';
import {sameFence, type PlaybackFence} from '../../../../../../packages/player-core/src';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {NativePlaybackAdapter} from '../../../platform/playback/types';
import type {NativePlaybackSubtitleControls} from '../../../platform/playback/subtitle-controls';
import {useFocusRegistry} from '../../../kit/focus';
import {useTVPanelInteractive} from '../../../kit/overlays/TVPanelMotion';
import {useOverlayLifecycle} from '../../../kit/overlays/OverlayHost';
import {useTheme} from '../../../kit/theme';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {type ShellAuthority} from '../../../shell/model';
import type {NativeWatchHostContext} from './useWatchHost';
import {type NativeWatchPanelNames} from '../../../screens/watch/panel-model';
import type {NativeWatchPanelOptions} from '../../../screens/watch/panel-options';

export type NativeWatchPanelsProps = Readonly<{
  context: NativeWatchHostContext;
  options: NativeWatchPanelOptions;
  playback: NativePlaybackAdapter;
  names: NativeWatchPanelNames;
  /** Exact localized frame copy; supplied by the genuine locale binding. */
  copy: Readonly<{sessionLead: string; sessionDefaults: string; audioContinues: string}>;
  reportError: (error: unknown) => void;
  renderError: (error: unknown, retry: () => void) => React.ReactNode;
  /** Rich current-fence producer content, never the old generic File info/title details. */
  playbackInfo?: (context: NativeWatchHostContext) => React.ReactNode;
  episodes?: (context: NativeWatchHostContext) => React.ReactNode;
  /** The current source's subtitle writer: adding a found track, searching a provider, applying one. */
  subtitleControls?: (fence: PlaybackFence) => NativePlaybackSubtitleControls | undefined;
  /** The viewer's sleep timer for this playback. */
  sleep?: Readonly<{minutes: number | null; set: (fence: PlaybackFence, minutes: number | null) => Promise<void>}>;
  /** Subtitle style, read from the server's own preference registry. */
  style?: NativeWatchStylePort;
}>;

export type NativeWatchStyleField = Readonly<{key: string; label: string; current: string | undefined; values: readonly Readonly<{value: string; label: string}>[]}>;
export type NativeWatchStylePort = Readonly<{fields: readonly NativeWatchStyleField[]; apply: (key: string, value: string) => Promise<void>}>;

/** The exact page a report row was drawn on: the fence it rendered for, and the
 * shell's route snapshot at that same render. A fence alone does not prove the
 * page — the same expanded output can survive away→Back — so identity of the
 * route snapshot is part of the proof. */
export type NativeReportOpening = Readonly<{
  fence: PlaybackFence;
  navigation: ReturnType<NonNullable<NativeShellRuntime['navigation']>['getSnapshot']>;
}>;
export type NativeReportAsk = NativeReportOpening & Readonly<{controller: AbortController; ticket: number}>;

/** The exact page a queue row was drawn on. An accepted output change does not move it; a route
 * or authority retirement does. A missing route is never proof, so no row is offered without it. */
export type NativeUpNextOpening = Readonly<{
  fence: PlaybackFence;
  route: ReturnType<NonNullable<NativeShellRuntime['navigation']>['getSnapshot']>;
  authority: ShellAuthority;
  queueId: string;
  revision: string;
  entryId: string;
}>;

/** The player panels' state and commands: the panel options' load, the report ask and its consent, the command epoch,
 * focus entry and scroll restore. Moved from screens/watch/WatchPanels as it was; `WatchPanelsView` draws the panels. */
export function useWatchPanels({context, options, playback, reportError}: NativeWatchPanelsProps) {
  const {source, fence, state} = context, theme = useTheme(), t = defaultI18n.t, registry = useFocusRegistry();
  const panelInteractive = useTVPanelInteractive();
  // tablet-play2b: an expanded-size-class device draws its choice rows at 48 pt
  // and its Timing buttons at 44 pt; the phone keeps 52 pt rows.
  const tablet = !theme.isTV && Math.min(context.renderer.width, context.renderer.height) >= 600;
  const choices = useSyncExternalStore(options.subscribe, options.getSnapshot);
  const first = useRef<View>(null), scroll = useRef<ScrollView>(null), busy = useRef(false), commandEpoch = useRef(0), reportCancel = useRef<View>(null);
  const [pending, setPending] = useState(false), [subtitleSearching, setSubtitleSearching] = useState(false), [subtitleReasonText, setSubtitleReasonText] = useState<string>();
  const latest = useRef(context); latest.current = context;
  // The report consent belongs to the exact page it was drawn on: that row's fence
  // and the shell route snapshot from the same render, one controller, one ticket.
  const [reportAsk, setReportAsk] = useState<NativeReportAsk>();
  const [reportBusy, setReportBusy] = useState(false), [reportFailure, setReportFailure] = useState<unknown>(), [reportSent, setReportSent] = useState(false);
  const reportTicket = useRef(0), reportActive = useRef<NativeReportAsk | undefined>(undefined),
    reportPending = useRef<NativeReportAsk | undefined>(undefined), reportAlive = useRef(true);
  const reportOff = useRef<readonly (() => void)[]>([]);
  // Neither the page nor the route may have moved; never re-anchor to what is latest.
  const reportOnPage = useCallback((opening: NativeReportOpening): boolean => {
    const snapshot = playback.getSnapshot(), shell = latest.current.runtime.navigation;
    return !snapshot.retired && sameFence(snapshot.fence, opening.fence)
      && sameFence(latest.current.fence, opening.fence) && sameFence(latest.current.state.fence, opening.fence)
      && latest.current.state.panel === 'options' && latest.current.panelCurrent() && !!shell && shell.getSnapshot() === opening.navigation;
  }, [playback]);
  const reportHeld = useCallback((asked: NativeReportAsk | undefined): asked is NativeReportAsk => !!asked && reportAlive.current
    && reportActive.current === asked && !asked.controller.signal.aborted, []);
  const reportCurrent = useCallback((asked: NativeReportAsk | undefined): asked is NativeReportAsk => reportHeld(asked) && reportOnPage(asked), [reportHeld, reportOnPage]);
  const reportDetach = useCallback(() => {
    const stops = reportOff.current; reportOff.current = [];
    stops.forEach(stop => stop());
  }, []);
  // Only the exact opening still held may be retired, so a retained control from an
  // older sheet is refused and a successor's state is never touched. While mounted
  // the retirement also clears its own rendered UI; the unmount cleanup passes
  // notify=false and writes no state at all.
  const reportDrop = useCallback((asked: NativeReportAsk, notify: boolean) => {
    if (reportActive.current !== asked) return false;
    reportActive.current = undefined;
    asked.controller.abort();
    reportDetach();
    // Clear only what this opening rendered, and only while mounted; the unmount
    // cleanup passes notify=false and writes no state at all.
    if (notify && reportAlive.current) {
      setReportAsk(shown => shown === asked ? undefined : shown);
      setReportBusy(false); setReportFailure(undefined);
    }
    return true;
  }, [reportDetach]);
  const reportAttach = useCallback((asked: NativeReportAsk) => {
    reportDetach();
    const shell = latest.current.runtime.navigation;
    if (!shell) return;
    const check = () => {if (reportActive.current === asked && !reportCurrent(asked)) reportDrop(asked, true);};
    reportOff.current = [shell.subscribe(check), playback.subscribe(check)];
  }, [playback, reportDetach, reportDrop, reportCurrent]);
  // Dismiss and Cancel are bound to the object that sheet rendered, never to
  // whatever happens to be active when the handler runs.
  const closeReport = useCallback((asked: NativeReportAsk) => {reportDrop(asked, true);}, [reportDrop]);
  const openReport = useCallback((opening: NativeReportOpening) => {
    // A retained row whose own page or route moved before the press opens nothing.
    if (!context.runtime.reportPlaybackProblem || !reportOnPage(opening)) return;
    reportDetach();
    const held = reportActive.current;
    reportActive.current = undefined;
    held?.controller.abort();
    const asked: NativeReportAsk = {...opening, controller: new AbortController(), ticket: ++reportTicket.current};
    reportActive.current = asked;
    setReportAsk(asked); setReportBusy(false); setReportFailure(undefined); setReportSent(false);
    reportAttach(asked);
  }, [context.runtime.reportPlaybackProblem, reportAttach, reportDetach, reportOnPage]);
  const sendReport = useCallback(() => {
    const port = context.runtime.reportPlaybackProblem, asked = reportAsk;
    // The same ticket cannot send twice; a later opening has its own ticket.
    if (!port || !reportCurrent(asked) || reportPending.current === asked) return;
    reportPending.current = asked;
    setReportBusy(true); setReportFailure(undefined);
    void port({signal: asked.controller.signal, current: () => reportCurrent(asked)}).then(() => {
      if (!reportCurrent(asked)) return;
      reportActive.current = undefined; reportPending.current = undefined; reportDetach();
      setReportAsk(undefined); setReportBusy(false); setReportSent(true);
    }, failure => {if (reportCurrent(asked)) setReportFailure(failure);}).finally(() => {
      if (reportPending.current === asked) reportPending.current = undefined;
      if (reportCurrent(asked)) setReportBusy(false);
    });
  }, [context.runtime.reportPlaybackProblem, reportAsk, reportDetach, reportCurrent]);
  const fenceKey = JSON.stringify(fence);
  const entered = useRef<{key: string; target: typeof state.returnTarget} | undefined>(undefined);
  const entryKey = JSON.stringify([fence, state.panel]);
  const current = useCallback(() => !playback.getSnapshot().retired && sameFence(playback.getSnapshot().fence, fence)
    && sameFence(latest.current.fence, fence) && sameFence(latest.current.state.fence, fence)
    && latest.current.state.panel === state.panel && !!state.panel && latest.current.panelCurrent(), [playback, fence, state.panel]);
  const back = () => {if (current()) context.backPanel();};
  useOverlayLifecycle(!!state.panel, true, back, theme.isTV ? undefined : first, undefined, 0, 'watch-panel', !theme.isTV);
  useEffect(() => {const expected = latest.current.fence; if (expected.kind === 'queue') void options.load(expected); else options.clear(); return () => options.clear();}, [options, fenceKey]);
  useEffect(() => {const epoch = commandEpoch.current + 1; commandEpoch.current = epoch; busy.current = false; setPending(false);
    return () => {commandEpoch.current = epoch + 1;};}, [fenceKey, state.panel]);
  useEffect(() => {scroll.current?.scrollTo({y: state.panelOffset, animated: false});}, [state.panel, state.panelOffset]);
  useEffect(() => {
    if (!panelInteractive || !current() || entered.current?.key === entryKey && entered.current.target === state.returnTarget) return;
    if (state.returnTarget && registry.focus(state.returnTarget)) {entered.current = {key: entryKey, target: state.returnTarget}; return;}
    if (first.current?.requestTVFocus) {first.current.requestTVFocus(); entered.current = {key: entryKey, target: state.returnTarget};}
  }, [entryKey, state.returnTarget, choices.phase, panelInteractive, current, registry]);
  useEffect(() => {setReportSent(false);}, [fenceKey]);
  // Unmount retirement writes no state at all, and the setup restores the mounted
  // witness if the effect replays; a passive cleanup would leave a window in which a
  // send could still reach a page that is already gone.
  useLayoutEffect(() => {
    reportAlive.current = true;
    return () => {
      reportAlive.current = false;
      const held = reportActive.current;
      if (held) reportDrop(held, false); else reportDetach();
    };
  }, [reportDrop, reportDetach]);
  // A panel or source change retires the exact opening, with its own rendered UI
  // state, before paint; navigation and playback publishes do the same from their own
  // subscriptions. Nothing else about the panel's commands is widened.
  useLayoutEffect(() => {
    const asked = reportActive.current;
    if (!asked || reportCurrent(asked)) return;
    reportDrop(asked, true);
  }, [state.panel, fenceKey, reportCurrent, reportDrop]);
  const run = async (command: () => Promise<void>) => {
    if (busy.current || !current()) return;
    const epoch = commandEpoch.current;
    busy.current = true; setPending(true);
    try {await command();} catch (error) {if (epoch === commandEpoch.current && current()) reportError(error);}
    finally {if (epoch === commandEpoch.current) {busy.current = false; if (current()) setPending(false);}}
  };
  return {source, fence, state, theme, t, panelInteractive, tablet, choices, first, scroll, busy, reportCancel, pending, subtitleSearching, setSubtitleSearching, subtitleReasonText, setSubtitleReasonText, reportAsk, reportBusy, reportFailure, reportSent, reportCurrent, closeReport, openReport, sendReport, current, back, run};
}
