import React, {useEffect} from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {dashboardAlertWords} from '../../../../../../packages/client-core/src/admin/dashboard-alert-words';
import {sessionFactParts} from '../../../../../../packages/client-core/src/presentation/kit-model';
import {jobName} from '../../../../../../packages/client-core/src/server-admin/console-catalogue';
import {playedFraction} from '../../../../../../packages/client-core/src/server-admin/now-playing';
import {cardAccessibilityLabel} from '../../../kit/cards/model';
import {Button} from '../../../kit/controls';
import {SectionCard, StateRow} from '../../../kit/frames';
import {SettingsControlRows} from '../../../kit/settings/SettingsParts';
import {TVDashboardFacts, TVDashboardRow} from '../../../kit/settings/dashboard/TVRows';
import {SettingsRow} from '../../../kit/shell';
import {StateView} from '../../../kit/states';
import {nativeDashboardIdentity} from '../../../screens/settings-live/dashboard-presentation';
import {useTVDashboard} from '../../shared/settings/useTVDashboard';

export function ServerDashboard({controller, ports, runtime, column, rows}: {
  controller: Parameters<typeof useTVDashboard>[0]; ports: Parameters<typeof useTVDashboard>[1];
  runtime: Parameters<typeof useTVDashboard>[2]; column: string; rows: string;
}) {
  const {t} = runtime.text;
  const {state, owner, facts, measurements} = useTVDashboard(controller, ports, runtime);
  const {sessionWindow, jobWindow} = state;
  useEffect(() => {
    if (state.admission === 'denied') runtime.onDestination({kind: 'settings'});
  }, [runtime, state.admission]);
  if (state.admission === 'denied') return null;
  return <SettingsControlRows id={rows} entry={state.sessions[0]?.id} orientation="vertical" edges={{left: column}}>
    <TVDashboardFacts facts={facts}/>
    <SectionCard label={t('next.shell.nowPlaying')}>
      {state.error !== undefined && <StateView {...runtime.error(state.error, () => {void controller.refresh();}, 'server-console')}/>}
      {state.loading && !state.sessions.length && <StateRow kind="loading"/>}
      {!state.loading && state.error === undefined && !state.sessions.length && <StateView title={t('web.nowPlaying.empty')}/>}
      {state.sessions.map((session, index) => {
        let lease = controller.sessionFocusToken(session.id);
        const identity = nativeDashboardIdentity(session, runtime.text);
        const sessionFacts = sessionFactParts({...session, kind: session.kind === 'live' || session.kind === 'channel' ? 'channel'
          : session.kind === 'audio' ? 'audio' : 'vod', location: session.location === 'other' ? 'unknown' : session.location}, runtime.text);
        const location = sessionFacts.find(fact => fact.id === 'location')?.text;
        const delivery = sessionFacts.find(fact => fact.id === 'delivery')?.text;
        return <TVDashboardRow key={session.id} id={session.id} testID={`dashboard-session-${session.id}`} first={index === 0}
          title={identity.title} secondary={identity.secondary} facts={[session.user, session.profile !== session.user ? session.profile : undefined,
            session.device, location, delivery].filter((value): value is string => !!value)} progress={playedFraction(session)} image={runtime.artwork(session.image)}
          label={cardAccessibilityLabel(identity.title, sessionFacts, [], identity.secondary)}
          onFocus={() => {
            if (!sessionWindow) return;
            lease = controller.focusSession(session.id, sessionWindow);
            if (index >= state.sessions.length - 2 && sessionWindow.error === undefined) void controller.nextSessions(sessionWindow);
          }}
          onBlur={() => {if (sessionWindow && lease) controller.blurSession(session.id, lease, sessionWindow);}}/>;
      })}
      {sessionWindow && <>
        {sessionWindow.refreshRequired && <StateView {...runtime.error({code: 'stale_continuation'}, () => {void controller.restartSessions(sessionWindow);}, 'server-console')} testID="dashboard-sessions-refresh-required"/>}
        {sessionWindow.error !== undefined && <StateView {...runtime.error(sessionWindow.error, () => {void controller.retrySessions(sessionWindow);}, 'server-console')} testID="dashboard-sessions-page-error"/>}
        {sessionWindow.chunks[0]?.cursor && <Button focusId="dashboard-sessions-previous" testID="dashboard-sessions-previous" onPress={() => {void controller.previousSessions(sessionWindow);}}>{t('web.pager.previous')}</Button>}
        {(sessionWindow.nextCursor || sessionWindow.pending) && <Button focusId="dashboard-sessions-more" testID="dashboard-sessions-more" variant="glass"
          onFocus={() => controller.focusSessionHost(sessionWindow)} disabled={sessionWindow.pending} focusableWhenDisabled
          onPress={() => {void (sessionWindow.refreshRequired ? controller.restartSessions(sessionWindow) : sessionWindow.error === undefined ? controller.nextSessions(sessionWindow) : controller.retrySessions(sessionWindow));}}>
          {t(sessionWindow.error === undefined && !sessionWindow.refreshRequired ? 'next.settings.loadMore' : 'next.states.tryAgain')}
        </Button>}
      </>}
    </SectionCard>
    {owner && <>
      {(state.alerts.length > 0 || !state.loading && state.error === undefined) && <SectionCard label={t('settings.server.alerts')}>
        {!state.alerts.length && <StateRow kind="empty" icon="i-check" title={t('server.attention.none')}/>}
        {state.alerts.map((alert, index) => {
          const words = dashboardAlertWords(alert, t);
          return <TVDashboardRow key={`${alert.code}:${index}`} id={`alert:${alert.code}:${index}`} testID={`dashboard-alert-${alert.code}`}
            title={words.title} secondary={words.detail ? [words.detail] : undefined} icon="i-warn" first={index === 0}/>;
        })}
      </SectionCard>}
      {(state.jobs.length > 0 || jobWindow || !state.loading && state.error === undefined) && <SectionCard label={t('next.shell.backgroundTasks')}>
        {!state.jobs.length && !jobWindow && <StateRow kind="empty" icon="i-clock" title={t('web.activity.noTasks')}/>}
        {state.jobs.map((job, index) => {
          let lease = controller.jobFocusToken(job.id);
          return <TVDashboardRow key={job.id} id={`job:${job.id}`} testID={`dashboard-job-${job.id}`} title={jobName(t, defaultI18n.has, job.kind)} first={index === 0}
            onFocus={() => {
              if (!jobWindow) return;
              lease = controller.focusJob(job.id, jobWindow);
              if (index >= state.jobs.length - 2 && jobWindow.error === undefined) void controller.nextJobs(jobWindow);
            }}
            onBlur={() => {if (jobWindow && lease) controller.blurJob(job.id, lease, jobWindow);}}/>;
        })}
        {jobWindow && <>
          {jobWindow.refreshRequired && <StateView {...runtime.error({code: 'stale_continuation'}, () => {void controller.restartJobs(jobWindow);}, 'server-console')} testID="dashboard-jobs-refresh-required"/>}
          {jobWindow.error !== undefined && <StateView {...runtime.error(jobWindow.error, () => {void controller.retryJobs(jobWindow);}, 'server-console')} testID="dashboard-jobs-page-error"/>}
          {jobWindow.chunks[0]?.cursor && <Button focusId="dashboard-jobs-previous" testID="dashboard-jobs-previous" onPress={() => {void controller.previousJobs(jobWindow);}}>{t('web.pager.previous')}</Button>}
          {(jobWindow.nextCursor || jobWindow.pending) && <Button focusId="dashboard-jobs-more" testID="dashboard-jobs-more" variant="glass"
            onFocus={() => controller.focusJobHost(jobWindow)} disabled={jobWindow.pending} focusableWhenDisabled
            onPress={() => {void (jobWindow.refreshRequired ? controller.restartJobs(jobWindow) : jobWindow.error === undefined ? controller.nextJobs(jobWindow) : controller.retryJobs(jobWindow));}}>
            {t(jobWindow.error === undefined && !jobWindow.refreshRequired ? 'next.settings.loadMore' : 'next.states.tryAgain')}
          </Button>}
        </>}
      </SectionCard>}
      {measurements.length > 0 && <SectionCard label={t('settings.server.diskUsage')}>
        {measurements.map((measure, index) => <SettingsRow key={measure.id} testID={`dashboard-disk-${measure.id}`} readable
          first={index === 0} label={measure.label} value={measure.value}/>)}
      </SectionCard>}
    </>}
  </SettingsControlRows>;
}
