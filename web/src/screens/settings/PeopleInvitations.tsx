import {useCallback, useEffect, useLayoutEffect, useRef, useState} from 'react';
import {defaultI18n, type MessageId} from '@i18n';
import type {ServerInvitation} from '@core/direct-account.ts';
import {settingsSpec} from '@core/presentation/settings-spec.ts';
import {randomId} from '@core/random-id.ts';
import {Button} from '../../kit/controls';
import {Dialog} from '../../kit/overlays/Dialog';
import {useRetained} from '../../kit/overlays';
import {SettingsGroup, SettingsPageFailure, SettingsTable, type SettingsTableRow, type TableQuery} from '../../kit/settings';
import kitStyles from '../../kit/settings/settings.module.css';
import type {SettingsInvitations} from './invitations';
import {InvitationsView, type InvitationScope, type ResidentInvitations, type InvitationOpening, type InvitationRead, type InvitationRevoke} from './people-invitations-view';
import {PeopleInvitationCard} from './PeopleInvitationCard';

const t = defaultI18n.t;
const section = settingsSpec.find(candidate => candidate.scope === 'server' && candidate.id === 'people')!;
const group = section.groups![1], model = group.rows[0].control;
const DAY = 86_400_000;
const date = new Intl.DateTimeFormat(undefined, {dateStyle: 'medium'});
const initialQuery: TableQuery = {search: '', filters: {status: 'all'}, sort: {by: model.sort?.by ?? 'sent', direction: (model.sort?.dir as 'asc' | 'desc' | undefined) ?? 'desc'}};

function invitationRow(invitation: ServerInvitation, libraries: readonly {id: string; name: string}[], now: number): SettingsTableRow {
  const state = invitation.state, notes = invitation.notes?.trim() ?? '';
  const title = notes || t('next.settings.invitations.linkOnly');
  const role = invitation.role === 'admin';
  const names = invitation.allowedLibraries.map(id => libraries.find(library => library.id === id)?.name).filter((name): name is string => !!name);
  const access = !invitation.allowedLibraries.length ? t('next.settings.allLibraries')
    : names.length === invitation.allowedLibraries.length && names.length <= 2 ? defaultI18n.list(names)
    : t('next.settings.invitations.libraries', {count: invitation.allowedLibraries.length});
  const days = Math.ceil((Date.parse(invitation.expiresAt) - now) / DAY);
  const status = state === 'pending' ? days > 0 && days <= 2
      ? {title: t('next.settings.invitations.expiresIn', {count: Math.max(1, days)}), tone: 'warn' as const}
      : {title: t('next.settings.personStatus.waiting'), tone: 'quiet' as const}
    : state === 'accepted' ? {title: invitation.porticoUsername ? t('next.settings.invitations.usedBy', {name: invitation.porticoUsername}) : t('next.settings.invitations.used'), tone: 'ok' as const}
    : state === 'expired' ? {title: t('next.settings.invitations.expired'), tone: 'bad' as const}
    : {title: t('next.settings.invitations.revokedState'), tone: 'quiet' as const};
  return {id: invitation.id, label: title, cells: {
    invite: {title, ...(invitation.porticoUsername ? {meta: invitation.porticoUsername} : {}), avatar: Array.from(title)[0]?.toLocaleUpperCase() ?? 'L', avatarKey: title},
    role: {title: t(role ? 'next.shell.administrator' : 'next.settings.member'), tone: role ? 'accent' : 'quiet'},
    access: {title: access},
    sent: {title: date.format(new Date(invitation.createdAt))},
    status,
  }};
}

/** The drawn Invitations group: the honest server table, its counted pages and
 * the link/revoke commands. Nothing is cached: every read reauthorizes and the
 * share link is prepared only for the explicit row opening, then copied with a
 * fresh gesture from the existing ready card. */
export function PeopleInvitations({invitations, serverName, signIn, libraries, authority, version, openInvite, openHome, reportError}: Readonly<{
  invitations: SettingsInvitations;
  serverName: string;
  signIn?: 'local' | 'portico';
  libraries: readonly {id: string; name: string}[];
  authority: Readonly<{isCurrent: () => boolean; onAuthorityChanged: () => void}>;
  /** Bumped by the create flow so a new invitation appears without a reload. */
  version: number;
  openInvite: ((opener: HTMLElement) => void) | undefined;
  openHome: () => void;
  reportError: (error: unknown) => void;
}>) {
  const [entry, setEntry] = useState<Readonly<{view: InvitationsView; invitations: SettingsInvitations; authority: typeof authority; id: number}>>();
  const entrySequence = useRef(0);
  // The lease is created by a committed layout, including StrictMode remounts.
  useLayoutEffect(() => {
    const view = new InvitationsView(authority.isCurrent);
    const owned = Object.freeze({view, invitations, authority, id: ++entrySequence.current});
    setEntry(owned);
    return () => owned.view.retire();
  }, [invitations, authority]);
  // A successor render cannot combine a predecessor's view with new ports.
  // A fresh layout entry also gives the child fresh query/page/modal state.
  return entry && entry.invitations === invitations && entry.authority === authority && entry.view.current()
    && <InvitationTable key={entry.id} version={version} view={entry.view} invitations={entry.invitations} serverName={serverName} signIn={signIn} libraries={libraries} authority={entry.authority}
    openInvite={openInvite} openHome={openHome} reportError={reportError} />;
}

