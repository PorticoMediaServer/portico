import {useLayoutEffect, useMemo, useState, useSyncExternalStore} from 'react';
import type {SettingsPeopleQuery, SettingsPersonRow} from '../../../../../../packages/client-core/src/settings-people';
import type {ServerInvitation} from '../../../../../../packages/client-core/src/direct-account';
import type {AccessDevice, AccessMember, MemberDeletionPreview} from '../../../../../../packages/client-core/src/server-administration';
import {roleLabel as accessRoleLabel} from '../../../../../../packages/client-core/src/server-admin/people-words';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {NativePeopleSource} from '../../../screens/settings-live/server/people-source';

type Props = {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime};
export type Confirmation = Readonly<{person: AccessMember; action: 'disable' | 'enable' | 'role' | 'devices'; role?: 'admin' | 'member'; device?: AccessDevice} | {person: AccessMember; action: 'remove'; preview: MemberDeletionPreview} | {invitation: ServerInvitation; action: 'revoke'} | {row: SettingsPersonRow; action: 'letBackIn'}>;
// List contracts expose strings; the shared parser admits these three roles.
export const roleLabel = (t: NativeShellRuntime['text']['t'], role: string) => accessRoleLabel(t, role === 'owner' || role === 'admin' ? role : 'member');
// Invitation list rows carry the machine state; show the same labels as the status filter.
export const invitationState = (t: NativeShellRuntime['text']['t'], state: string) => state === 'pending' ? t('web.access.trustWaiting') : state === 'accepted' ? t('web.access.trustAccepted') : state === 'expired' ? t('api.credential.state.expired') : state === 'revoked' ? t('api.credential.state.revoked') : state;

export function usePeopleSource(props: Props) {
  const [source, setSource] = useState<NativePeopleSource>();
  const {context, requestId} = props.ports, reportError = props.runtime.reportError;
  useLayoutEffect(() => {const next = new NativePeopleSource(context, requestId, reportError); setSource(next); void next.load(); return next.dispose;}, [context, requestId, reportError]);
  return source?.context === context ? source : undefined;
}

export function useServerPeople(source: NativePeopleSource) {
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  const [search, setSearch] = useState(''), [cursors, setCursors] = useState<readonly string[]>(['']);
  const [confirmation, setConfirmation] = useState<Confirmation>(), [typedName, setTypedName] = useState('');
  const view = useMemo(() => ({active: true, source, confirmation: undefined as Confirmation | undefined}), [source]);
  useLayoutEffect(() => {view.active = true; return () => {view.active = false; view.confirmation = undefined;};}, [view]);
  const current = () => view.active && source.current();
  const choose = (patch: Partial<SettingsPeopleQuery>) => {if (!current() || state.busy) return; setCursors(['']); source.select(state.tab, {...state.query, ...patch});};
  const ask = (value: Confirmation) => {if (!current() || source.getSnapshot().busy) return; view.confirmation = value; setConfirmation(value); setTypedName('');};
  const close = () => {if (source.getSnapshot().busy || view.confirmation !== confirmation) return; view.confirmation = undefined; setConfirmation(undefined); setTypedName('');};
  const held = confirmation && view.confirmation === confirmation && current() ? confirmation : undefined;
  const confirm = async () => {
    if (!held || view.confirmation !== held || !current() || source.getSnapshot().busy) return;
    if (held.action === 'remove' && typedName !== held.preview.username) return;
    view.confirmation = undefined; setConfirmation(undefined); setTypedName('');
    if (held.action === 'remove') await source.remove(held.person, held.preview);
    else if (held.action === 'revoke') await source.revoke(held.invitation);
    else if (held.action === 'letBackIn') await source.letBackIn(held.row);
    else if (held.action === 'devices') await source.signOutDevices(held.person, held.device);
    else if (held.action === 'role' && held.role) await source.role(held.person, held.role);
    else if (held.action === 'enable' || held.action === 'disable') await source.enabled(held.person, held.action === 'enable');
  };
  const loading = state.loading || state.busy;
  const page = state.tab === 'people' ? state.people : state.invitations;
  const rows: readonly (SettingsPersonRow | ServerInvitation)[] = state.tab === 'people' ? state.people?.items ?? [] : state.invitations?.items ?? [];
  const selectTab = (tab: string) => {
    if (current() && !loading && (tab === 'people' || tab === 'invitations')) {setCursors(['']); source.select(tab);}
  };
  const searchPeople = () => choose({search: search.trim()});
  const filterInvitation = (status: string) => {
    if (current() && ['', 'pending', 'accepted', 'expired', 'revoked'].includes(status)) {
      setCursors(['']); source.select('invitations', state.query, status as typeof state.invitationStatus);
    }
  };
  const refresh = () => {if (current()) void source.load(state.cursor);};
  const previousPage = () => {
    if (!current() || loading) return;
    const next = cursors.slice(0, -1);
    setCursors(next); void source.load(next[next.length - 1]);
  };
  const more = () => {
    if (!current() || loading || !page?.nextCursor) return;
    const currentPage = source.getSnapshot();
    if (page !== (currentPage.tab === 'people' ? currentPage.people : currentPage.invitations)) return;
    setCursors([...cursors, page.nextCursor]); void source.load(page.nextCursor);
  };
  return {
    state, loading, page, rows, choose, selectTab, search, setSearch, searchPeople, filterInvitation, refresh,
    ownerControls: source.ownerControls(), ask, close, confirm, held, typedName,
    changeTypedName: (name: string) => {if (current() && held && view.confirmation === held) setTypedName(name);},
    hasPrevious: cursors.length > 1, previousPage, more,
    openPerson: (item: SettingsPersonRow) => {if (current()) void source.open(item);},
    retryPerson: () => {if (state.selected && current()) void source.open(state.selected);},
    closePerson: () => {if (current() && source.getSnapshot().selected === state.selected) source.closePerson();},
    invite: (invitation?: ServerInvitation) => {if (current()) source.beginInvitation(invitation);},
  };
}
