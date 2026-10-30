import React, {useEffect, useRef, useState} from 'react';
import {Text, View} from 'react-native';
import type {RemoteSource, RemoteSourceStatus} from '../../../../../../packages/client-core/src/remote-sources';
import {sourceStateText} from '../../../../../../packages/client-core/src/server-admin/storage-words';
import {defaultI18n, type MessageId} from '../../../../../../packages/i18n/src';
import {Button} from '../../../kit/controls/Button';
import {useTheme} from '../../../kit/theme';
import type {NativeRemoteSourcesController} from './controller';
import {Problem, remoteSourceError, useAttempt} from './RemoteSourceForms';

type ContactCode = NonNullable<RemoteSourceStatus['lastErrorCode']>;
type HealthCode = NonNullable<RemoteSourceStatus['health']>;

/** The server's closed health words; a code outside this map is not drawn. */
const HEALTH_WORDS: Readonly<Record<HealthCode, MessageId>> = {
  ready: 'next.remoteSources.healthReady', healthy: 'next.remoteSources.healthHealthy', degraded: 'next.remoteSources.healthDegraded',
  inventory_running: 'next.remoteSources.healthInventoryRunning', cancelled: 'next.remoteSources.healthCancelled',
  offline: 'next.remoteSources.healthOffline', root_changed: 'next.remoteSources.healthRootChanged', removing: 'next.remoteSources.healthRemoving',
};

/** The four real contact failures and their one-sentence explanation. */
const CONTACT_NOTICES: Readonly<Record<ContactCode, Readonly<{label: MessageId; problem: MessageId}>>> = {
  offline: {label: 'next.remoteSources.contactOffline', problem: 'next.remoteSources.problem.offline'},
  credentials_required: {label: 'next.remoteSources.contactCredentials', problem: 'next.remoteSources.problem.credentials_required'},
  rate_limited: {label: 'next.remoteSources.contactRateLimited', problem: 'next.remoteSources.problem.rate_limited'},
  quota_exceeded: {label: 'next.remoteSources.contactQuota', problem: 'next.remoteSources.problem.quota_exceeded'},
};

/** One explicit status read; no polling or inferred provider wait deadline. */
export function SourceStatusPanel({controller, source, isCurrent, t}: {
  controller: NativeRemoteSourcesController; source: RemoteSource; isCurrent: () => boolean; t: typeof defaultI18n.t;
}) {
  const theme = useTheme(), current = useAttempt(isCurrent), flight = useRef(false);
  const [status, setStatus] = useState<RemoteSourceStatus>(), [busy, setBusy] = useState(false), [error, setError] = useState<string>();
  const load = async () => {
    if (flight.current || !current()) return;
    flight.current = true; setBusy(true); setError(undefined);
    try {const next = await controller.sourceStatus(source, current); if (current()) setStatus(next);}
    catch (problem) {if (current()) {setStatus(undefined); setError(remoteSourceError(t, problem));}}
    finally {flight.current = false; if (current()) setBusy(false);}
  };
  useEffect(() => {void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  const ago = (value: string | undefined) => value ? defaultI18n.relativeTime(value) || undefined : undefined;
  // A missing count is unknown, never zero: only real numbers make a live claim.
  const playback = typeof status?.playbackReads === 'number' ? status.playbackReads : undefined;
  const analysis = typeof status?.analysisReads === 'number' ? status.analysisReads : undefined;
  const connections = typeof status?.connectionsInUse === 'number' ? status.connectionsInUse : undefined;
  const live = playback === undefined
    ? connections !== undefined && connections > 0 ? t('next.remoteSources.connectionsNow', {connections}) : undefined
    : playback > 0 && connections !== undefined ? t('next.remoteSources.readingNow', {watching: playback, connections})
    : playback === 0 && analysis === 0 && connections === 0 ? t('next.remoteSources.nothingReading')
    : connections !== undefined && connections > 0 ? t('next.remoteSources.connectionsNow', {connections}) : undefined;
  const checked = ago(status?.lastCheckedAt);
  const health = status?.health ? HEALTH_WORDS[status.health] : undefined;
  const contact = status?.lastErrorCode ? CONTACT_NOTICES[status.lastErrorCode] : undefined;
  const failedAt = ago(status?.lastErrorAt), connectedAt = ago(status?.lastConnectedAt);
  return <View style={{gap: 10}}>
    <Text accessibilityRole="header" style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{t('next.remoteSources.status')}</Text>
    {status && <>
      <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{sourceStateText(t, id => defaultI18n.has(id), {...source, state: status.state, errorCode: status.errorCode})}</Text>
      {live !== undefined && <>
        <Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{t('next.remoteSources.rightNow')}</Text>
        <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{live}</Text>
      </>}
      {health && <>
        <Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{t('next.remoteSources.health')}</Text>
        <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t(health)}</Text>
      </>}
      {(!!checked || status.lastCheckedAt === '') && <>
        <Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{t('next.remoteSources.lastChecked')}</Text>
        <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{checked
          ? t('next.remoteSources.checkedAt', {when: checked}) : t('next.remoteSources.neverChecked')}</Text>
      </>}
      {contact && <>
        <Text accessibilityRole="header" style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{t(contact.label)}</Text>
        <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t(contact.problem)}</Text>
        {failedAt && <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('next.remoteSources.lastError')}: {failedAt}</Text>}
        {connectedAt && <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('next.remoteSources.lastConnected')}: {connectedAt}</Text>}
      </>}
      <Text accessibilityRole="header" style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{t('next.remoteSources.usedBy')}</Text>
      {!status.libraries.length && !status.librariesTruncated && <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('next.remoteSources.noLibraryUses')}</Text>}
      {status.libraries.map(library => <View key={library.libraryId} style={{gap: 4}}>
        <Text style={[theme.type.body, {color: theme.colors.text}]}>{library.name}</Text>
        <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('next.remoteSources.foldersInLibrary', {count: library.folders})}</Text>
        {!!library.lastCompleteAt && !!ago(library.lastCompleteAt) && <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>
          {t('next.remoteSources.libraryComplete', {when: ago(library.lastCompleteAt)})}
        </Text>}
      </View>)}
    </>}
    <Problem message={error}/>
    <Button variant="text" disabled={busy} onPress={() => {void load();}}>{t(busy ? 'next.remoteSources.loading' : 'next.remoteSources.refresh')}</Button>
  </View>;
}
