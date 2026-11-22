import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {dashboardAlertWords} from '../../../../../../packages/client-core/src/admin/dashboard-alert-words';
import {formatBytes} from '../../../../../../packages/client-core/src/presentation/content';
import {jobName} from '../../../../../../packages/client-core/src/server-admin/console-catalogue';
import {playedFraction} from '../../../../../../packages/client-core/src/server-admin/now-playing';
import {sessionFactParts} from '../../../../../../packages/client-core/src/presentation/kit-model';
import {Button} from '../../../kit/controls';
import {FocusRegion} from '../../../kit/focus';
import type {ServerFact} from '../../../kit/shell';
import {StateView} from '../../../kit/states';
import {cardAccessibilityLabel} from '../../../kit/cards/model';
import {TVDashboardBody, TVDashboardHead, TVDashboardItem, TVDashboardMore, TVDashboardSectionLabel, TVDashboardSession, TVDiskUsage} from '../../../kit/dashboard/TVDashboardParts';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {NativeDashboardController} from '../../../screens/settings-live/dashboard-controller';
import {nativeDashboardIdentity} from '../../../screens/settings-live/dashboard-presentation';
import {useServerDashboard} from '../../shared/dashboard/useServerDashboard';
import {useDiskUsage} from '../../shared/dashboard/useDiskUsage';

/** The Dashboard on TV (TV35), in Server settings beside the page list (`column`): Now playing, then for the owner Alerts,
 * Background tasks and Disk usage. TV never offers a mutation, secret, settings document or inferred administrator action. */
