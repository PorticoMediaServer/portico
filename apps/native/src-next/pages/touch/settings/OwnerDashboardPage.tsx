import React, {useEffect, type ReactNode} from 'react';
import {dataFolderConfirmText} from '../../../../../../packages/client-core/src/admin/data-folder';
import {sessionViewer, STOP_MESSAGE_MAX} from '../../../../../../packages/client-core/src/server-admin/now-playing';
import {Button, FormError, TextField} from '../../../kit/controls';
import {SettingsPageFrame} from '../../../kit/frames';
import {BottomSheet, Menu} from '../../../kit/overlays';
import {OwnerDashboardContent} from '../../../kit/settings/dashboard/OwnerDashboardContent';
import {ownerIsNewServer, ownerPeopleLine, ownerUpdatedLabel} from '../../../screens/settings-live/owner-dashboard/model';
import {useOwnerDashboard} from '../../shared/settings/useOwnerDashboard';
import {useDashboardRead} from '../../shared/settings/useDashboardRead';

export function OwnerDashboardOpening({deniedFallback, onDenied, ...props}: Parameters<typeof useOwnerDashboard>[0] & {
  deniedFallback: ReactNode; onDenied?: () => void;
}) {
  const {controller, ports, runtime} = props;
  const {admission} = useDashboardRead(controller, ports);
  useEffect(() => {
    if (admission !== 'denied') return;
    if (onDenied) onDenied();
    else runtime.onDestination({kind: 'settings'});
  }, [admission, onDenied, runtime]);
  return admission === 'denied' ? <>{deniedFallback}</> : <OwnerDashboardPage {...props}/>;
}

export function OwnerDashboardPage({controller, ports, runtime, inline = false}: Parameters<typeof useOwnerDashboard>[0]) {
  const {
    t, owner, state, insets, health, telemetry, statistics, healthWindow, setHealthWindow, viewingPeriod, setViewingPeriod, now,
    expanded, setExpanded, compact, all, setAllRevision, entries, allEntries, totals, alerts, jobs, update, updates, busy, pull,
    actionError, setActionError, refresh, releaseNotes, confirmStop, confirmPermissions, streamMenu, setStreamMenu, jobMenu, setJobMenu, stopView, setStopView,
    message, setMessage, stopSession, fixView, setFixView, refusal, setRefusal, menuItems, jobItems,
  } = useOwnerDashboard({controller, ports, runtime, inline});
  return <>
    <SettingsPageFrame title={t('next.dashboard.title')} hideHeader panel={<OwnerDashboardContent t={t} role={owner ? 'owner' : 'administrator'} serverName={ports.serverName()} topInset={inline ? 0 : insets.top} inline={inline}
      health={{degraded: health.facts.degraded, status: owner ? health.status : '', line: health.line, ...(health.uptimeSeconds === undefined ? {} : {uptimeSeconds: health.uptimeSeconds})}}
      updated={ownerUpdatedLabel(telemetry.reading?.observedAt ?? (state.observedAt ? Date.parse(state.observedAt) : undefined), now, t)} loading={state.loading || busy} error={actionError ?? state.error}
      streams={entries} streamTotals={totals} expanded={expanded && compact} allStreams={allEntries} allError={all.error}
      onShowAll={() => setExpanded(true)} onShowFewer={() => setExpanded(false)} onRetryAll={() => setAllRevision(value => value + 1)}
      people={totals.streams ? ownerPeopleLine(totals, t) : t('next.shell.streams', {count: 0})} alerts={alerts} jobs={jobs}
      update={update} onCheckUpdate={() => {void updates?.check();}}
      onReleaseNotes={releaseNotes}
      telemetry={telemetry.reading} telemetryError={telemetry.error} healthWindow={healthWindow} healthNote={ownerIsNewServer(health.uptimeSeconds) ? t('next.dashboard.healthLater') : undefined}
      summary={statistics.summary} statisticsError={statistics.error} viewingPeriod={viewingPeriod}
      onBack={() => ports.context.navigation.back(controller.lifetime.authority)} refreshControl={pull} onRetry={refresh}
      onHealthWindow={setHealthWindow} onViewingPeriod={setViewingPeriod}
      onFixPermissions={view => {setRefusal(''); setFixView(view);}}
      onJobMenu={view => {setJobMenu(view); setActionError(undefined);}}
      onStop={view => {setMessage(''); setStopView(view);}} onStreamMenu={view => {setStreamMenu(view); setActionError(undefined);}}/>}/>
    <Menu open={Boolean(streamMenu)} onOpenChange={open => {if (!open) setStreamMenu(undefined);}} presentation="sheet" testID="owner-stream-menu"
      title={streamMenu?.title} description={streamMenu ? `${streamMenu.viewer.username || streamMenu.viewer.name} · ${streamMenu.viewer.device}` : undefined} items={menuItems}/>
    <Menu open={Boolean(jobMenu)} onOpenChange={open => {if (!open) setJobMenu(undefined);}} presentation="sheet" testID="owner-job-menu" title={jobMenu?.title} description={jobMenu?.subtitle ?? jobMenu?.status} items={jobItems}/>
    <BottomSheet open={Boolean(stopView)} onOpenChange={open => {if (!open && !busy) setStopView(undefined);}} testID="owner-stop-sheet" keyboardAvoiding
      title={stopSession ? t('web.nowPlaying.stopTitle', {title: stopSession.title || t('web.nowPlaying.untitled')}) : t('web.nowPlaying.stop')}
      description={stopSession ? t('web.nowPlaying.stopBody', {viewer: sessionViewer(stopSession, t)}) : undefined}
      footer={<><Button variant="glass" disabled={busy} onPress={() => setStopView(undefined)}>{t('action.cancel')}</Button><Button variant="danger" disabled={busy} loading={busy} onPress={confirmStop}>{t('web.nowPlaying.stopConfirm')}</Button></>}>
      <TextField testID="owner-stop-message" label={t('web.nowPlaying.messageLabel')} description={t('web.nowPlaying.messageHelp')} placeholder={t('web.nowPlaying.messagePlaceholder')} value={message} onChangeText={setMessage} maxLength={STOP_MESSAGE_MAX} multiline/>
    </BottomSheet>
    <BottomSheet open={Boolean(fixView)} onOpenChange={open => {if (!open && !busy) setFixView(undefined);}} testID="owner-fix-permissions-sheet"
      title={t('next.dashboard.fixPermissions')} description={fixView?.owner && fixView.path ? dataFolderConfirmText(fixView.owner, fixView.path) : undefined}
      footer={<><Button variant="glass" disabled={busy} onPress={() => setFixView(undefined)}>{t('action.cancel')}</Button><Button variant="primary" disabled={busy} loading={busy} onPress={confirmPermissions}>{t('next.dashboard.fixPermissions')}</Button></>}>
      {refusal ? <FormError placement="inline">{refusal}</FormError> : null}
    </BottomSheet>
  </>;
}
