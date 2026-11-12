import type {I18n} from '@i18n';
import type {ListedServerRow} from '../platform/portico-account-holder';

/** The account's signed list gives each server's role; it says nothing about whether a server is
 * up, so no row claims Online or a last-seen time. The labels grant no server authority. */
export function servicesServerDirectoryCopy(server: ListedServerRow, i18n: I18n) {
  const {t} = i18n;
  const badge = server.ownedByMe ? {label: t('web.hostedAccount.serverOwner'), tone: 'accent' as const}
    : server.role === 'admin' ? {label: t('web.access.roleAdmin'), tone: 'default' as const} : undefined;
  return {badge, presence: undefined as string | undefined, online: undefined as boolean | undefined};
}
