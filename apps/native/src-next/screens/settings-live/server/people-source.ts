import {routePath} from '../../../../../../packages/contracts/src/routes';
import {parseLibraryList} from '../../../../../../packages/client-core/src/library-list';
import type {Library} from '../../../../../../packages/client-core/src';
import {SettingsPeopleClient, type SettingsPeoplePage, type SettingsPeopleQuery, type SettingsPersonRow} from '../../../../../../packages/client-core/src/settings-people';
import {PeopleInvitationsClient, privatePersonNotes, type PeopleInvitationPage, type ServerInvitation} from '../../../../../../packages/client-core/src/direct-account';
import {deleteMember, previewMemberDeletion, parseAccessDevicePage, signOutMemberDevice, signOutMemberDevices, type AccessDevice, type AccessMember, type MemberDeletionPreview, type MemberLimits} from '../../../../../../packages/client-core/src/server-administration';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import {NativeScreenLifetime, screenScopeEnded} from '../lifetime';
import {nativeStorageOwner} from './storage/space-source';

export const nativePeopleQuery: SettingsPeopleQuery = Object.freeze({search: '', role: 'all', status: 'all', sort: 'person', direction: 'asc'});
/** `ready` is the Invitation ready card: right after creating (a short code is
 * issued with it), or opened again to share a waiting invitation. */
export type InvitationOpening = Readonly<{invitation?: ServerInvitation; ready?: 'created' | 'shared'}>;
export type InvitationDraft = Readonly<{notes: string; role: 'member' | 'admin'; allowedLibraries: readonly string[]; days: number}>;
type Tab = 'people' | 'invitations';
type Snapshot = Readonly<{
  tab: Tab; query: SettingsPeopleQuery; invitationStatus: '' | 'pending' | 'accepted' | 'revoked' | 'expired';
  people?: SettingsPeoplePage; invitations?: PeopleInvitationPage; cursor: string;
  devices?: Readonly<{person: AccessMember; items: readonly AccessDevice[]; cursor: string}>; devicesLoading?: boolean; devicesError?: unknown;
  libraries?: readonly Library[]; librariesError?: unknown; librariesLoading?: boolean;
  invitationOpening?: InvitationOpening; invitationLink?: string;
  loading: boolean; busy: boolean; opening: boolean; person?: AccessMember; selected?: SettingsPersonRow; error?: unknown;
}>;
const initial: Snapshot = Object.freeze({tab: 'people', query: nativePeopleQuery, invitationStatus: '', cursor: '', loading: false, busy: false, opening: false});

/** One committed owner page. List cursors and selected revisions never cross a
 * query, tab, account, profile, or screen lifetime. */
