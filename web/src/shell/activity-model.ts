import {playbackIdentity, type CardSubject, type PlaybackSubject, type TimeEnvironment} from '@core/presentation/kit-model/index.ts';
import {activityIssueCopy, type ActivitySummarySession, type ActivitySummarySnapshot, type OwnerActivitySummary} from '@core/admin/activity-summary/index.ts';
import {jobName, scanStepName} from '@core/server-admin/console-catalogue.ts';
import {heightText} from '@core/presentation/kit-model/technical.ts';
import {defaultI18n} from '@i18n';
import type {ActivityMajorIssueView, ActivityPanelView} from '../kit/shell';

/** The server's qualified issue points at its own existing settings destination. */
export type ActivityIssueDestination = Readonly<{kind: string; target: string}>;
export type ActivityIssueAction = (destination: ActivityIssueDestination) => void;
type ActivityActions = Pick<ActivityPanelView, 'onDashboard' | 'onRetry'> & {openUpdate: () => void; openIssue: ActivityIssueAction};

/** One glanceable problem, chosen by the server's own order; the Settings page owns the detail. */
export function activityMajorIssue(issue: OwnerActivitySummary['majorIssues'][number] | undefined, t: typeof defaultI18n.t, openIssue: ActivityIssueAction): ActivityMajorIssueView | undefined {
  if (!issue) return undefined;
  const copy = activityIssueCopy(issue);
  if (!copy) return undefined;
  return {title: t(copy.title, copy.values), description: t(copy.detail), onOpen: () => openIssue({kind: issue.destination.kind, target: issue.destination.target})};
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

/** Semantic facts only. The canonical formatter owns episode identity and fact order. */
export function activitySubject(stream: ActivitySummarySession): PlaybackSubject | undefined {
  const item = stream.item;
  if (!item || !['movie', 'episode', 'extra', 'song', 'audiobook_file'].includes(item.kind)) return undefined;
  const subject: CardSubject = {id: item.id, kind: item.kind as CardSubject['kind'], title: item.title, year: item.year, artist: item.artist, album: item.album,
    ...(item.showId && item.showTitle ? {show: {id: item.showId, title: item.showTitle}} : {}),
    ...(item.kind === 'episode' ? {episode: item.episodeNumbering === 'date' ? {mode: 'date' as const, day: item.airDate}
      : item.episodeNumbering === 'absolute' ? {mode: 'absolute' as const, number: item.episodeNumber}
      : item.seasonNumber === 0 ? {mode: 'special' as const} : {mode: 'seasonal' as const, season: item.seasonNumber, episode: item.episodeNumber}} : {}),
  };
  return {kind: item.kind === 'song' ? 'music' : item.kind === 'audiobook_file' ? 'audiobook' : 'video', item: subject};
}
export function activityView(document: OwnerActivitySummary, time: TimeEnvironment, actions: ActivityActions): ActivityPanelView {
  const {t} = defaultI18n;
  // health.degraded includes watchdog recovery as well as failed probes/corruption.
  // It cannot qualify a major issue; only the server's qualified, ordered
  // playback-stop/data-risk facts do, using their existing settings destination.
  const majorIssue = activityMajorIssue(document.majorIssues[0], t, actions.openIssue);
  return {serverName: document.serverName, health: document.health.degraded ? 'attention' : 'normal', streamCount: document.streams.count, streamOverflow: document.streams.overflow, taskCount: document.tasks.count,
    ...(majorIssue ? {majorIssue} : {}),
    // This bounded set is already selected globally. Preserve its order and exact overflow facts.
    streams: document.streams.items.map(stream => {
      const subject = activitySubject(stream);
      const video = stream.decision.video?.action === 'transcode' || stream.decision.video?.action === 'burn' || stream.decision.subtitles?.action === 'burn';
      const height = heightText(stream.videoHeight, time.t);
      const conversion = stream.converting && video && height
        ? {id: 'delivery', text: t('next.home.shell.convertingTo', {quality: height}), role: 'status' as const}
        : stream.converting ? {id: 'delivery', text: t(video ? 'web.nowPlaying.delivery.convertVideo' : 'web.nowPlaying.delivery.convertAudio'), role: 'status' as const} : undefined;
      return {id: stream.id, identity: subject ? playbackIdentity(subject, 'dock', time) : undefined, title: stream.item?.title, username: stream.user.username, name: stream.profile.name, device: stream.device.name,
        startedAt: stream.startedAt, initial: Array.from(stream.profile.name.trim())[0] ?? '', positionMs: stream.positionMs, durationMs: stream.durationMs, paused: stream.state === 'paused',
        problem: stream.problem ? {id: 'problem', text: t(stream.state === 'buffering' ? 'web.together.loading' : 'next.shell.activityProblem'), role: 'status' as const} : undefined,
        conversion, onOpen: () => actions.onDashboard('streams')};
    }),
    scans: document.scans.map(scan => ({id: scan.libraryId, title: scan.libraryName, detail: scanStepName(t, defaultI18n.has, scan.phase), current: scan.current,
      icon: 'i-refresh', processed: scan.analyzed, discovered: scan.discovered, total: scan.total, onOpen: () => actions.onDashboard('activity')})),
    // The drawn row is the task, then the library it works on, then the count of what is done:
    // real totals only, in the Dashboard's own words, because the two surfaces never disagree.
    tasks: document.tasks.items.map(task => ({id: task.id, title: jobName(t, defaultI18n.has, task.kind), detail: task.libraryName,
      icon: task.kind === 'trickplay' ? 'i-image' : 'i-server', processed: task.processed, total: task.total,
      progressText: task.processed !== undefined && task.total !== undefined
        ? t(task.unit === 'episodes' ? 'next.dashboard.taskProgressEpisodes' : task.unit === 'files' ? 'next.dashboard.taskProgressFiles' : 'next.dashboard.taskProgress', {done: task.processed, total: task.total})
        : undefined,
      onOpen: () => actions.onDashboard('activity')})),
    update: {...document.update, onOpen: actions.openUpdate},
    ...(document.lastScanFinishedAt ? {lastScan: defaultI18n.relativeTime(document.lastScanFinishedAt, Date.parse(document.observedAt))} : {}),
    phase: 'ready', onRetry: actions.onRetry, onDashboard: actions.onDashboard,
  };
}
