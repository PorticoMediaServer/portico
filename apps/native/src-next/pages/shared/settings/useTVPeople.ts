import {useLayoutEffect, useRef, useState} from 'react';
import type {SettingsPersonRow} from '../../../../../../packages/client-core/src/settings-people';
import type {MessageId} from '../../../../../../packages/i18n/src';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeScreenLifetime} from '../../../screens/settings-live/lifetime';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import {PeoplePagesController, type PeoplePages, type PeopleSection} from '../../../screens/settings-live/server/people-pages';

export type TVPersonRow = Readonly<{id: string; title: string; meta: readonly string[]; status?: string}>;
export type TVPeopleGroup = Readonly<{id: keyof PeoplePages; title: MessageId; rows: readonly TVPersonRow[]; empty?: MessageId;
  pending: boolean; error?: unknown; retry: () => void}>;

const roleLabel = (role: SettingsPersonRow['role']): MessageId =>
  role === 'owner' ? 'next.tvPeople.owner' : role === 'admin' ? 'next.settings.people.adminRole' : 'next.settings.people.memberRole';

/** TV37: the web People page's groups, read only: accounts and waiting invitations, the server's own authorized view.
 * The rebuilt server keeps no server-wide list of devices (only each person's own), so that group is not shown.
 * Returns undefined while the first page loads. */
export function useTVPeople({ports, runtime, lifetime}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; lifetime: NativeScreenLifetime}) {
  const t = runtime.text.t;
  type Publication = {pages: PeoplePagesController; state: PeoplePages; generation: number; ports: NativeSettingsLivePorts; lifetime: NativeScreenLifetime};
  const [publication, setPublication] = useState<Publication>();
  const controller = useRef<PeoplePagesController | undefined>(undefined);
  const renderScope = useRef({ports, lifetime});
  renderScope.current = {ports, lifetime};
  useLayoutEffect(() => {
    const pages = new PeoplePagesController(ports.context, lifetime,
      state => setPublication({pages, state, generation: pages.currentGeneration(), ports, lifetime}));
    controller.current = pages;
    setPublication(undefined);
    pages.start();
    const retire = () => {pages.dispose(); setPublication(undefined);};
    lifetime.signal.addEventListener('abort', retire, {once: true});
    return () => {
      pages.dispose();
      if (controller.current === pages) controller.current = undefined;
      lifetime.signal.removeEventListener('abort', retire);
    };
  }, [ports, lifetime]);
  const time = (at: Date) => new Intl.DateTimeFormat(runtime.text.locale ?? 'en-US', {hour: 'numeric', minute: '2-digit'}).format(at);
  const dayStart = (date: Date) => new Date(date.getFullYear(), date.getMonth(), date.getDate()).getTime();
  const when = (iso: string | undefined): string | undefined => {
    if (!iso) return undefined;
    const at = new Date(iso);
    if (!Number.isFinite(at.getTime())) return undefined;
    const days = Math.max(0, Math.round((dayStart(new Date()) - dayStart(at)) / 86400000));
    if (days === 0) return t('next.tvPeople.todayAt', {time: time(at)});
    if (days === 1) return t('next.tvPeople.yesterdayAt', {time: time(at)});
    if (days < 7) return t('next.tvPeople.daysAgo', {count: days});
    if (days < 35) return t('next.tvPeople.weeksAgo', {count: Math.floor(days / 7)});
    return t('next.tvPeople.monthsAgo', {count: Math.floor(days / 30)});
  };
  const expiry = (iso: string): string | undefined => {
    const at = new Date(iso);
    if (!Number.isFinite(at.getTime())) return undefined;
    const days = Math.ceil((dayStart(at) - dayStart(new Date())) / 86400000);
    if (days <= 0) return t('next.tvPeople.expiresToday', {time: time(at)});
    return t('next.tvPeople.expiresIn', {count: days});
  };
  if (!publication || controller.current !== publication.pages || publication.ports !== ports || publication.lifetime !== lifetime) return undefined;
  const {pages, state, generation} = publication;
  const load = (section: keyof PeoplePages) => {
    if (controller.current === pages && renderScope.current.ports === ports && renderScope.current.lifetime === lifetime) void pages.load(section, '', generation);
  };
  const tail = (section: keyof PeoplePages, page: PeopleSection<unknown>) => ({pending: page.pending, error: page.error, retry: () => load(section)});
  const accounts = state.members.items.map(account => {
    const live = account.liveState === 'watching' || account.liveState === 'listening' ? t('next.shell.nowPlaying') : undefined;
    const last = when(account.lastActiveAt);
    const status = account.disabled ? t('next.tvPeople.turnedOff') : live ?? (last ? t('next.tvPeople.lastActive', {when: last}) : undefined);
    return {id: `account:${account.accountId}`, title: account.username, status,
      meta: [t(roleLabel(account.role)), t('next.tvPeople.profiles', {count: account.profileNames.length})]};
  });
  const invitations = state.invitations.items.map(invitation => {
    const expires = expiry(invitation.expiresAt);
    return {id: `invitation:${invitation.id}`, title: t(invitation.role === 'admin' ? 'next.tvPeople.invitationForAdmin' : 'next.tvPeople.invitationForMember'),
      meta: expires ? [expires] : [], status: t('next.tvPeople.notUsed')};
  });
  const groups: readonly TVPeopleGroup[] = [
    {id: 'members', title: 'next.tvPeople.accounts', rows: accounts, empty: state.members.loaded ? 'next.tvPeople.noAccounts' : undefined,
      ...tail('members', state.members)},
    {id: 'invitations', title: 'next.tvPeople.invitations', rows: invitations, empty: state.invitations.loaded ? 'next.tvPeople.noInvitations' : undefined,
      ...tail('invitations', state.invitations)},
  ];
  return groups;
}
