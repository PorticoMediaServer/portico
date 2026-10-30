import React, {useLayoutEffect, useRef, useState} from 'react';
import {Text, View} from 'react-native';
import {
  type AccessMember,
} from '../../../../../../packages/client-core/src/server-administration';
import type {MessageId} from '../../../../../../packages/i18n/src';
import {Focusable} from '../../../kit/controls';
import {shellType} from '../../../kit/shell/visual';
import {StateView} from '../../../kit/states';
import {useTheme} from '../../../kit/theme';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeScreenLifetime} from '../lifetime';
import type {NativeSettingsLivePorts} from '../ports';
import {PeoplePagesController, type PeoplePages, type PeopleSection} from './people-pages';

const roleLabel = (role: AccessMember['role']): MessageId =>
  role === 'owner' ? 'next.tvPeople.owner' : role === 'admin' ? 'next.settings.people.adminRole' : 'next.settings.people.memberRole';

/** TV37: the web People page's three groups, read only. Accounts, invitations and
 * devices are the server's own authorized view; a device's session count is the
 * producer's optional fact and is omitted when unknown. */
export function TVPeoplePanel({ports, runtime, lifetime}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; lifetime: NativeScreenLifetime}) {
  const theme = useTheme(), t = runtime.text.t;
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
  const row = (id: string, title: string, meta: string[], status?: string, onFocus?: () => void) => <Focusable key={id} onFocus={onFocus} focusId={`tv-people:${id}`} focusKind="row" accessibilityRole="text" accessibilityLabel={[title, ...meta, status].filter(Boolean).join('. ')}
    style={{flexDirection: 'row', gap: 20, alignItems: 'center', padding: 22, marginBottom: 8, borderRadius: 16, backgroundColor: theme.colors.raised}}>
    <View style={{width: 44, height: 44, borderRadius: 22, alignItems: 'center', justifyContent: 'center', backgroundColor: `${theme.colors.accent}33`}}><Text style={[shellType(22, 700), {color: theme.colors.accentStrong}]}>{title.slice(0, 1).toUpperCase()}</Text></View>
    <View style={{flex: 1, gap: 5}}><Text style={[shellType(26, 700), {color: theme.colors.text}]}>{title}</Text>
      <Text style={[shellType(20), {color: theme.colors.textSecondary}]}>{meta.filter(Boolean).join(' · ')}</Text></View>
    {status && <Text style={[shellType(20, 600), {color: theme.colors.textSecondary, maxWidth: '38%'}]}>{status}</Text>}
  </Focusable>;
  const group = (id: string, title: MessageId, children: React.ReactNode) => <View key={id} style={{marginBottom: 30}}>
    <Text accessibilityRole="header" style={[shellType(24, 700), {color: theme.colors.textSecondary, marginBottom: 12}]}>{t(title)}</Text>
    {children}
  </View>;

  if (!publication || controller.current !== publication.pages || publication.ports !== ports || publication.lifetime !== lifetime) return <StateView title={t('status.loading')}/>;
  const {pages, state, generation} = publication;
  const load = (section: keyof PeoplePages) => {if (controller.current === pages && renderScope.current.ports === ports && renderScope.current.lifetime === lifetime) void pages.load(section, state[section].cursor, generation);};
  const sectionTail = (section: keyof PeoplePages, page: PeopleSection<unknown>) => <>
    {page.pending && <StateView title={t('status.loading')}/>}
    {page.error !== undefined && <StateView {...runtime.error(page.error, () => load(section))} testID={`tv-people-${section}-error`}/>}
    {page.loaded && !!page.cursor && <Focusable key={`tv-people-${section}-more`} focusId={`tv-people-${section}-more`} testID={`tv-people-${section}-more`} disabled={page.pending} focusableWhenDisabled onPress={() => load(section)}><Text style={{color: theme.colors.text}}>{t(page.error !== undefined ? 'next.states.tryAgain' : 'next.settings.loadMore')}</Text></Focusable>}
  </>;
  const tail = (section: keyof PeoplePages, index: number, length: number) => index >= length - 2 && state[section].error === undefined ? () => load(section) : undefined;
  return <View>
    {group('accounts', 'next.tvPeople.accounts', <>{state.members.items.length ? state.members.items.map((account, index) => {
      const name = account.username;
      const meta = [t(roleLabel(account.role)), t(account.hostedAccountId ? 'next.tvPeople.portico' : 'next.tvPeople.local')];
      if (account.profileNames !== undefined) meta.push(t('next.tvPeople.profiles', {count: account.profileNames.length}));
      const live = account.liveState === 'watching' || account.liveState === 'listening' ? t('next.shell.nowPlaying') : undefined;
      const last = when(account.lastActiveAt);
      const status = account.disabled ? t('next.tvPeople.turnedOff') : live ?? (last ? t('next.tvPeople.lastActive', {when: last}) : undefined);
      return row(`account:${account.accountId}`, name, meta, status, tail('members', index, state.members.items.length));
    }) : state.members.loaded && <StateView title={t('next.tvPeople.noAccounts')}/>} {sectionTail('members', state.members)}</>)}
    {group('invitations', 'next.tvPeople.invitations', <>{state.invitations.items.length ? state.invitations.items.map((invitation, index) => {
      const title = t(invitation.role === 'admin' ? 'next.tvPeople.invitationForAdmin' : 'next.tvPeople.invitationForMember');
      const expires = expiry(invitation.expiresAt);
      return row(`invitation:${invitation.id}`, title, expires ? [expires] : [], t('next.tvPeople.notUsed'), tail('invitations', index, state.invitations.items.length));
    }) : state.invitations.loaded && <StateView title={t('next.tvPeople.noInvitations')}/>} {sectionTail('invitations', state.invitations)}</>)}
    {group('devices', 'next.tvPeople.devices', <>{state.devices.items.length ? state.devices.items.map((device, index) => {
      const meta = [device.platform, when(device.firstSeenAt) ? t('next.tvPeople.firstSeen', {when: when(device.firstSeenAt)}) : ''].filter(Boolean);
      const sessions = device.sessionCount === undefined ? undefined : t('next.tvPeople.sessions', {count: device.sessionCount});
      return row(`device:${device.id}`, device.name, meta, sessions, tail('devices', index, state.devices.items.length));
    }) : state.devices.loaded && <StateView title={t('next.tvPeople.noDevices')}/>} {sectionTail('devices', state.devices)}</>)}
  </View>;
}
