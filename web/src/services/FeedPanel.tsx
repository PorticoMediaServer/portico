import type {AccountNotification} from '@core/account-notifications.ts';
import {HostedError} from '../kit/accounts/ServicesAuth';
import {useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {ServicesEmpty, ServicesFeedRow, ServicesGroup, ServicesWorkspaceRow} from '../kit/accounts/ServicesWorkspace';
import {Button, Switch} from '../kit/controls';
import {ServicesFeed} from './feed';
import {serviceNoticeCopy} from './notification-copy';
import {servicesI18n} from './i18n';
import {servicesNoticeTime} from './time';
import styles from './services.module.css';

type Commit = {active: boolean; running: boolean};
type FeedTicket = {feed: ServicesFeed; retired: boolean; commit?: Commit};
type DownloadAttempt = Readonly<{ticket: FeedTicket; commit: Commit; notice: AccountNotification; noticeId: string; exportId: string}>;

export function FeedPanel({feed, onReviewDevices}: {feed: ServicesFeed; onReviewDevices: () => void}) {
  const i18n = useMemo(servicesI18n, []), {t} = i18n;
  const state = useSyncExternalStore(feed.subscribe, feed.getSnapshot);
  const ticket = useMemo<FeedTicket>(() => ({feed, retired: false}), [feed]);
  const renderedTicket = useRef(ticket);
  // Retained A handlers cannot borrow B's committed lifetime, even before effects run.
  if (renderedTicket.current !== ticket) {
    renderedTicket.current.retired = true;
    renderedTicket.current = ticket;
  }
  useLayoutEffect(() => {
    if (renderedTicket.current !== ticket) return;
    const commit = {active: true, running: false}; ticket.commit = commit; ticket.retired = false;
    return () => {commit.active = false;if (ticket.commit === commit) ticket.commit = undefined;};
  }, [ticket]);
  const owns = (attempt: DownloadAttempt) => renderedTicket.current === attempt.ticket && !attempt.ticket.retired
    && attempt.ticket.commit === attempt.commit && attempt.commit.active
    && attempt.ticket.feed.getSnapshot().items.some(item => item === attempt.notice && item.kind === 'account_export_ready'
      && item.id === attempt.noticeId && item.detail.exportId === attempt.exportId);
  const [downloadState, setDownloadState] = useState<DownloadAttempt>();
  const [failure, setFailure] = useState<Readonly<{attempt: DownloadAttempt; error: unknown}>>();
  const downloading = downloadState && owns(downloadState) ? downloadState.exportId : undefined;
  const error = failure && owns(failure.attempt) ? failure.error : undefined;
  const download = async (offer: Readonly<{notice: AccountNotification; noticeId: string; exportId: string}>) => {
    // `ticket` and `notice` belong to this render, never selected through current refs.
    const commit = ticket.commit, {exportId} = offer;
    if (!commit || commit.running || !exportId) return;
    const attempt = {ticket, commit, ...offer};
    if (!owns(attempt)) return;
    commit.running = true; setDownloadState(attempt); setFailure(undefined);
    try {
      if (!owns(attempt)) return;
      const data = await ticket.feed.client.downloadExport(exportId);
      if (!owns(attempt)) return;
      const url = URL.createObjectURL(new Blob([JSON.stringify(data, null, 2)], {type: 'application/json'}));
      try {
        if (!owns(attempt)) return;
        const link = document.createElement('a'); link.href = url; link.download = 'portico-account.json'; link.click();
      } finally {setTimeout(() => URL.revokeObjectURL(url), 1000);}
      if (owns(attempt)) await ticket.feed.markRead([attempt.noticeId]);
    } catch (value) {if (owns(attempt)) setFailure({attempt, error: value});}
    finally {
      commit.running = false;
      if (owns(attempt)) setDownloadState(undefined);
    }
  };
  return <>
    {state.error ? <><HostedError error={state.error} /><Button disabled={state.busy} onClick={() => void feed.refresh()}>{t('action.tryAgain')}</Button></> : null}{error ? <HostedError error={error} /> : null}
    {state.loading && !state.error && <p role="status">{t('status.loading')}</p>}
    {!state.loading && !state.error && !state.items.length ? <ServicesGroup><ServicesEmpty title={t('services.workspace.emptyNotifications')} description={t('services.workspace.emptyNotificationsBody')} /></ServicesGroup> : null}
    {state.items.length ? <ServicesGroup>{state.items.map(item => {
      const copy = serviceNoticeCopy(item, i18n);
      const offer = item.kind === 'account_export_ready' && item.detail.exportId ? Object.freeze({notice: item, noticeId: item.id, exportId: item.detail.exportId}) : undefined;
      const actions = item.kind === 'new_device' ? <Button size="small" disabled={state.busy} onClick={() => { if (!item.read) void feed.markRead([item.id]); onReviewDevices(); }}>{t('services.feed.reviewDevices')}</Button>
        : offer ? <Button size="small" variant="primary" icon="i-download" iconSize={16} loading={downloading === item.detail.exportId} disabled={Boolean(downloading)} onClick={() => void download(offer)}>{t('action.download')}</Button> : undefined;
      return <ServicesFeedRow key={item.id} {...copy} unread={!item.read} when={servicesNoticeTime(i18n, item.occurredAt)} actions={actions} />;
    })}</ServicesGroup> : null}
    {state.cursor && <div className={styles.pager}><Button size="small" disabled={state.busy} onClick={() => void feed.older()}>{t('web.account.notif.older')}</Button></div>}
    <ServicesGroup title={t('web.account.notif.prefsTitle')} description={t('web.account.notif.prefsHelp')}>
      <ServicesWorkspaceRow title={t('web.account.notif.prefServerOffline')}><Switch checked={state.preferences?.serverOffline ?? false} disabled={!state.preferences || state.busy} aria-label={t('web.account.notif.prefServerOffline')} onCheckedChange={checked => void feed.savePreferences(checked)} /></ServicesWorkspaceRow>
    </ServicesGroup>
  </>;
}
