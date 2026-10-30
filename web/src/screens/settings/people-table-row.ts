import {defaultI18n} from '@i18n';
import type {SettingsPersonRow} from '@core/settings-people.ts';
import {managesTier, type AccessTier} from '@core/server-administration.ts';
import type {SettingsTableRow} from '../../kit/settings';

const t = defaultI18n.t;
/** Whole-set facts are already resolved by the producer. In particular, zero
 * raw streams in server mode is not an unlimited effective limit. */
export function peopleTableRow(row: SettingsPersonRow, actor: Readonly<{accountId: string; role: AccessTier}>): SettingsTableRow {
  return {id: row.accountId, label: row.username, selectable: row.accountId !== actor.accountId && managesTier(actor.role, row.role), cells: {
    person: {title: row.username, meta: row.email, avatar: Array.from(row.username)[0]?.toLocaleUpperCase(), avatarKey: row.username},
    role: {title: t(row.role === 'owner' ? 'next.shell.owner' : row.role === 'admin' ? 'next.shell.administrator' : 'next.settings.member'), tone: row.role === 'owner' ? 'ok' : row.role === 'admin' ? 'accent' : 'quiet'},
    access: {title: row.libraryNames.length ? defaultI18n.list(row.libraryNames) : t('next.settings.allLibraries'), meta: row.effectiveMaxStreams === null ? t('next.settings.unlimitedStreams') : t('next.settings.streams', {count: row.effectiveMaxStreams})},
    profiles: defaultI18n.list(row.profileNames),
    active: {title: row.liveState === 'watching' ? t('next.settings.watchingNow') : row.liveState === 'listening' ? t('next.settings.listeningNow') : row.status !== 'active' ? t(`next.settings.personStatus.${row.status}` as 'next.settings.personStatus.off') : row.lastActiveAt ? new Intl.DateTimeFormat(undefined, {dateStyle: 'medium'}).format(new Date(row.lastActiveAt)) : ''},
  }};
}
