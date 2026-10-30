import type {ShellIdentity} from './Shell';
import {useEffect, useLayoutEffect, useRef, useState} from 'react';
import type {AppSessionRecord} from '@core/app-sessions.ts';
import {defaultI18n} from '@i18n';
import {AuthError} from '../kit/accounts';
import {SwitchProfileServerDialog, type SwitchProfileServerEntry, type SwitchProfileServerProfile} from '../kit/shell/SwitchProfileServerDialog';
import type {ProfileIdentity} from '../kit/shell';
import {browserAppSessions, observeBrowserSessionChanges} from '../platform/server-connections';
import {browserServerDisplay} from '../platform/server-display';
import {browserAccount} from '../platform/account';
import {accountIdentity} from '../platform/account-work';
import {createAccountServerDirectory} from '../platform/account-server-directory';
import {createAccountServerAccess, type AccountServerAccess} from '../platform/account-server-access';
import type {HostedDirectoryServer} from '../platform/account-entry';
import {createBrowserAccountEntry} from '../platform/account-entry';
import {AccountSignInView} from '../screens/accounts/AccountSignInView';
import {AccountSignInForm} from '../screens/accounts/sign-in-form';

const t = defaultI18n.t;
export type HostedSwitchSelection = Readonly<{server: HostedDirectoryServer; access: AccountServerAccess; revision: number}>;
export type SwitchProfileServerOpening = Readonly<{
  current: () => boolean; serverId: string; serverName: string; portico: boolean;
  profiles: readonly ProfileIdentity[]; profileId: string;
  selectProfile: (profile: ProfileIdentity) => void;
  selectServer?: (id: string, expectedRevision: number) => Promise<void>;
  selectHosted: (selection: HostedSwitchSelection) => void;
  useLocal: () => void;
}>;
type Choice = Readonly<{view: SwitchProfileServerEntry; select?: () => Promise<void>}>;
type Published = Readonly<{record: AppSessionRecord; servers: readonly Choice[]; locals: readonly Choice[]}>;

/** One actual menu opening. No identity is looked up again by a rendered id.
 * Refresh replaces the whole immutable projection; callbacks retain its entries. */
