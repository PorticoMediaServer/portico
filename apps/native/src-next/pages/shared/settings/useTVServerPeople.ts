import {useLayoutEffect, useRef, useState} from 'react';
import type {SettingsPersonRow} from '../../../../../../packages/client-core/src/settings-people';
import type {MessageId} from '../../../../../../packages/i18n/src';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeScreenLifetime} from '../../../screens/settings-live/lifetime';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import {PeoplePagesController, type PeoplePages} from '../../../screens/settings-live/server/people-pages';

export type TVServerPeopleProps = Readonly<{ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; lifetime: NativeScreenLifetime}>;

const roleLabel = (role: SettingsPersonRow['role']): MessageId =>
  role === 'owner' ? 'next.tvPeople.owner' : role === 'admin' ? 'next.settings.people.adminRole' : 'next.settings.people.memberRole';

export function useTVServerPeople({ports, runtime, lifetime}: TVServerPeopleProps) {
  const {t} = runtime.text;
  const [publication, setPublication] = useState<{pages: PeoplePagesController; state: PeoplePages; generation: number; ports: NativeSettingsLivePorts; lifetime: NativeScreenLifetime}>();
  const controller = useRef<PeoplePagesController | undefined>(undefined);
  const renderScope = useRef({ports, lifetime});
  renderScope.current = {ports, lifetime};
  useLayoutEffect(() => {
    const pages = new PeoplePagesController(ports.context, lifetime, state => setPublication({pages, state, generation: pages.currentGeneration(), ports, lifetime}));
    controller.current = pages;
    setPublication(undefined);
    pages.start();
    const retire = () => {pages.dispose(); setPublication(undefined);};
    lifetime.signal.addEventListener('abort', retire, {once: true});
    return () => {pages.dispose(); if (controller.current === pages) controller.current = undefined; lifetime.signal.removeEventListener('abort', retire);};
  }, [ports, lifetime]);

  const when = (iso: string | undefined): string | undefined => {
    if (!iso) return undefined;
    const at = new Date(iso);
    if (!Number.isFinite(at.getTime())) return undefined;
    const start = (date: Date) => new Date(date.getFullYear(), date.getMonth(), date.getDate()).getTime();
    const days = Math.max(0, Math.round((start(new Date()) - start(at)) / 86400000));
    const time = new Intl.DateTimeFormat(runtime.text.locale ?? 'en-US', {hour: 'numeric', minute: '2-digit'}).format(at);
    if (days === 0) return t('next.tvPeople.todayAt', {time});
    if (days === 1) return t('next.tvPeople.yesterdayAt', {time});
    if (days < 7) return t('next.tvPeople.daysAgo', {count: days});
    if (days < 35) return t('next.tvPeople.weeksAgo', {count: Math.floor(days / 7)});
    return t('next.tvPeople.monthsAgo', {count: Math.floor(days / 30)});
  };
  const expiry = (iso: string): string | undefined => {
    const at = new Date(iso);
    if (!Number.isFinite(at.getTime())) return undefined;
    const start = (date: Date) => new Date(date.getFullYear(), date.getMonth(), date.getDate()).getTime();
    const time = new Intl.DateTimeFormat(runtime.text.locale ?? 'en-US', {hour: 'numeric', minute: '2-digit'}).format(at);
    const days = Math.ceil((start(at) - start(new Date())) / 86400000);
    if (days <= 0) return t('next.tvPeople.expiresToday', {time});
    return t('next.tvPeople.expiresIn', {count: days});
  };
  if (!publication || controller.current !== publication.pages || publication.ports !== ports || publication.lifetime !== lifetime) return;
  const {pages, state, generation} = publication;
  const retry = (section: keyof PeoplePages) => {
    if (controller.current === pages && renderScope.current.ports === ports && renderScope.current.lifetime === lifetime) {
      void pages.load(section, '', generation);
    }
  };
  return {
    groups: [
      {
        id: 'members',
        label: t('next.tvPeople.accounts'),
        empty: t('next.tvPeople.noAccounts'),
        icon: 'i-person',
        rows: state.members.items.map(account => {
          const facts = [t(roleLabel(account.role)), t('next.tvPeople.profiles', {count: account.profileNames.length})];
          const live = account.liveState === 'watching' || account.liveState === 'listening' ? t('next.shell.nowPlaying') : undefined;
          const last = when(account.lastActiveAt);
          const status = account.disabled ? t('next.tvPeople.turnedOff') : live ?? (last ? t('next.tvPeople.lastActive', {when: last}) : undefined);
          return {id: `account:${account.accountId}`, label: account.username, facts, status};
        }),
      },
      {
        id: 'invitations',
        label: t('next.tvPeople.invitations'),
        empty: t('next.tvPeople.noInvitations'),
        icon: 'i-link',
        rows: state.invitations.items.map(invitation => {
          const label = t(invitation.role === 'admin' ? 'next.tvPeople.invitationForAdmin' : 'next.tvPeople.invitationForMember');
          const expires = expiry(invitation.expiresAt);
          return {id: `invitation:${invitation.id}`, label, facts: expires ? [expires] : [], status: t('next.tvPeople.notUsed')};
        }),
      },
    ] as const,
    state,
    retry,
  };
}