export function DashboardPage({controller, ports, runtime, column, rows}: {
  controller: NativeDashboardController; ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; column: string; rows: string;
}) {
  const t = defaultI18n.t;
  const state = useServerDashboard({controller, ports, runtime});
  // The rebuilt server's facts: needs attention while any alert is major, free space and the database's size.
  const health = state.health ?? {degraded: false};
  const disk = useDiskUsage({enabled: state.admission === 'owner', context: ports.context, runtime, freeBytes: health.freeBytes, databaseBytes: health.databaseBytes});
  if (state.admission === 'denied') return null;
  // Only the owner's header says how the server is: an administrator is sent only some alerts (console rule 4).
  const facts: ServerFact[] = state.health && state.admission === 'owner' ? [{id: 'state', text: t(health.degraded ? 'next.shell.health.attention' : 'next.shell.health.normal'), tone: health.degraded ? 'warning' : 'normal'}] : [];
  if (health.uptimeSeconds !== undefined) facts.push({id: 'uptime', text: t('settings.server.health.upFor', {duration: defaultI18n.duration(health.uptimeSeconds)})});
  if (health.freeBytes !== undefined) facts.push({id: 'free', text: t('settings.server.health.free', {bytes: formatBytes(health.freeBytes)})});
  if (health.databaseBytes !== undefined) facts.push({id: 'database', text: t('settings.server.health.database', {bytes: formatBytes(health.databaseBytes)})});
  const sessions = state.sessionWindow, jobs = state.jobWindow;
  return <TVDashboardBody>
    <TVDashboardHead serverName={ports.serverName()} role={state.admission} title={t('next.shell.dashboard')} facts={facts}/>
    <FocusRegion id={rows} entry={state.sessions[0]?.id} orientation="vertical" edges={{left: column}}>
      <TVDashboardSectionLabel first>{t('next.shell.nowPlaying')}</TVDashboardSectionLabel>
      {state.error !== undefined && <StateView {...runtime.error(state.error, () => {void controller.refresh();}, 'server-console')}/>}
      {!state.loading && state.error === undefined && !state.sessions.length && <StateView title={t('web.nowPlaying.empty')}/>}
      {state.sessions.map((session, index) => {
        let lease = controller.sessionFocusToken(session.id);
        const identity = nativeDashboardIdentity(session, runtime.text);
        const sessionFacts = sessionFactParts({...session, kind: session.kind === 'live' || session.kind === 'channel' ? 'channel' : session.kind === 'audio' ? 'audio' : 'vod', location: session.location === 'other' ? 'unknown' : session.location}, runtime.text);
        const location = sessionFacts.find(fact => fact.id === 'location'), delivery = sessionFacts.find(fact => fact.id === 'delivery');
        // TV identity leads with the username, then a profile that differs from it, then the device, where and how it plays.
        const people = [session.user, session.profile !== session.user ? session.profile : undefined, session.device, location?.text, delivery?.text].filter((value): value is string => !!value);
        return <TVDashboardSession key={session.id} focusId={session.id} testID={`dashboard-session-${session.id}`}
          onFocus={() => {const window = state.sessionWindow; if (window) {lease = controller.focusSession(session.id, window); if (index >= state.sessions.length - 2 && window.error === undefined) void controller.nextSessions(window);}}}
          onBlur={() => {if (state.sessionWindow && lease) controller.blurSession(session.id, lease, state.sessionWindow);}}
          accessibilityLabel={cardAccessibilityLabel(identity.title, sessionFacts, [], identity.secondary)} artwork={runtime.artwork(session.image)}
          title={identity.title} secondary={identity.secondary} people={people} fraction={playedFraction(session)}/>;
      })}
      {sessions && <>
        {sessions.refreshRequired && <StateView {...runtime.error({code: 'stale_continuation'}, () => {if (state.sessionWindow) void controller.restartSessions(state.sessionWindow);}, 'server-console')} testID="dashboard-sessions-refresh-required"/>}
        {sessions.error !== undefined && <StateView {...runtime.error(sessions.error, () => {if (state.sessionWindow) void controller.retrySessions(state.sessionWindow);}, 'server-console')} testID="dashboard-sessions-page-error"/>}
        {sessions.chunks[0]?.cursor && <Button focusId="dashboard-sessions-previous" testID="dashboard-sessions-previous" onPress={() => {if (state.sessionWindow) void controller.previousSessions(state.sessionWindow);}}>{t('web.pager.previous')}</Button>}
        {(sessions.nextCursor || sessions.pending) && <TVDashboardMore key="dashboard-sessions-more" focusId="dashboard-sessions-more" testID="dashboard-sessions-more" pending={sessions.pending}
          label={t(sessions.error === undefined && !sessions.refreshRequired ? 'next.settings.loadMore' : 'next.states.tryAgain')}
          onFocus={() => {if (state.sessionWindow) controller.focusSessionHost(state.sessionWindow);}}
          onPress={() => {if (state.sessionWindow) void (state.sessionWindow.refreshRequired ? controller.restartSessions(state.sessionWindow) : state.sessionWindow.error === undefined ? controller.nextSessions(state.sessionWindow) : controller.retrySessions(state.sessionWindow));}}/>}
      </>}
      {state.admission === 'owner' && <>
        <TVDashboardSectionLabel>{t('settings.server.alerts')}</TVDashboardSectionLabel>
        {state.alerts.map((alert, index) => <TVDashboardItem key={`${alert.code}:${index}`} focusId={`alert:${alert.code}:${index}`} testID={`dashboard-alert-${alert.code}`} label={dashboardAlertWords(alert, t).title}/>)}
        <TVDashboardSectionLabel>{t('next.shell.backgroundTasks')}</TVDashboardSectionLabel>
        {state.jobs.map((job, index) => {
          let lease = controller.jobFocusToken(job.id);
          return <TVDashboardItem key={job.id} focusId={`job:${job.id}`} testID={`dashboard-job-${job.id}`} label={jobName(t, defaultI18n.has, job.kind)}
            onFocus={() => {const window = state.jobWindow; if (window) {lease = controller.focusJob(job.id, window); if (index >= state.jobs.length - 2 && window.error === undefined) void controller.nextJobs(window);}}}
            onBlur={() => {if (state.jobWindow && lease) controller.blurJob(job.id, lease, state.jobWindow);}}/>;
        })}
        {jobs && <>
          {jobs.refreshRequired && <StateView {...runtime.error({code: 'stale_continuation'}, () => {if (state.jobWindow) void controller.restartJobs(state.jobWindow);}, 'server-console')} testID="dashboard-jobs-refresh-required"/>}
          {jobs.error !== undefined && <StateView {...runtime.error(jobs.error, () => {if (state.jobWindow) void controller.retryJobs(state.jobWindow);}, 'server-console')} testID="dashboard-jobs-page-error"/>}
          {jobs.chunks[0]?.cursor && <Button focusId="dashboard-jobs-previous" testID="dashboard-jobs-previous" onPress={() => {if (state.jobWindow) void controller.previousJobs(state.jobWindow);}}>{t('web.pager.previous')}</Button>}
          {(jobs.nextCursor || jobs.pending) && <TVDashboardMore key="dashboard-jobs-more" focusId="dashboard-jobs-more" testID="dashboard-jobs-more" pending={jobs.pending}
            label={t(jobs.error === undefined && !jobs.refreshRequired ? 'next.settings.loadMore' : 'next.states.tryAgain')}
            onFocus={() => {if (state.jobWindow) controller.focusJobHost(state.jobWindow);}}
            onPress={() => {if (state.jobWindow) void (state.jobWindow.refreshRequired ? controller.restartJobs(state.jobWindow) : state.jobWindow.error === undefined ? controller.nextJobs(state.jobWindow) : controller.retryJobs(state.jobWindow));}}/>}
        </>}
        {disk && <TVDiskUsage title={t('settings.server.diskUsage')} rows={disk}/>}
      </>}
    </FocusRegion>
  </TVDashboardBody>;
}