export function SwitchProfileServerHost({opening, close, presentation}: {opening: SwitchProfileServerOpening; close: () => void; presentation?: Pick<ShellIdentity, 'serverName' | 'profiles'>}) {
  const [published, setPublished] = useState<Published>(), [busy, setBusy] = useState(false), [error, setError] = useState<unknown>();
  const holder = useRef<Published | undefined>(undefined), alive = useRef(false), request = useRef<AbortController | undefined>(undefined), claimed = useRef(false);
  const offAccount = useRef<(() => void) | undefined>(undefined);
  const current = () => alive.current && opening.current();
  const retire = () => {alive.current = false; request.current?.abort(); offAccount.current?.(); offAccount.current = undefined; holder.current = undefined;};
  const dismiss = () => {retire(); close();};
  const refresh = async () => {
    if (!current() || claimed.current) return;
    request.current?.abort(); const operation = request.current = new AbortController();
    const valid = () => current() && request.current === operation && !operation.signal.aborted;
    holder.current = undefined; setPublished(undefined); setBusy(true); setError(undefined);
    try {
      const record = await browserAppSessions().read(); if (!valid()) return;
      if (record.selectedServerId !== opening.serverId) {dismiss(); return;}
      const names = await browserServerDisplay().choices(record.servers.map(row => ({signIn: row.signIn, pin: row.connection.pin, origin: row.connection.logicalOrigin})), operation.signal);
      if (!valid()) return;
      const nameById = new Map(names.map(row => [row.id, row.label]));
      const account = opening.portico ? browserAccount() : undefined;
      let access: AccountServerAccess | undefined;
      let directory: Awaited<ReturnType<ReturnType<typeof createAccountServerDirectory>['read']>> | undefined;
      offAccount.current?.(); offAccount.current = undefined;
      if (account) {
        try {
          await account.service.synchronize(); if (!valid()) return;
          const session = account.service.getSnapshot().session;
          if (session?.familyId) {
            const scope = accountIdentity(session);
            access = createAccountServerAccess(account, scope);
            const original = access;
            offAccount.current = original.observe?.(() => {if (!original.isCurrent()) dismiss();});
            if (!valid() || !original.isCurrent()) return;
            directory = await createAccountServerDirectory(account, scope).read(operation.signal);
          }
        } catch (failure) {
          if (!valid()) return;
          // Hosted unavailability cannot hide this browser's real local sign-ins.
          // Do not use an unsynchronized account session as directory authority.
          access = undefined; offAccount.current?.(); offAccount.current = undefined; setError(failure);
        }
      }
      if (!valid() || access && !access.isCurrent()) return;
      const directoryAccess = access;
      const servers: Choice[] = [], locals: Choice[] = [];
      const shown = new Set<string>();
      const selectSaved = (id: string) => opening.selectServer ? async () => {await opening.selectServer!(id, record.revision);} : undefined;
      for (const server of [...(directory?.items ?? [])].sort((a, b) => Number(b.status === 'active') - Number(a.status === 'active'))) {
        const isCurrent = server.id === opening.serverId;
        const detail = isCurrent ? t('web.servers.current') : server.status === 'active' ? t('web.servers.online')
          : server.presence.lastSeenAt ? t('web.chooser.lastSeen', {date: defaultI18n.date(server.presence.lastSeenAt, 'dayMonth')}) : t('web.chooser.neverSeen');
        const saved = record.servers.find(row => row.connection.pin.serverId === server.id && row.signIn === 'portico' && row.connection.pin.publicKey === server.publicKey);
        const immutable = Object.freeze({...server, ...(server.routes ? {routes: structuredClone(server.routes)} : {})});
        servers.push(Object.freeze({view: Object.freeze({id: server.id, name: server.name, detail, current: isCurrent,
          detailTone: server.status === 'active' && !isCurrent ? 'online' : undefined, dimmed: server.status !== 'active', available: true}),
          select: saved && opening.selectServer ? selectSaved(server.id) : directoryAccess ? async () => {
            if (!directoryAccess.isCurrent()) throw new DOMException('Account changed', 'AbortError');
            opening.selectHosted(Object.freeze({server: immutable, access: directoryAccess, revision: record.revision}));
          } : undefined}));
        shown.add(server.id);
      }
      for (const saved of record.servers) {
        const id = saved.connection.pin.serverId;
        if (shown.has(id)) continue;
        const name = id === opening.serverId ? opening.serverName : nameById.get(id); if (!name) continue;
        const isCurrent = id === opening.serverId;
        const choice = Object.freeze({view: Object.freeze({id, name, current: isCurrent, available: !!opening.selectServer,
          detail: isCurrent ? t('web.servers.current') : t(saved.signIn === 'local' ? 'accounts.session.local' : 'accounts.session.portico')}), select: selectSaved(id)});
        (saved.signIn === 'local' ? locals : servers).push(choice);
      }
      const value = Object.freeze({record, servers: Object.freeze(servers), locals: Object.freeze(locals)});
      if (!valid()) return; holder.current = value; setPublished(value);
    } catch (failure) {if (valid()) setError(failure);}
    finally {if (valid()) setBusy(false);}
  };
  // Publish only at commit, then sample once for this setup's original lifetime.
  const setupPorts = useRef({refresh});
  useEffect(() => {setupPorts.current = {refresh};});
  useEffect(() => {
    const {refresh} = setupPorts.current;
    alive.current = true; void refresh();
    const off = observeBrowserSessionChanges(() => {if (!claimed.current) void refresh();});
    return () => {retire(); off();};
  }, [opening]);
  const choose = async (entry: SwitchProfileServerEntry) => {
    const value = published, choice = value && [...value.servers, ...value.locals].find(row => row.view === entry);
    if (!current() || !value || holder.current !== value || claimed.current || busy || !choice?.select || entry.current || !entry.available) return;
    claimed.current = true; setBusy(true); setError(undefined); let changed = false;
    try {
      const fresh = await browserAppSessions().read();
      if (!current() || holder.current !== value) return;
      if (fresh.revision !== value.record.revision || fresh.selectedServerId !== value.record.selectedServerId) {changed = true; return;}
      await choice.select();
      if (current()) dismiss();
    } catch (failure) {if (current()) setError(failure);}
    finally {claimed.current = false; if (current()) {setBusy(false); if (changed) void refresh();}}
  };
  const serverViews = new Map<SwitchProfileServerEntry, SwitchProfileServerEntry>();
  const displayServer = (choice: Choice) => {const view = choice.view.current && presentation ? Object.freeze({...choice.view, name: presentation.serverName}) : choice.view;serverViews.set(view, choice.view);return view;};
  const profiles: readonly SwitchProfileServerProfile[] = Object.freeze(opening.profiles.map(profile => {
    const display = presentation?.profiles.find(row => row.id === profile.id);
    return Object.freeze({...profile, ...(display ? {name: display.name, initial: display.initial, art: display.art} : {}), current: profile.id === opening.profileId});
  }));
  return <SwitchProfileServerDialog open onClose={dismiss} profilesLabel={t('web.account.profilesOnServer', {server: presentation?.serverName ?? opening.serverName})}
    profiles={profiles} servers={published?.servers.map(displayServer)} localServers={published?.locals.map(displayServer)} busy={busy && claimed.current} refreshing={busy && !claimed.current}
    error={error ? <AuthError error={error} /> : undefined}
    onSelectProfile={profile => {
      if (!current() || claimed.current) return;
      const index = profiles.indexOf(profile), original = opening.profiles[index];
      if (!original || profile.current) return;
      claimed.current = true; opening.selectProfile(original); dismiss();
    }} onSelectServer={entry => {void choose(serverViews.get(entry) ?? entry);}}
    onRefreshServers={() => {void refresh();}} onUseLocalServer={() => {if (current() && !claimed.current) {claimed.current = true; opening.useLocal(); dismiss();}}} />;
}

/** Transfer a captured Hosted row to the existing canonical sign-in state
 * machine. It owns route proof, membership, challenges and profile selection. */
export function HostedServerOpening({selection, cancel}: {selection: HostedSwitchSelection; cancel: () => void}) {
  type Owner = {selection: HostedSwitchSelection; live: boolean; entry: ReturnType<typeof createBrowserAccountEntry>; form: AccountSignInForm};
  const current = useRef<Owner | undefined>(undefined), [instance, setInstance] = useState<Owner>();
  useLayoutEffect(() => {
    const entry = createBrowserAccountEntry(), form = new AccountSignInForm(entry);
    const owner: Owner = {selection, live: true, entry, form};
    current.current = owner; setInstance(owner);
    void entry.openHostedServer(selection.server, selection.access, selection.revision);
    // Retire the exact operation during commit, before passive cleanup or a
    // held continuation can run. Strict replay creates another entry and form.
    return () => {
      owner.live = false; if (current.current === owner) current.current = undefined;
      try {form.dispose();} finally {entry.dispose();}
    };
  }, [selection]);
  const owner = instance?.selection === selection && instance.live && current.current === instance ? instance : undefined;
  return owner ? <AccountSignInView entry={owner.entry} form={owner.form} initialize={false} onCancel={cancel} /> : null;
}
