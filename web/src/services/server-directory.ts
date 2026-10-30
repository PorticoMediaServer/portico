import type {I18n} from '@i18n';
import type {WorkspaceServers} from './workspace-api';

/** Hosted directory.go emits active/quiet/dormant, and actual membership roles.
 * These drawn labels describe its facts; they grant no Server/leave authority. */
export function servicesServerDirectoryCopy(server: WorkspaceServers['items'][number], i18n: I18n) {
  const {t} = i18n;
  const badge = server.ownedByMe ? {label: t('web.hostedAccount.serverOwner'), tone: 'accent' as const}
    : server.role === 'admin' ? {label: t('web.access.roleAdmin'), tone: 'default' as const} : undefined;
  // Never an address, never a spinner: a pending secure connection is one plain line,
  // and when Hosted cannot reach the server, one next step.
  const presence = server.secureConnection === 'pending' ? t(server.status === 'active' ? 'web.hostedAccount.secureConnectionPending' : 'web.hostedAccount.secureConnectionOffline')
    : server.status === 'active' ? t('web.hostedAccount.serverOnline')
    : server.presence.lastSeenAt ? t('web.hostedAccount.serverLastSeen', {when: i18n.date(server.presence.lastSeenAt)}) : undefined;
  return {badge, presence, online: server.status === 'active'};
}
