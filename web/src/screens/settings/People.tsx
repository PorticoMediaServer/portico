import {dependsOn} from '@core/freshness-hub.ts';
import {useFreshness} from '../../platform/freshness';
import {mountQuietInput, SettingsOpenContext} from './quiet-refresh';
import {peopleQuietInputs} from './people-refresh';
import {useContext, useCallback, useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore, type ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import {SettingsPeopleClient, type SettingsPeoplePage, type SettingsPeopleQuery, type SettingsPersonRow, type SettingsBulkAccess, type SettingsBulkAction} from '@core/settings-people.ts';
import {personMemberResetCodeRow, settingsRowIndex, settingsSpec, type SettingsRow, type SettingsValue} from '@core/presentation/settings-spec.ts';
import {managesTier, type AccessMember, type AccessTier, type MemberLimits} from '@core/server-administration.ts';
import {MemberResetCodeSheet, SettingsAction, SettingsGroup, SettingsHours, SettingsSelectionList, SettingsStringList, SettingsPanel, SettingsRows, SettingsTable, type SettingPresentation, type SettingsTableHandle, type TableQuery} from '../../kit/settings';
import {Button, RichSelect} from '../../kit/controls';
import {Dialog} from '../../kit/overlays/Dialog';
import {SettingsRow as Row} from '../../kit/shell/SettingsSurfaces';
import {SettingsPageFailure} from '../../kit/settings';
import {peopleTableRow} from './people-table-row';
import type {SettingsViewerScope} from '../../platform/settings-platform';
import type {SettingsInvitations} from './invitations';
import {InvitationShortCodeDialog} from '../../kit/accounts';
import {randomId} from '@core/random-id.ts';
import {PeopleMemberProfiles} from './PeopleMemberProfiles';
import type {PeopleMemberProfilesApi} from './people-member-profiles-api';
import {InviteAttempts, defaultInviteChoices, inviteBody, inviteChoicesOf, type InviteAttempt, type InviteChoices} from './people-invite';
import {PeopleInviteForm} from './people-invite-form';
import {PeopleInvitations} from './PeopleInvitations';
import type {ServerInvitation} from '@core/direct-account.ts';
import type {MemberResetCodeSource} from './member-reset-code';
import {useMemberResetCodeView} from './member-reset-code-view';
import {bulkAccessWithLibraries, type BulkLibraryChoice} from './people-bulk-access';
import type {PeopleDevicesApi} from './people-devices-source';
import {PeopleDevices} from './PeopleDevices';
import {PeopleInvitationCard} from './PeopleInvitationCard';

const invitationStates = ['pending', 'accepted', 'revoked', 'expired'] as const;
const invitationState = (value: string) => invitationStates.find(state => state === value);

const t = defaultI18n.t, section = settingsSpec.find(section => section.scope === 'server' && section.id === 'people')!;
const table = section.groups![0].rows[0].control, personSpec = section.panels!.person;
/** The generated person panel stays untouched. This local clone adds the admitted
 * reset action to the first Account tab's Sign-in group, before Turn off account. */
const personPanel: NonNullable<typeof personSpec> = {
  ...personSpec,
  tabs: (personSpec.tabs ?? []).map((tab, index) => index === 0 ? {
    ...tab,
    groups: (tab.groups ?? []).map(group => group.rows.some(row => row.id === personMemberResetCodeRow.id) ? group : {
      ...group,
      rows: group.rows.flatMap(row => row.id === 'people.turnOff' ? [personMemberResetCodeRow, row] : [row]),
    }),
  } : tab),
};
const label = (id: string) => t(settingsRowIndex.get(id)!.row.label);
const initialQuery: TableQuery = {search: '', filters: {role: 'all', status: 'all'}, sort: {by: 'active', direction: 'desc'}};
function queryOf(query: TableQuery): SettingsPeopleQuery {
  return {search: query.search, role: query.filters.role as SettingsPeopleQuery['role'], status: query.filters.status as SettingsPeopleQuery['status'], sort: query.sort.by as SettingsPeopleQuery['sort'], direction: query.sort.direction};
}
/** Supplied only by the genuine inherit/unlimited/limit producer facade. */
export type PeopleStreamLimits = Readonly<{
  read: (person: AccessMember) => SettingsValue;
  patch: (person: AccessMember, choice: SettingsValue) => MemberLimits;
  readBulk: (access: SettingsBulkAccess | undefined) => SettingsValue;
  patchBulk: (access: SettingsBulkAccess | undefined, choice: SettingsValue) => SettingsBulkAccess;
}>;
export type PeopleBindings = Readonly<{
  inputActivity?: Readonly<{active:()=>boolean;revision:()=>number;subscribe:(listener:()=>void)=>()=>void}>;
  subscribe?: (listener: () => void) => () => void;
  memberResetCodes?: MemberResetCodeSource; devices?: PeopleDevicesApi;
  client: SettingsPeopleClient; serverName: string; libraries: readonly {id: string; name: string}[]; actor: {accountId: string; role: AccessTier};
  authority: Readonly<{scope: SettingsViewerScope; isCurrent: () => boolean; subscribe: (listener: () => void) => () => void; onAuthorityChanged: () => void}>;
  /** The owner's invitation expiry in hours, read from the loaded runtime
   * document when a new invitation form opens. A started invitation keeps the
   * value it opened with. */
  inviteExpiryHours?: () => number | undefined;
  /** The owner's default role for a new invitation, in the dialog's own words
   * (member or admin), read when a form opens and kept for that invitation. */
  inviteRole?: () => 'member' | 'admin' | undefined;
  /** Another person's profiles for the Profiles tab's Manage. */
  memberProfiles?: PeopleMemberProfilesApi;
  streamLimits?: PeopleStreamLimits; invitations?: SettingsInvitations; invite?: (opener: HTMLElement) => void; signIn?: 'local' | 'portico'; openHome: () => void; reportError: (error: unknown) => void;
}>;

/** The table query, detail revision and bulk transaction stay bound to one real session. */
export function PeopleScreen({bindings, initialStatus, rowActions, advancedRows}: {bindings: PeopleBindings; initialStatus?: SettingsPeopleQuery['status']; rowActions?: (person: SettingsPersonRow) => ReactNode; advancedRows?: ReactNode}) {
  const scope = bindings.authority.scope;
  return <PeopleWorkspace key={JSON.stringify([scope.serverId, scope.authority, scope.accountId, scope.profileId, scope.sessionFamilyId, bindings.actor.role])} bindings={bindings} initialStatus={initialStatus} rowActions={rowActions} advancedRows={advancedRows} />;
}

function PeopleWorkspace({bindings, initialStatus, rowActions, advancedRows}: {bindings: PeopleBindings; initialStatus?: SettingsPeopleQuery['status']; rowActions?: (person: SettingsPersonRow) => ReactNode; advancedRows?: ReactNode}) {
  const {client} = bindings;
  const {hub} = useFreshness();
  const visibility=useContext(SettingsOpenContext);
  const [, redrawFacts] = useState(0);
  useEffect(() => bindings.subscribe?.(() => redrawFacts(value => value + 1)), [bindings]);
  const [issued, setIssued] = useState<ServerInvitation>(), [inviteOpen, setInviteOpen] = useState(false);
  const [invitationVersion, setInvitationVersion] = useState(0);
  const [formOpen, setFormOpen] = useState(false), [choices, setChoices] = useState<InviteChoices>(defaultInviteChoices);
  /** The drawn ladder is whole days on the server and hours in the dialog. */
  const openedChoices = (): InviteChoices => {
    const hours = bindings.inviteExpiryHours?.(), role = bindings.inviteRole?.();
    return Object.freeze({...defaultInviteChoices,
      ...(hours && Number.isFinite(hours) && hours > 0 ? {expiresInHours: hours} : {}),
      ...(role ? {role} : {})});
  };
  const [formBusy, setFormBusy] = useState(false), [formError, setFormError] = useState<unknown>(), [retained, setRetained] = useState<InviteAttempt>();
  const inviteLife = useRef(new AbortController()), inviteOpener = useRef<HTMLElement | null>(null), alive = useRef(true);
  const formSession = useRef(0), formActive = useRef(false);
  const formToken = formSession.current;
  const attempts = useRef<InviteAttempts | undefined>(undefined);
  if (!attempts.current) attempts.current = new InviteAttempts(randomId);
  useEffect(() => () => { alive.current = false; attempts.current?.retire(); inviteLife.current.abort(); }, []);
  /** Opening the form writes nothing; Create is the explicit command. */
  const openInvite = (opener: HTMLElement) => {
    if (!alive.current || !bindings.authority.isCurrent() || attempts.current?.busy) return;
    ++formSession.current; formActive.current = true;
    inviteOpener.current = opener;
    setFormError(undefined);
    setChoices(openedChoices());
    setFormOpen(true);
  };
  const choicesOf = (attempt: InviteAttempt): InviteChoices => inviteChoicesOf(attempt.body);
  const submitInvite = async (nextChoices: InviteChoices) => {
    const invitations = bindings.invitations, owner = attempts.current!;
    const currentForm = () => alive.current && bindings.authority.isCurrent() && formActive.current && formSession.current === formToken;
    if (!invitations || !currentForm()) return;
    let attempt: InviteAttempt | undefined;
    try { attempt = owner.begin(inviteBody(nextChoices)); } catch (error) { if (currentForm()) setFormError(error); return; }
    if (!attempt) return;
    setFormBusy(true); setFormError(undefined);
    try {
      const created = await invitations.create({...attempt.body, allowedLibraries: [...attempt.body.allowedLibraries]},
        {signal: inviteLife.current.signal, isCurrent: currentForm});
      if (!currentForm()) return;
      if (!invitationState(created.state)) throw new Error('invitation_state_unknown');
      owner.created(); setFormBusy(false); formActive.current = false;
      setRetained(undefined); setFormOpen(false); setIssued(created); setInviteOpen(true);
      setInvitationVersion(value => value + 1);
    } catch (error) {
      if (!currentForm()) return;
      const status = (error as {status?: number})?.status;
      if (status === 401 || status === 403) { owner.retire(); formActive.current = false; inviteLife.current.abort(); failure(error); return; }
      owner.failed(error); setFormError(error); setRetained(owner.pending);
    } finally { if (currentForm()) setFormBusy(false); }
  };
  const live = useSyncExternalStore(bindings.authority.subscribe, bindings.authority.isCurrent, () => false);
  const [denied, setDenied] = useState(false);
  // Frame 24c: arriving from the move in progress lands on the Waiting list.
  const [query, setQuery] = useState<TableQuery>(() => initialStatus ? {...initialQuery, filters: {...initialQuery.filters, status: initialStatus}} : initialQuery);
  const [page, setPage] = useState<SettingsPeoplePage>(), [error, setError] = useState<unknown>();
  // The move read settles after the first render; apply its filter once, and never
  // over a status the owner has already chosen.
  const [loading, setLoading] = useState(false), [selected, setSelected] = useState<ReadonlySet<string>>(new Set()), [refresh, setRefresh] = useState(0);
  const [person, setPerson] = useState<AccessMember>(), [personId, setPersonId] = useState<string>(), [tab, setTab] = useState(personPanel.tabs![0].id);
  const [advanced, setAdvanced] = useState(false), [saving, setSaving] = useState(false), [saved, setSaved] = useState(false);
  const [chooseLibraries, setChooseLibraries] = useState(false);
  const [chooseHours, setChooseHours] = useState(false);
  const [command, setCommand] = useState<{action: SettingsBulkAction; rows: readonly SettingsPersonRow[]} | undefined>(), [bulk, setBulk] = useState<SettingsBulkAccess>();
  const [bulkLibraryChoice, setBulkLibraryChoice] = useState<BulkLibraryChoice>('keep'), [bulkLibraryIds, setBulkLibraryIds] = useState<readonly string[]>([]);
  const tableRef = useRef<SettingsTableHandle>(null), opener = useRef<HTMLElement | null>(null), generation = useRef(0), mounted = useRef(true);
  const firstPageIds = useRef<ReadonlySet<string>>(new Set());
  const quietSerial = useRef(0), quietListeners = useRef(new Set<() => void>());
  const requests = useRef(new Set<AbortController>()), retired = useRef(false);
  const queryRead = useRef<AbortController | undefined>(undefined), detailRead = useRef<AbortController | undefined>(undefined), pagingRead = useRef<AbortController | undefined>(undefined);
  const quiet = useRef({page,query,personId,person,saving,loading,command,denied,live});
  const confirmOpener = useRef<HTMLElement | null>(null);
  useLayoutEffect(() => {
    quiet.current={page,query,personId,person,saving,loading,command,denied,live};
    for(const listener of quietListeners.current)listener();
  });
  useEffect(() => {const activeRequests = requests.current;mounted.current = true;return () => {mounted.current = false;for (const request of activeRequests) request.abort();activeRequests.clear();};}, []);
  const failure = useCallback((error: unknown): boolean => {
    if (!mounted.current || !bindings.authority.isCurrent()) return true;
    const status = error && typeof error === 'object' && 'status' in error ? error.status : undefined;
    if (status === 401 || status === 403) {
      if (retired.current) return true;
      retired.current = true;++generation.current;client.dispose();
      for (const request of requests.current) request.abort();requests.current.clear();
      setDenied(true);setPage(undefined);setPerson(undefined);setPersonId(undefined);setSelected(new Set());setCommand(undefined);setBulk(undefined);setError(undefined);
      bindings.authority.onAuthorityChanged();return true;
    }
    setError(error);return false;
  }, [bindings.authority, client]);
  const admitQuery = useCallback((next: TableQuery) => {
    if (!mounted.current || !bindings.authority.isCurrent() || retired.current || next === quiet.current.query) return;
    ++quietSerial.current;++generation.current;queryRead.current?.abort();pagingRead.current?.abort();pagingRead.current = undefined;
    quiet.current = {...quiet.current, query: next, loading: true};
    setQuery(next);setLoading(true);for (const listener of quietListeners.current) listener();
  }, [bindings.authority]);
  const admitPerson = useCallback((id?: string) => {
    if (!mounted.current || !bindings.authority.isCurrent() || retired.current || id === quiet.current.personId) return;
    ++quietSerial.current;detailRead.current?.abort();
    quiet.current = {...quiet.current, personId: id, person: undefined};
    setPersonId(id);setPerson(undefined);for (const listener of quietListeners.current) listener();
  }, [bindings.authority]);
  const waitingApplied = useRef(false);
  useEffect(() => {
    if (!initialStatus || waitingApplied.current) return;
    waitingApplied.current = true;
    const current = quiet.current.query;
    if (current.filters.status === 'all') admitQuery({...current, filters: {...current.filters, status: initialStatus}});
  }, [initialStatus, admitQuery]);
  useEffect(() => {
    if (!live || denied || quiet.current.query !== query) return;
    ++quietSerial.current;const current = ++generation.current, controller = new AbortController();queryRead.current = controller;
    quiet.current = {...quiet.current, query, page: undefined, loading: true};for (const listener of quietListeners.current) listener();
    setPage(undefined); setLoading(true); setError(undefined); setSelected(new Set());
    const timer = setTimeout(() => {void client.read(queryOf(query), '', controller.signal).then(value => {if (!controller.signal.aborted && current === generation.current && bindings.authority.isCurrent() && quiet.current.query === query) {firstPageIds.current = new Set(value.items.map(row => row.accountId));quiet.current = {...quiet.current, page: value};setPage(value);}}).catch(error => {if (!controller.signal.aborted && current === generation.current) failure(error);}).finally(() => {if (queryRead.current === controller && current === generation.current) {queryRead.current = undefined;quiet.current = {...quiet.current, loading: false};setLoading(false);for (const listener of quietListeners.current) listener();}});}, query.search ? 200 : 0);
    return () => {clearTimeout(timer); controller.abort();if (queryRead.current === controller) queryRead.current = undefined;};
  }, [client, query, refresh, live, denied, failure, bindings.authority]);
  useEffect(() => {
    if (quiet.current.personId !== personId) return;
    if (!live || denied || !personId) {setPerson(undefined); return;}
    const controller = new AbortController();detailRead.current = controller; setPerson(undefined); setError(undefined); setChooseLibraries(false); setChooseHours(false);
    void client.person(personId, controller.signal).then(value => {if (!controller.signal.aborted && detailRead.current === controller && quiet.current.personId === personId && bindings.authority.isCurrent()) {quiet.current = {...quiet.current, person: value};setPerson(value);}}).catch(error => {if (!controller.signal.aborted) failure(error);});
    return () => {controller.abort();if (detailRead.current === controller) detailRead.current = undefined;};
  }, [client, personId, live, denied, failure, bindings.authority]);
  useEffect(() => {
    const current=() => mounted.current && bindings.authority.isCurrent() && !retired.current;
    const listeners=quietListeners.current;
    const subscribe=(listener: () => void) => {listeners.add(listener);const off=bindings.inputActivity?.subscribe(listener);return () => {off?.();listeners.delete(listener);};};
    const blocked=() => !!bindings.inputActivity?.active() || !quiet.current.live || quiet.current.denied || quiet.current.saving || quiet.current.loading || !!quiet.current.command;
    const inputs=peopleQuietInputs({client,current,blocked,subscribe,serial:()=>quietSerial.current+(bindings.inputActivity?.revision()??0),
      get:()=>({...quiet.current,query:queryOf(quiet.current.query)}),firstPageIds:()=>firstPageIds.current,requests:requests.current,
      installPage:(page,ids)=>{firstPageIds.current=ids;quiet.current={...quiet.current,page};setPage(page);},
      installPerson:person=>{
        // This accepted installation supersedes the older ordinary selection read.
        const older=detailRead.current;detailRead.current=undefined;
        quiet.current={...quiet.current,person};setPerson(person);older?.abort();
      },
      closePerson:id=>{if(quiet.current.personId===id){admitPerson();setSelected(before=>new Set([...before].filter(value=>value!==id)));}},
      denied:failure,
    }).map(input=>mountQuietInput(hub,'settings/people',[dependsOn.list('members'),dependsOn.settings('server')],input,visibility));
    return () => {for(const input of inputs)input.dispose();};
  },[hub,client,bindings.authority,bindings.inputActivity,failure,visibility,admitPerson]);
  const more = () => {
    const installed = quiet.current, before = installed.page;
    if (!mounted.current || !installed.live || installed.denied || !bindings.authority.isCurrent() || installed.loading || !before?.nextCursor) return;
    const current = generation.current, request = new AbortController();++quietSerial.current;
    pagingRead.current = request;requests.current.add(request);
    // SettingsTable can call from a passive effect. Fence before React commits loading.
    quiet.current = {...installed, loading: true};setLoading(true);for (const listener of quietListeners.current) listener();
    void client.read(queryOf(installed.query), before.nextCursor, request.signal).then(next => {
      // Detail selection fences quiet adoption, but does not invalidate this table's page.
      if (!request.signal.aborted && pagingRead.current === request && mounted.current && current === generation.current && bindings.authority.isCurrent() && quiet.current.query === installed.query && quiet.current.page === before) {
        const ids = new Set(before.items.map(row => row.accountId));
        const page = {...next, items: [...before.items, ...next.items.filter(row => !ids.has(row.accountId))]};
        quiet.current = {...quiet.current, page};setPage(page);
      }
    }).catch(error => {if (!request.signal.aborted && current === generation.current) failure(error);}).finally(() => {
      requests.current.delete(request);
      if (pagingRead.current === request) {
        pagingRead.current = undefined;
        if (mounted.current && current === generation.current) {quiet.current = {...quiet.current, loading: false};setLoading(false);for (const listener of quietListeners.current) listener();}
      }
    });
  };
  const run = async (action: () => Promise<void>) => {
    if (!live || denied || !bindings.authority.isCurrent() || saving) return; ++quietSerial.current;quiet.current={...quiet.current,saving:true};setSaving(true); setSaved(false); setError(undefined);
    try {await action(); if (mounted.current && bindings.authority.isCurrent() && !retired.current) {setSaved(true); setRefresh(value => value + 1);}}
    catch (error) {if (!failure(error)) bindings.reportError(error);}
    finally {if (mounted.current) setSaving(false);}
  };
  const canEdit = live && !denied && bindings.authority.isCurrent() && !!person && person.accountId !== bindings.actor.accountId && managesTier(bindings.actor.role, person.role);
  const resetView = useMemberResetCodeView(bindings.memberResetCodes, person, bindings.authority.isCurrent, saving || !canEdit, bindings.reportError, tab);
  const values: Readonly<Record<string, SettingsValue | undefined>> = person ? {
    'people.username': person.username, ...(bindings.signIn === 'portico' && person.email !== undefined ? {'people.email': person.email} : {}), 'people.notes': person.notes ?? '', 'people.role': person.role === 'admin' ? 'administrator' : person.role,
    'people.libraries': person.allowedLibraries.length || chooseLibraries ? 'some' : 'all', ...(bindings.streamLimits ? {'people.maxStreams': bindings.streamLimits.read(person)} : {}),
    'people.awayAccess': person.limits.allowRemote ? 'allowed' : 'home', 'people.conversions': person.limits.conversion === 'audio' ? 'sound' : person.limits.conversion,
    'people.hours': person.limits.schedule.windows.length || chooseHours ? 'set' : 'any', 'people.hoursTimezone': person.limits.schedule.timezone,
    'people.maxRating': person.limits.maxContentRating || 'none', 'people.allowUnrated': person.limits.allowUnrated, 'people.accessTokens': person.limits.accessTokens,
  } : {};
  const state = (row: SettingsRow): SettingPresentation => row.id === personMemberResetCodeRow.id
    ? {present: resetView?.present === true, disabled: !resetView?.canMake}
    : {present: !!person && (Object.hasOwn(values, row.id) || ['people.turnOff', 'people.delete', 'people.profiles', 'people.hoursList', 'people.hiddenLabels'].includes(row.id) || row.id === 'people.libraryList' && (chooseLibraries || !!person.allowedLibraries.length)),
      value: values[row.id], displayValue: typeof values[row.id] === 'string' ? values[row.id] as string : undefined, disabled: saving || !canEdit};
  const change = (row: SettingsRow, value: SettingsValue) => {
    if (!person || !canEdit) return;
    if (row.id === 'people.libraries' && value === 'some') {setChooseLibraries(true); return;}
    if (row.id === 'people.hours' && value === 'set') {setChooseHours(true); return;}
    const before = person;
    void run(async () => {
      if (row.id === 'people.notes') {const next = await client.notes(before, String(value)); setPerson(current => current?.accountId === before.accountId ? next : current); return;}
      if (row.id === 'people.role') {const next = await client.role(before, value === 'administrator' ? 'admin' : value as AccessTier); setPerson(current => current?.accountId === before.accountId ? next : current); return;}
      if (row.id === 'people.libraries' && value === 'all') {await libraryAccess([]); setChooseLibraries(false); return;}
      const patch: {-readonly [K in keyof MemberLimits]?: MemberLimits[K]} = {};
      if (row.id === 'people.maxStreams') {
        if (!bindings.streamLimits) throw new Error('settings_stream_limit_inheritance_unavailable');
        const saved = await client.limits(before, bindings.streamLimits.patch(before, value));
        setPerson(current => current?.accountId === before.accountId ? {...before, limits: saved.limits, limitsRevision: saved.revision} : current);
        return;
      }
      else if (row.id === 'people.awayAccess') patch.allowRemote = value === 'allowed';
      else if (row.id === 'people.conversions') patch.conversion = value === 'sound' ? 'audio' : value as MemberLimits['conversion'];
      else if (row.id === 'people.maxRating') patch.maxContentRating = value === 'none' ? '' : String(value);
      else if (row.id === 'people.allowUnrated') patch.allowUnrated = value === true;
      else if (row.id === 'people.accessTokens') patch.accessTokens = value === true;
      else if (row.id === 'people.hoursTimezone') patch.schedule = {...before.limits.schedule, timezone: String(value)};
      else if (row.id === 'people.hours' && value === 'any') patch.schedule = {...before.limits.schedule, windows: []};
      else return;
      const saved = await client.limits(before, {...before.limits, ...patch});
      setPerson(current => current?.accountId === before.accountId ? {...before, limits: saved.limits, limitsRevision: saved.revision} : current);
    });
  };
  const libraryAccess = async (allowedLibraries: readonly string[]) => {
    if (!person || !canEdit) return;
    const row = page?.items.find(row => row.accountId === person.accountId); if (!row) return;
    await client.bulk([{...row, revision: person.revision, limitsRevision: person.limitsRevision}], 'access', {allowedLibraries: [...allowedLibraries]});
    const next = await client.person(person.accountId); setPerson(current => current?.accountId === next.accountId ? next : current);
  };
  const hours = (windows: MemberLimits['schedule']['windows']) => {
    if (!person || !canEdit) return;
    const before = person;
    void run(async () => {const saved = await client.limits(before, {...before.limits, schedule: {...before.limits.schedule, windows}}); setPerson(current => current?.accountId === before.accountId ? {...before, limits: saved.limits, limitsRevision: saved.revision} : current);});
  };
  const ask = (action: SettingsBulkAction, ids: readonly string[], element?: HTMLElement) => {
    if (!live || denied || !bindings.authority.isCurrent()) return;
    const rows = (page?.items ?? []).filter(row => ids.includes(row.accountId)).map(row => person?.accountId === row.accountId ? {...row, revision: person.revision, limitsRevision: person.limitsRevision} : row);
    if (!rows.length || rows.some(row => row.accountId === bindings.actor.accountId || !managesTier(bindings.actor.role, row.role))) return;
    confirmOpener.current = element ?? (document.activeElement instanceof HTMLElement ? document.activeElement : null); setCommand({action, rows}); if (action === 'access') {setBulk({}); setBulkLibraryChoice('keep'); setBulkLibraryIds([]);}
  };
  const personRow = page?.items.find(row => row.accountId === personId);
  const filtered = !!query.search.trim() || Object.values(query.filters).some(value => value !== 'all');
  const special = (row: SettingsRow) => {
    if (!person) return null;
    if (row.id === 'people.libraryList') return <SettingsSelectionList label={label(row.id)} minimum={1} items={bindings.libraries.map(library => ({id: library.id, label: library.name}))} selected={person.allowedLibraries} disabled={!canEdit || saving} onChange={ids => {void run(() => libraryAccess(ids));}} />;
    if (row.id === 'people.hoursList') return <SettingsHours windows={person.limits.schedule.windows} disabled={!canEdit || saving} onChange={hours} />;
    if (row.id === 'people.hiddenLabels') return <SettingsStringList label={label(row.id)} values={person.limits.tagPolicy.deniedLabels} addLabel={t(row.control.addLabel!)} disabled={!canEdit || saving} onChange={deniedLabels => {
      const before = person;
      void run(async () => {const saved = await client.limits(before, {...before.limits, tagPolicy: {deniedLabels}}); setPerson(current => current?.accountId === before.accountId ? {...before, limits: saved.limits, limitsRevision: saved.revision} : current);});
    }} />;
    if (row.id === 'people.profiles') return bindings.memberProfiles && managesTier(bindings.actor.role, person.role) && person.accountId !== bindings.actor.accountId
      ? <PeopleMemberProfiles api={bindings.memberProfiles} accountId={person.accountId} canEdit={canEdit} serverOwner={bindings.actor.role === 'owner'} />
      : <>{person.profileNames?.map((name, index) => <Row key={index} label={name}><span /></Row>)}</>;
    if (row.control.type === 'readonly') return <span>{values[row.id] as string}</span>;
    if (row.id === personMemberResetCodeRow.id) {
      if (row.control.type !== 'action' || !resetView?.present) return null;
      const view = resetView;
      return <SettingsAction appearance="glass" disabled={!view.canMake} onClick={() => view.begin()}>{t(row.control.label!)}</SettingsAction>;
    }
    if (row.control.type === 'action') return <SettingsAction appearance={row.control.style === 'danger' ? 'danger' : 'glass'} disabled={!canEdit || saving || !personRow} onClick={() => {if (personRow) ask(row.id === 'people.delete' ? 'remove' : person.disabled ? 'enable' : 'disable', [personId!]);}}>{row.id === 'people.turnOff' && person.disabled ? t('next.settings.turnOn') : t(row.control.label!)}</SettingsAction>;
    return null;
  };
  const applyCommand = () => {
    if (!command || command.action === 'access' && bulkLibraryChoice === 'some' && !bulkLibraryIds.length) return;
    const access = command.action === 'access' ? bulkAccessWithLibraries(bulk, bulkLibraryChoice, bulkLibraryIds) : undefined;
    void run(async () => {await client.bulk(command.rows, command.action, access); setCommand(undefined); setBulk(undefined); setSelected(new Set()); if (command.action === 'remove' && command.rows.some(row => row.accountId === personId)) {admitPerson();} else if (personId) {const next = await client.person(personId); setPerson(current => current?.accountId === next.accountId ? next : current);}});
  };
  if (!live || denied) return null;
  return <>
    <SettingsGroup title={t(section.groups![0].title!)} plain actions={<Button size="small" variant="primary" icon="i-plus" iconSize={20} disabled={!bindings.invite && !bindings.invitations}
      onClick={event => { if (!bindings.authority.isCurrent()) return; if (bindings.invite) bindings.invite(event.currentTarget); else void openInvite(event.currentTarget); }}>{t(table.addLabel!)}</Button>}>
    {error && !page && !personId ? <SettingsPageFailure retry={() => setRefresh(value => value + 1)} home={bindings.openHome} /> : <SettingsTable ref={tableRef} height={table.height}
      label={t(section.title)} rows={(page?.items ?? []).map(row => peopleTableRow(row, bindings.actor))} columns={table.columns!.map(column => ({id: column.id!, label: t(column.label), kind: column.kind as 'main', sortable: column.sort, width: column.width}))}
      query={query} onQueryChange={admitQuery} filters={table.filters!.map(filter => ({id: filter.id, label: t(filter.label), showAllValue: false,
        options: [...filter.options.map(option => ({value: option.value, label: t(option.label)})),
          ...(filter.id === 'status' && !filter.options.some(option => option.value === 'waiting') ? [{value: 'waiting', label: t('next.settings.personStatus.waiting')}] : [])]}))}
      searchLabel={t(table.search!)} countLabel={page ? t(filtered ? 'next.settings.peopleCount' : 'next.settings.peopleTotal', {matched: page.matchedCount, total: page.totalCount}) : undefined} emptyLabel={t(table.empty!)} loading={loading}
      rowActions={rowActions ? row => {const person = page?.items.find(item => item.accountId === row.id); return person ? rowActions(person) ?? null : null;} : undefined}
      matchedCount={page?.matchedCount} hasMore={!!page?.nextCursor} onLoadMore={more} selected={selected} onSelectionChange={setSelected}
      rowAction={{label: t(table.rowAction!.label), onAction: (row, element) => {if (row.id === personId) return; if (resetView && !resetView.leave()) return; opener.current = element; admitPerson(row.id); setTab(personPanel.tabs![0].id);}}}
      bulkActions={[
        {id: 'access', label: t(table.select!.bulk[0].label), onAction: (ids, element) => ask('access', ids, element)},
        {id: 'disable', label: t(table.select!.bulk[1].label), onAction: (ids, element) => ask('disable', ids, element)},
        {id: 'remove', label: t(table.select!.bulk[2].label), destructive: true, onAction: (ids, element) => ask('remove', ids, element)},
      ]} />}
    </SettingsGroup>
    {bindings.invitations && <PeopleInvitations invitations={bindings.invitations} serverName={bindings.serverName} signIn={bindings.signIn} libraries={bindings.libraries} authority={bindings.authority}
      version={invitationVersion} openInvite={bindings.invite ?? openInvite} openHome={bindings.openHome} reportError={bindings.reportError} />}
    {advancedRows}
    {personId && <SettingsPanel open onClose={() => {if (resetView && !resetView.leave()) return; admitPerson();}} title={person?.username ?? personRow?.username ?? t(section.title)} audience={t(bindings.actor.role === 'owner' ? 'next.shell.serverVisibility.ownerShared' : 'next.shell.serverVisibility.administrator')}
      returnFocusRef={opener} selectedTab={tab} onTabChange={next => {if (next === tab) return; if (resetView && !resetView.leave()) return; setTab(next);}} saveState={saving ? 'saving' : saved ? 'saved' : undefined}
      error={error ? t('next.states.pageFailedBody') : undefined} tabs={personPanel.tabs!.map(tab => ({id: tab.id, title: t(tab.title), content: !person ? <p role="status">{t('next.settings.loading')}</p> : tab.id === 'devices' && bindings.devices ? <PeopleDevices key={person.accountId} accountId={person.accountId} api={bindings.devices} isCurrent={bindings.authority.isCurrent} canSignOut={canEdit} onError={failure} /> : <SettingsRows holder={tab} state={state} change={change} reset={() => {}} advancedOpen={advanced} onAdvancedChange={setAdvanced}
        renderSpecial={special} />
      }))} />}
    {resetView?.sheet && <MemberResetCodeSheet {...resetView.sheet} />}
    {command?.action === 'access' && <SettingsPanel open title={t(table.select!.bulk[0].label)}
      audience={t(bindings.actor.role === 'owner' ? 'next.shell.serverVisibility.ownerShared' : 'next.shell.serverVisibility.administrator')}
      onClose={() => {if (!saving) {setCommand(undefined); setBulk(undefined);}}} returnFocusRef={confirmOpener}
      confirm={{label: t('next.settings.apply'), pending: saving,
        disabled: bulkLibraryChoice === 'some' && !bulkLibraryIds.length || bulkLibraryChoice === 'keep' && !Object.keys(bulk ?? {}).length, onConfirm: applyCommand}}>
      <SettingsRows holder={section.panels!.bulkAccess} advancedOpen={false} onAdvancedChange={() => {}} reset={() => {}} state={row => ({present: ['bulk.libraries', 'bulk.awayAccess', 'bulk.maxRating'].includes(row.id) || row.id === 'bulk.maxStreams' && !!bindings.streamLimits, disabled: saving,
        value: row.id === 'bulk.libraries' ? bulkLibraryChoice : row.id === 'bulk.maxStreams' ? bindings.streamLimits?.readBulk(bulk) : row.id === 'bulk.awayAccess' ? bulk?.allowRemote === undefined ? 'keep' : bulk.allowRemote ? 'allowed' : 'home' : bulk?.maxContentRating === undefined ? 'keep' : bulk.maxContentRating || 'none',
        ...(row.id === 'bulk.libraries' ? {control: <div>
          <RichSelect aria-label={t(row.label)} value={bulkLibraryChoice} disabled={saving} options={row.control.options!.map(option => ({value: String(option.value), label: t(option.label), description: option.hint && t(option.hint)}))}
            onValueChange={value => {if (value === 'keep' || value === 'all' || value === 'some') setBulkLibraryChoice(value);}} />
          {bulkLibraryChoice === 'some' && <SettingsSelectionList label={t(row.label)} items={bindings.libraries.map(library => ({id: library.id, label: library.name}))} selected={bulkLibraryIds} onChange={setBulkLibraryIds} disabled={saving} minimum={1} />}
        </div>} : {})})}
        change={(row, value) => setBulk(before => {if (row.id === 'bulk.maxStreams') return bindings.streamLimits?.patchBulk(before, value) ?? before; const next = {...before}; if (row.id === 'bulk.awayAccess') {if (value === 'keep') delete next.allowRemote; else next.allowRemote = value === 'allowed';} else {if (value === 'keep') delete next.maxContentRating; else next.maxContentRating = value === 'none' ? '' : String(value);} return next;})} renderSpecial={() => null} />
    </SettingsPanel>}
    {command && command.action !== 'access' && <Dialog open title={t('next.settings.confirmPeople', {count: command.rows.length})} onOpenChange={open => {if (!open && !saving) {setCommand(undefined); setBulk(undefined);}}}
      returnFocusRef={confirmOpener} footer={<><Button disabled={saving} onClick={() => {setCommand(undefined); setBulk(undefined);}}>{t('next.settings.cancel')}</Button><Button variant="primary" loading={saving} onClick={applyCommand}>{t('next.settings.apply')}</Button></>}>
      <p>{t(command.action === 'remove' ? table.select!.bulk[2].confirm! : table.select!.bulk[1].confirm!)}</p>
    </Dialog>}
    <PeopleInviteForm open={formOpen} onOpenChange={open => { if (formSession.current !== formToken || !formActive.current || attempts.current?.busy) return; if (!open) { formActive.current = false; ++formSession.current; setFormError(undefined); } setFormOpen(open); }}
      choices={choices} onChoices={next => { if (formActive.current && formSession.current === formToken && !attempts.current?.busy) setChoices(next); }} libraries={bindings.libraries} signIn={bindings.signIn}
      busy={formBusy} error={formError} retained={retained} audience={t(bindings.actor.role === 'owner' ? 'next.shell.serverVisibility.ownerShared' : 'next.shell.serverVisibility.administrator')}
      onCreate={() => { void submitInvite(choices); }}
      onRetry={() => { if (retained && formActive.current && formSession.current === formToken) { void submitInvite(choicesOf(retained)); } }} />
    {issued && <InvitationShortCodeDialog open={inviteOpen} onOpenChange={setInviteOpen}
      invitation={{id: issued.id, revision: issued.revision, state: invitationState(issued.state)!}}
      request={(operationId, signal, isCurrent) => bindings.invitations!.shortCode(issued, operationId, {signal, isCurrent})}
      isCurrent={bindings.authority.isCurrent} issueOnOpen returnFocusRef={inviteOpener}>
      <PeopleInvitationCard signIn={bindings.signIn} open={inviteOpen} invitation={issued} invitations={bindings.invitations!} serverName={bindings.serverName} libraries={bindings.libraries} isCurrent={bindings.authority.isCurrent} />
    </InvitationShortCodeDialog>}
  </>;
}
