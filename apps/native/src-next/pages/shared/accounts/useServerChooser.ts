import {useState} from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {nativeAccounts} from '../../../platform/accounts';

export type Snapshot = Awaited<ReturnType<typeof nativeAccounts.snapshot>>;
export type ServerRow = Snapshot['servers'][number];

/** What tells a saved server apart under its name: where this device reaches it when another saved server has
 * the same name, otherwise how it is signed in to. Phone and TV rows both draw this. */
export function serverRowDetail(server: ServerRow): string {return server.sharesName ? server.address : defaultI18n.t(server.labelKey);}

/** A saved server's actions: Forget always (local, always works), Sign out only while its sign-in is live. */
export function serverMenuItems(server: ServerRow, act: {signOut: (server: ServerRow) => void; forget: (server: ServerRow) => void}) {
  const t = defaultI18n.t;
  return [
    ...(server.signedIn ? [{id: 'sign-out', label: t('accounts.signOut.server', {server: server.name}), onSelect: () => act.signOut(server)}] : []),
    {id: 'forget', label: t('connection.identity.forget', {server: server.name}), onSelect: () => act.forget(server)},
  ];
}

/** Connect22's phone list. Session capture, switching and sign-out remain with ServersRoute. */
export type ServerChooserProps = {
  snapshot?: Snapshot; busy: boolean; error?: React.ReactNode; onBack?: () => void;
  onSelect: (server: ServerRow) => void; onSignOut: (server: ServerRow) => void; onForget: (server: ServerRow) => void;
  onRefresh: () => void; onAdd: () => void; footer?: React.ReactNode; certificates?: React.ReactNode;
};
export function useServerChooser(snapshot: Snapshot | undefined) {
  const t = defaultI18n.t;
  const [menu, setMenu] = useState<ServerRow>();
  const current = snapshot?.servers.find(server => server.id === snapshot.selectedServerId);
  const groups = [
    {id: 'portico', label: 'accounts.servers.title' as const, rows: snapshot?.servers.filter(server => server.id !== current?.id && server.signIn === 'portico') ?? []},
    {id: 'local', label: 'accounts.servers.local' as const, rows: snapshot?.servers.filter(server => server.id !== current?.id && server.signIn !== 'portico') ?? []},
  ];
  return {menu, setMenu, current, groups};
}
