import type {TimeEnvironment} from '@core/presentation/kit-model/index.ts';
import {activityIssueCopy, type ActivitySummaryAlert, type ActivitySummarySnapshot, type OwnerActivitySummary} from '@core/admin/activity-summary/index.ts';
import {jobName, scanStepName} from '@core/server-admin/console-catalogue.ts';
import {defaultI18n} from '@i18n';
import type {ActivityMajorIssueView, ActivityPanelView} from '../kit/shell';

/** The server's qualified issue points at its own existing settings destination. */
export type ActivityIssueDestination = Readonly<{kind: string; target: string}>;
export type ActivityIssueAction = (destination: ActivityIssueDestination) => void;
type ActivityActions = Pick<ActivityPanelView, 'onDashboard' | 'onRetry'> & {openUpdate: () => void; openIssue: ActivityIssueAction};

/** One glanceable problem: the server's first major alert, in its own order; the Settings page owns the detail. */
export function activityMajorIssue(alert: ActivitySummaryAlert | undefined, t: typeof defaultI18n.t, openIssue: ActivityIssueAction, onDashboard: ActivityPanelView['onDashboard'] = () => {}): ActivityMajorIssueView | undefined {
  if (!alert) return undefined;
  const copy = activityIssueCopy({code: alert.code, subject: alert.params.subject});
  if (alert.code === 'data_folder_readable' || !copy) return {
    title: alert.code === 'data_folder_readable' ? t('next.dashboard.permissionsAlert') : alert.params.message || t('next.dashboard.unknownAlert'),
    description: alert.code === 'data_folder_readable' && alert.params.message ? alert.params.message : t('next.dashboard.openAlerts'), onOpen: () => onDashboard(),
  };
  return {title: t(copy.title, copy.values), description: t(copy.detail), onOpen: () => openIssue({kind: alert.action.kind, target: alert.action.target})};
}

/** The existing Settings page that explains and resolves the issue; an unknown kind has none. */
export function activityIssueSettings(kind: string): string | undefined {
  switch (kind) {
    case 'library-source': return 'libraries';
    case 'remote-source': return 'remote-sources';
    case 'storage': case 'backups': return 'storage';
    case 'networking': return 'remote';
    default: return undefined;
  }
}

/** Cold, refreshing and failed reads do not establish normal health or zero work. */
export function activityPanelView(snapshot: ActivitySummarySnapshot, serverName: string, time: TimeEnvironment, actions: ActivityActions): ActivityPanelView {
  if (snapshot.phase === 'ready' && snapshot.document) return activityView(snapshot.document, time, actions);
  return {serverName, streams: [], scans: [], tasks: [], phase: snapshot.phase === 'error' || snapshot.phase === 'unavailable' ? 'error' : 'loading', onRetry: actions.onRetry, onDashboard: actions.onDashboard};
}

export function activityView(document: OwnerActivitySummary, _time: TimeEnvironment, actions: ActivityActions): ActivityPanelView {
  const {t} = defaultI18n;
  // The server sends only its major alerts and update_available, most urgent first (console rule 4).
  const major = document.alerts.find(alert => alert.major), update = document.alerts.find(alert => alert.code === 'update_available' && !!alert.params.latest);
  const majorIssue = activityMajorIssue(major, t, actions.openIssue, actions.onDashboard);
  const streams = document.streams, shown = streams.items;
  return {serverName: document.serverName, health: major ? 'attention' : 'normal', streamCount: streams.count, taskCount: document.tasks.count,
    // The rest of Now playing, in the server's own counts: the line under the three streams shown.
    streamOverflow: {count: streams.count - shown.length, convertingCount: streams.converting - shown.filter(stream => stream.route === 'converted').length},
    ...(majorIssue ? {majorIssue} : {}),
    // This bounded set is already selected by the server, in Now playing's order. The server names the title, the
    // person and the device; it sends no artwork, episode numbering or buffering facts for a stream.
    streams: shown.map(stream => {
      const name = stream.profile || stream.username;
      const video = stream.delivery.video?.action === 'transcode' || stream.delivery.video?.action === 'burn' || stream.delivery.subtitles?.action === 'burn';
      const conversion = stream.route === 'converted' ? {id: 'delivery', text: t(video ? 'web.nowPlaying.delivery.convertVideo' : 'web.nowPlaying.delivery.convertAudio'), role: 'status' as const} : undefined;
      return {id: stream.id, ...(stream.title ? {title: stream.title} : {}), username: stream.username, name, device: stream.deviceName,
        startedAt: stream.startedAt, initial: Array.from(name.trim())[0] ?? '', positionMs: stream.positionMs, durationMs: stream.durationMs, paused: stream.state === 'paused',
        conversion, onOpen: () => actions.onDashboard('streams')};
    }),
    scans: document.scans.map(scan => ({id: scan.libraryId, title: scan.libraryName, detail: scanStepName(t, defaultI18n.has, scan.phase),
      icon: 'i-refresh', processed: scan.analyzed, discovered: scan.discovered, total: scan.total, onOpen: () => actions.onDashboard('activity')})),
    // The drawn row is the task, then the library it works on, then the count of what is done:
    // real totals only, in the Dashboard's own words, because the two surfaces never disagree.
    tasks: document.tasks.items.map(task => ({id: task.id, title: jobName(t, defaultI18n.has, task.kind), detail: task.libraryName,
      icon: task.kind === 'trickplay' ? 'i-image' : 'i-server', processed: task.processed, total: task.total,
      progressText: task.processed !== undefined && task.total !== undefined ? t('next.dashboard.taskProgress', {done: task.processed, total: task.total}) : undefined,
      onOpen: () => actions.onDashboard('activity')})),
    update: update ? {available: true, latest: update.params.latest, current: update.params.current, onOpen: actions.openUpdate} : {available: false, onOpen: actions.openUpdate},
    ...(document.lastScanAt ? {lastScan: defaultI18n.relativeTime(document.lastScanAt, Date.parse(document.observedAt), {inSentence: true})} : {}),
    phase: 'ready', onRetry: actions.onRetry, onDashboard: actions.onDashboard,
  };
}