export class NativePeopleSource {
  private state = initial;
  private readonly lifetime: NativeScreenLifetime;
  private readonly people: SettingsPeopleClient;
  private readonly invitations: PeopleInvitationsClient;
  private readonly listeners = new Set<() => void>();
  private listFlight?: AbortController;
  private personFlight?: AbortController;
  private deviceFlight?: AbortController;
  private libraryFlight?: AbortController;
  private deviceCursors = new Set<string>();
  private mutation?: object;
  private deletion?: MemberDeletionPreview;
  constructor(readonly context: NativeShellBootstrapContext, private readonly operationId: () => string, private readonly reportError: (error: unknown) => void) {
    this.lifetime = new NativeScreenLifetime(context);
    const api = {request: <T,>(path: string, method?: string, body?: unknown, caller?: AbortSignal): Promise<T> => this.current()
      ? this.lifetime.request(signal => context.api.request<T>(path, method, body, signal), caller) : Promise.reject(screenScopeEnded())};
    this.people = new SettingsPeopleClient(api, this.current);
    this.invitations = new PeopleInvitationsClient(api);
    this.api = api;
  }
  private readonly api: {request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>};
  /** Existing account/invitation controls remain owner-only. Administrators
   * reach People to manage member profiles through its separate tiered source. */
  ownerControls = () => nativeStorageOwner(this.context);
  current = () => {
    const {account} = this.context, authority = this.context.authority();
    const administrator = account.account.role === 'admin' && !account.account.disabled && account.serverId === authority.serverId
      && account.account.id === authority.accountId && account.account.primaryProfileId === authority.profileId;
    return this.lifetime.current() && (this.ownerControls() || administrator) && account.capabilities.managePeople;
  };
  getSnapshot = (): Snapshot => this.current() ? this.state : initial;
  subscribe = (listener: () => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  private publish(patch: Partial<Snapshot>) {if (this.current()) {this.state = Object.freeze({...this.state, ...patch}); for (const listener of this.listeners) listener();}}
  private fail(error: unknown) {
    if (!this.current()) return;
    const status = (error as {status?: number} | null)?.status;
    if (status === 401 || status === 403) {this.dispose(); this.reportError(error);} else this.publish({error});
  }
  select(tab: Tab, query = this.state.query, invitationStatus = this.state.invitationStatus) {
    if (!this.current() || this.state.busy) return;
    this.listFlight?.abort(); this.personFlight?.abort(); this.deviceFlight?.abort(); this.deletion = undefined;
    this.publish({tab, query, invitationStatus, cursor: '', people: undefined, invitations: undefined, person: undefined, selected: undefined, opening: false, devices: undefined, devicesLoading: false, devicesError: undefined, invitationOpening: undefined, invitationLink: undefined, error: undefined});
    void this.load();
  }
  load = async (cursor = ''): Promise<void> => {
    if (!this.current() || this.state.busy) return;
    this.listFlight?.abort(); const flight = this.listFlight = new AbortController();
    this.personFlight?.abort(); this.personFlight = undefined;
    const {tab, query, invitationStatus} = this.state;
    const owns = () => this.current() && this.listFlight === flight && !flight.signal.aborted;
    // A refreshed row has a new identity. Retire an unfinished detail sheet
    // before replacing that row, so its late reply cannot leave it loading.
    this.publish({loading: true, opening: false, error: undefined, ...(this.state.opening ? {selected: undefined, person: undefined} : {})});
    try {
      if (tab === 'people') {
        const people = await this.people.read(query, cursor, flight.signal);
        if (owns()) {
          const selected = this.state.selected ? people.items.find(row => row.accountId === this.state.selected!.accountId) : undefined;
          this.publish({people, cursor, selected, ...(!selected ? {person: undefined} : {})});
        }
      } else {
        const invitations = await this.invitations.list({q: query.search, ...(invitationStatus ? {state: invitationStatus} : {}), sort: 'sent', direction: 'desc', limit: 40, cursor}, flight.signal);
        if (owns()) this.publish({invitations, cursor});
      }
    } catch (error) {if (owns()) this.fail(error);}
    finally {if (owns()) this.publish({loading: false}); if (this.listFlight === flight) this.listFlight = undefined;}
  };
  open = async (row: SettingsPersonRow): Promise<void> => {
    if (!this.current() || this.state.busy || this.state.loading || !this.state.people?.items.includes(row)) return;
    this.deviceFlight?.abort(); this.personFlight?.abort(); const flight = this.personFlight = new AbortController(); this.deletion = undefined;
    const owns = () => this.current() && this.personFlight === flight && !flight.signal.aborted && this.state.selected === row;
    this.publish({selected: row, person: undefined, opening: true, devices: undefined, devicesLoading: false, devicesError: undefined, error: undefined});
    try {const person = await this.people.person(row.accountId, flight.signal); if (owns()) this.publish({person});}
    catch (error) {if (owns()) this.fail(error);}
    finally {if (owns()) this.publish({opening: false}); if (this.personFlight === flight) this.personFlight = undefined;}
  };
  closePerson = () => {if (this.state.busy) return; this.deviceFlight?.abort(); this.personFlight?.abort(); this.personFlight = undefined; this.deletion = undefined; this.publish({selected: undefined, person: undefined, opening: false, devices: undefined, devicesLoading: false, devicesError: undefined});};
  private owns(person: AccessMember) {return this.ownerControls() && this.current() && !this.state.busy && !this.state.loading && this.state.person === person && person.role !== 'owner' && person.accountId !== this.context.account.account.id;}
  private async change(person: AccessMember, action: () => Promise<unknown>): Promise<boolean> {
    if (!this.owns(person)) return false;
    const token = this.mutation = {}; this.deletion = undefined; this.publish({busy: true, error: undefined});
    const owns = () => this.current() && this.mutation === token && this.state.person === person;
    let saved = false;
    try {
      await action(); if (!owns()) return false;
      const fresh = await this.people.person(person.accountId); if (!owns()) return false;
      this.deviceFlight?.abort();
      this.publish({person: fresh, devices: undefined, devicesLoading: false, devicesError: undefined}); saved = true; return true;
    } catch (error) {if (owns()) this.fail(error); return false;}
    finally {if (this.current() && this.mutation === token) {this.mutation = undefined; this.publish({busy: false}); if (saved) void this.load(this.state.cursor);}}
  }
  loadLibraries = async (): Promise<void> => {
    if (!this.current() || this.state.librariesLoading) return;
    const flight = this.libraryFlight = new AbortController(); this.publish({librariesLoading: true, librariesError: undefined});
    const owns = () => this.current() && this.libraryFlight === flight && !flight.signal.aborted;
    try {const page = parseLibraryList(await this.api.request(routePath('get_libraries'), 'GET', undefined, flight.signal)); if (owns()) this.publish({libraries: page.items});}
    catch (error) {if (owns()) {this.publish({librariesError: error}); if ([401, 403].includes((error as {status: number})?.status)) this.fail(error);}}
    finally {if (owns()) this.publish({librariesLoading: false}); if (this.libraryFlight === flight) this.libraryFlight = undefined;}
  };
  access = (person: AccessMember, allowedLibraries: readonly string[]) => this.change(person, () => {
    const row = this.state.selected;
    if (!row || row.accountId !== person.accountId) throw screenScopeEnded();
    return this.people.bulk([{...row, revision: person.revision, limitsRevision: person.limitsRevision}], 'access', {allowedLibraries: [...allowedLibraries]}, this.operationId());
  });
  loadDevices = async (person: AccessMember, more = false): Promise<void> => {
    if (!this.current() || this.state.person !== person || this.state.busy || this.state.devicesLoading) return;
    const previous = more && this.state.devices?.person === person ? this.state.devices : undefined;
    if (more && !previous?.cursor) return;
    if (!more) this.deviceCursors.clear();
    const cursor = previous?.cursor ?? '', flight = this.deviceFlight = new AbortController();
    const owns = () => this.current() && this.state.person === person && this.deviceFlight === flight && !flight.signal.aborted;
    this.publish({devicesLoading: true, devicesError: undefined, ...(!more ? {devices: undefined} : {})});
    try {
      const params = new URLSearchParams({account: person.accountId, limit: '40', ...(cursor ? {cursor} : {})});
      const page = parseAccessDevicePage(await this.api.request(routePath('get_admin_access_devices') + '?' + params, 'GET', undefined, flight.signal));
      if (!owns()) return;
      if (page.items.some(device => device.accountId !== person.accountId)) throw new Error('native_people_device_scope_mismatch');
      const items = [...(previous?.items ?? [])], seen = new Set(items.map(device => device.id));
      for (const device of page.items) {if (seen.has(device.id)) throw new Error('native_people_duplicate_device'); seen.add(device.id); items.push(device);}
      if (cursor) this.deviceCursors.add(cursor);
      if (page.nextCursor && (page.nextCursor === cursor || this.deviceCursors.has(page.nextCursor))) throw new Error('native_people_device_cursor_loop');
      this.publish({devices: {person, items, cursor: page.nextCursor}});
    } catch (error) {if (owns()) {this.publish({devicesError: error}); if ([401, 403].includes((error as {status: number})?.status)) this.fail(error);}}
    finally {if (owns()) this.publish({devicesLoading: false}); if (this.deviceFlight === flight) this.deviceFlight = undefined;}
  };
  signOutDevices = async (person: AccessMember, device?: AccessDevice): Promise<boolean> => {
    if (!this.owns(person) || this.state.devicesLoading || this.state.devices?.person !== person
      || device && (!this.state.devices.items.includes(device) || device.accountId !== person.accountId)) return false;
    const token = this.mutation = {}; this.publish({busy: true, devicesError: undefined});
    let saved = false;
    try {
      if (device) await signOutMemberDevice(this.api, person.accountId, device.id);
      else await signOutMemberDevices(this.api, person.accountId);
      if (!this.current() || this.mutation !== token || this.state.person !== person) return false;
      saved = true; return true;
    } catch (error) {if (this.current() && this.mutation === token) {this.publish({devicesError: error}); if ([401, 403].includes((error as {status: number})?.status)) this.fail(error);} return false;}
    finally {if (this.current() && this.mutation === token) {this.mutation = undefined; this.publish({busy: false}); if (saved) void this.loadDevices(person);}}
  };
  beginInvitation = (invitation?: ServerInvitation) => {
    if (!this.ownerControls()) return;
    if (!this.current() || this.state.busy || this.state.loading || invitation && (!this.state.invitations?.items.includes(invitation) || invitation.state !== 'pending')) return;
    this.closePerson(); this.publish({invitationOpening: Object.freeze({invitation}), invitationLink: undefined, error: undefined});
  };
  closeInvitation = () => {if (!this.state.busy) {this.publish({invitationOpening: undefined, invitationLink: undefined});}};
  saveInvitation = async (opening: InvitationOpening, draft: InvitationDraft): Promise<boolean> => {
    if (!this.ownerControls()) return false;
    if (!this.current() || this.state.busy || this.state.loading || this.state.invitationOpening !== opening || ![1, 3, 7, 30].includes(draft.days) || !['member', 'admin'].includes(draft.role)) return false;
    const token = this.mutation = {}; this.publish({busy: true, error: undefined});
    const owns = () => this.current() && this.mutation === token && this.state.invitationOpening === opening;
    let saved = false;
    try {
      const common = {notes: draft.notes, role: draft.role, allowedLibraries: [...draft.allowedLibraries], operationId: this.operationId()};
      const invitation = opening.invitation ? await this.invitations.edit(opening.invitation.id, {...common, expectedRevision: opening.invitation.revision, expiresAt: opening.invitation.expiresAt})
        : await this.invitations.create({...common, expiresInHours: draft.days * 24});
      if (!owns()) return false;
      this.publish({invitationOpening: Object.freeze(opening.invitation ? {invitation} : {invitation, ready: 'created' as const}), invitationLink: undefined}); saved = true; return true;
    } catch (error) {if (owns()) this.fail(error); return false;}
    finally {if (this.current() && this.mutation === token) {this.mutation = undefined; this.publish({busy: false}); if (saved) void this.load(this.state.cursor);}}
  };
  /** A fresh operation ID, from the same generator this source's own commands use. */
  newOperationId = (): string => this.operationId();
  /** Opens the share card for a waiting invitation. */
  shareInvitation = (opening: InvitationOpening) => {
    if (!this.ownerControls()) return;
    if (!this.current() || this.state.busy || this.state.invitationOpening !== opening || opening.invitation?.state !== 'pending') return;
    this.publish({invitationOpening: Object.freeze({invitation: opening.invitation, ready: 'shared' as const}), invitationLink: undefined, error: undefined});
  };
  /** One short-code issuance for the card's own invitation; the issuer owns the operation ID. */
  invitationShortCode = async (opening: InvitationOpening, operationId: string, signal: AbortSignal) => {
    if (!this.ownerControls()) throw screenScopeEnded();
    const invitation = opening.invitation;
    if (!this.current() || this.state.invitationOpening !== opening || !opening.ready || invitation?.state !== 'pending') throw new DOMException('Invitation closed', 'AbortError');
    const result = await this.invitations.shortCode(invitation, operationId, signal);
    if (!this.current() || this.state.invitationOpening !== opening) throw new DOMException('Invitation closed', 'AbortError');
    return result;
  };
  invitationShareLink = async (opening: InvitationOpening): Promise<string | undefined> => {
    if (!this.ownerControls()) return;
    if (!this.current() || this.state.busy || this.state.invitationOpening !== opening || opening.invitation?.state !== 'pending') return;
    const token = this.mutation = {}; this.publish({busy: true, error: undefined});
    try {
      const receipt = await this.invitations.link(opening.invitation);
      if (!this.current() || this.mutation !== token || this.state.invitationOpening !== opening) return;
      const url = new URL('/join', this.context.api.baseUrl); url.hash = new URLSearchParams({code: receipt.code}).toString();
      this.publish({invitationLink: url.href}); return url.href;
    } catch (error) {if (this.current() && this.mutation === token) this.fail(error);}
    finally {if (this.current() && this.mutation === token) {this.mutation = undefined; this.publish({busy: false});}}
  };
  notes = (person: AccessMember, value: string) => this.change(person, () => this.people.notes(person, privatePersonNotes(value) ?? ''));
  role = (person: AccessMember, role: 'admin' | 'member') => this.change(person, () => this.people.role(person, role));
  limits = (person: AccessMember, limits: MemberLimits) => this.change(person, () => this.people.limits(person, limits));
  enabled = (person: AccessMember, enabled: boolean) => this.change(person, async () => {
    const row = this.state.selected;
    if (!row || row.accountId !== person.accountId) throw screenScopeEnded();
    await this.people.bulk([{...row, revision: person.revision, limitsRevision: person.limitsRevision}], enabled ? 'enable' : 'disable', undefined, this.operationId());
  });
  previewDeletion = async (person: AccessMember): Promise<MemberDeletionPreview | undefined> => {
    if (!this.owns(person)) return;
    const token = this.mutation = {}; this.publish({busy: true, error: undefined});
    try {
      const preview = await previewMemberDeletion(this.api, person.accountId);
      if (!this.current() || this.mutation !== token || this.state.person !== person || preview.role === 'owner') return;
      this.deletion = preview; return preview;
    } catch (error) {if (this.current() && this.mutation === token) this.fail(error);}
    finally {if (this.current() && this.mutation === token) {this.mutation = undefined; this.publish({busy: false});}}
  };
  remove = async (person: AccessMember, preview: MemberDeletionPreview): Promise<boolean> => {
    if (!this.owns(person) || this.deletion !== preview || preview.accountId !== person.accountId) return false;
    const token = this.mutation = {}; this.deletion = undefined; this.publish({busy: true, error: undefined});
    let removed = false;
    try {
      await deleteMember(this.api, preview);
      if (!this.current() || this.mutation !== token || this.state.person !== person) return false;
      this.publish({person: undefined, selected: undefined}); removed = true; return true;
    } catch (error) {if (this.current() && this.mutation === token) this.fail(error); return false;}
    finally {if (this.current() && this.mutation === token) {this.mutation = undefined; this.publish({busy: false}); if (removed) void this.load();}}
  };
  revoke = async (invitation: ServerInvitation): Promise<void> => {
    if (!this.ownerControls()) return;
    if (!this.current() || this.state.busy || this.state.loading || !this.state.invitations?.items.includes(invitation) || invitation.state !== 'pending') return;
    const token = this.mutation = {}; this.publish({busy: true, error: undefined});
    let revoked = false;
    try {await this.invitations.revoke(invitation, this.operationId()); revoked = true;}
    catch (error) {if (this.current() && this.mutation === token) this.fail(error);}
    finally {if (this.current() && this.mutation === token) {this.mutation = undefined; this.publish({busy: false}); if (revoked) void this.load(this.state.cursor);}}
  };
  dispose = () => {this.listFlight?.abort(); this.personFlight?.abort(); this.deviceFlight?.abort(); this.libraryFlight?.abort(); this.mutation = undefined; this.deletion = undefined; this.people.dispose(); this.lifetime.dispose(); this.state = initial; this.listeners.clear();};
}