function InvitationTable({view, invitations, serverName, signIn, libraries, authority, version, openInvite, openHome, reportError}: Readonly<{
  view: InvitationsView;
  version: number;
  invitations: SettingsInvitations;
  serverName: string;
  signIn?: 'local' | 'portico';
  libraries: readonly {id: string; name: string}[];
  authority: Readonly<{isCurrent: () => boolean; onAuthorityChanged: () => void}>;
  openInvite: ((opener: HTMLElement) => void) | undefined;
  openHome: () => void;
  reportError: (error: unknown) => void;
}>) {
  const [query, setQuery] = useState<TableQuery>(initialQuery), [refresh, setRefresh] = useState(0);
  const [scope, setScope] = useState<InvitationScope>();
  const [resident, setResident] = useState<ResidentInvitations>();
  const [busy, setBusy] = useState<InvitationScope | InvitationRead>(), [error, setError] = useState<unknown>();
  const loading = !!busy;
  const [note, setNote] = useState<Readonly<{message: MessageId}>>();
  const [confirming, setConfirming] = useState<InvitationOpening>();
  const [sharing, setSharing] = useState<InvitationOpening>();
  const [running, setRunning] = useState<InvitationRevoke>();
  const noteTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  useEffect(() => () => clearTimeout(noteTimer.current), []);
  const failure = (value: unknown, selected: InvitationScope) => {
    const status = value && typeof value === 'object' && 'status' in value ? value.status : undefined;
    if (status === 401 || status === 403) {authority.onAuthorityChanged();return;}
    setError((before: unknown) => view.scopeCurrent(selected) ? value : before);
  };
  const read = (ticket: InvitationRead, cursor: string) => {
    void invitations.read(ticket.scope.query, cursor, {signal: ticket.life.signal, isCurrent: () => view.readCurrent(ticket)}).then(value => {
      const page = view.acceptRead(ticket, value);
      if (page) {
        setResident(before => view.pageCurrent(page) ? page : before);
        setBusy(before => before === ticket ? undefined : before);
      }
    }).catch(value => {
      if (view.refuseRead(ticket)) {setBusy(before => before === ticket ? undefined : before);failure(value, ticket.scope);}
    });
  };
  // Publish only at commit, then sample once for this setup's original lifetime.
  const setupPorts = useRef({read});
  useLayoutEffect(() => {setupPorts.current = {read};});
  useLayoutEffect(() => {
    const {read} = setupPorts.current;
    const owned = view.replace(query);
    setScope(owned);setResident(undefined);setBusy(owned);setError(undefined);setConfirming(undefined);setSharing(undefined);setRunning(undefined);
    const timer = setTimeout(() => {
      const ticket = view.beginRead(owned);
      if (ticket) {setBusy(ticket);read(ticket, '');}
    }, owned.query.search ? 200 : 0);
    return () => {clearTimeout(timer);view.retireScope(owned);};
    // The read owns these exact committed source/authority closures. A new
    // source creates a new view; no pending callback reads replacement refs.
  }, [view, query, refresh, version]);
  useLayoutEffect(() => confirming ? view.commitOpening(confirming) : undefined, [view, confirming]);
  const page = view.pageCurrent(resident) ? resident : undefined;
  const opening = view.openingCurrent(confirming) ? confirming : undefined;
  const shareOpening = view.openingCurrent(sharing) ? sharing : undefined;
  const shownShare = useRetained(shareOpening), shownRevoke = useRetained(opening);
  const shareCurrent = useCallback(() => view.openingCurrent(sharing), [view, sharing]);
  const revoking = !!opening && running?.opening === opening;
  const more = () => {
    if (!scope || !page?.value.nextCursor) return;
    const ticket = view.beginRead(scope, page);
    if (ticket) {setBusy(ticket);read(ticket, page.value.nextCursor);}
  };
  const show = (message: MessageId) => {
    const owned = Object.freeze({message});
    clearTimeout(noteTimer.current);setNote(owned);
    noteTimer.current = setTimeout(() => {if (view.current()) setNote(before => before === owned ? undefined : before);}, 4000);
  };
  const cancel = (selected: InvitationOpening) => {
    if (view.cancel(selected)) setConfirming(before => before === selected ? undefined : before);
  };
  const closeShare = (selected: InvitationOpening) => {
    if (view.cancel(selected)) setSharing(before => before === selected ? undefined : before);
  };
  const revoke = async (selected: InvitationOpening) => {
    let ticket: InvitationRevoke | undefined;
    try {
      ticket = view.beginRevoke(selected, randomId);
      if (!ticket) return;
      const owned = ticket;
      setRunning(owned);
      await invitations.revoke(selected.row, owned.operationId, {signal: selected.life.signal, isCurrent: () => view.revokeCurrent(owned)});
      if (!view.settleRevoke(owned, true)) return;
      setConfirming(before => before === selected ? undefined : before);
      setRunning(before => before === owned ? undefined : before);
      show('next.settings.invitations.revoked');
      // Retire the page synchronously; held row callbacks cannot run while
      // React is scheduling the refresh.
      view.retireScope(selected.page.scope);setRefresh(value => value + 1);
    } catch (value) {
      if (ticket ? view.settleRevoke(ticket, false) : view.openingCurrent(selected)) {
        const owned = ticket;
        if (owned) setRunning(before => before === owned ? undefined : before);
        reportError(value);
      }
    }
  };
  const now = Date.now();
  const rows = (page?.value.items ?? []).map(invitation => invitationRow(invitation, libraries, now));
  const filtered = !!query.search.trim() || Object.values(query.filters).some(value => value !== 'all');
  const actionCopy = model.rowActions![0], revokeCopy = model.rowActions![1];
  return <>
    <SettingsGroup title={t(group.title!)} plain actions={openInvite && <Button size="small" variant="primary" icon="i-plus" iconSize={20}
      onClick={event => {if (view.scopeCurrent(scope)) openInvite(event.currentTarget);}}>{t(model.addLabel!)}</Button>}>
    {error && !page ? <SettingsPageFailure retry={() => {if (view.scopeCurrent(scope)) {view.retireScope(scope);setRefresh(value => value + 1);}}}
      home={() => {if (view.scopeCurrent(scope)) openHome();}} /> : <SettingsTable height={model.height}
      label={t(group.title!)} rows={rows}
      columns={model.columns!.map(column => ({id: column.id!, label: t(column.label), kind: column.kind as 'main' | 'text' | 'muted' | 'badge' | 'num' | undefined, sortable: column.sort && (column.id === 'sent' || column.id === 'status'), width: column.width}))}
      query={query} onQueryChange={next => {if (view.scopeCurrent(scope)) {view.retireScope(scope);setQuery(Object.freeze({search: next.search, filters: Object.freeze({...next.filters}), sort: Object.freeze({...next.sort})}));}}}
      filters={model.filters!.map(filter => ({id: filter.id, label: t(filter.label), options: filter.options.map(option => ({value: option.value, label: t(option.label)}))}))}
      searchLabel={t(model.search!)} emptyLabel={t(model.empty!)} loading={loading}
      countLabel={page ? t(filtered ? 'next.settings.invitationsCount' : 'next.settings.invitationsTotal', {matched: page.value.matchedCount, total: page.value.totalCount}) : undefined}
      matchedCount={page?.value.matchedCount} hasMore={!!page?.value.nextCursor} onLoadMore={more}
      actions={error && page ? <span className={kitStyles.status} role="status" data-tone="bad">{t('next.states.pageFailedBody')}</span>
        : note && <span className={kitStyles.status} role="status" data-tone="ok">{t(note.message)}</span>}
      rowActions={row => {
        const selected = page, invitation = selected?.value.items.find(item => item.id === row.id);
        if (!selected || !invitation) return null;
        // State/filter/count all consume the producer's normalized snapshot.
        // The local clock is used only for the display countdown above.
        const state = invitation.state;
        return <span style={{display: 'inline-flex', gap: 4}}>
          {state === 'pending' && <Button size="xs" variant="text" onClick={event => {
            if (!view.rowCurrent(selected, invitation)) return;
            const owned = view.open(selected, invitation, event.currentTarget);
            if (owned) setSharing(owned);
          }}>{t(actionCopy.label)}</Button>}
          {(state === 'pending' || state === 'expired') && <Button size="xs" variant="text" onClick={event => {
            const owned = view.open(selected, invitation, event.currentTarget);
            if (owned) setConfirming(owned);
          }}>{t(revokeCopy.label)}</Button>}
        </span>;
      }} />}
    </SettingsGroup>
    {shownShare && <Dialog open={!!shareOpening} title={t('invite.readyTitle')} size="sm" width={560} returnFocusRef={shownShare.focus}
      onOpenChange={open => {if (!open) closeShare(shownShare);}}
      footer={<Button variant="primary" onClick={() => closeShare(shownShare)}>{t('action.done')}</Button>}>
      <PeopleInvitationCard open invitation={shownShare.row} invitations={invitations} serverName={serverName} signIn={signIn}
        libraries={libraries} isCurrent={shareCurrent} />
    </Dialog>}
    {shownRevoke && <Dialog open={!!opening} title={t(revokeCopy.label)} onOpenChange={open => {if (!open) cancel(shownRevoke);}}
      returnFocusRef={shownRevoke.focus} footer={<><Button disabled={revoking} onClick={() => cancel(shownRevoke)}>{t('next.settings.cancel')}</Button>
        <Button variant="primary" className={kitStyles.danger} loading={revoking} onClick={() => {void revoke(shownRevoke);}}>{t(revokeCopy.label)}</Button></>}>
      <p>{t(revokeCopy.confirm!)}</p>
    </Dialog>}
  </>;
}
