import {createNativeAccountWorkspaceRequest} from '../../../../packages/client-core/src/query/hosted-account-transport';
import {readHostedServers} from '../../../../packages/client-core/src/query/connection-operations';
import {parseServerPresence, type HostedServer} from '../../../../packages/client-core/src';
import type {AccountSessionScope} from '../../../../packages/client-core/src/account-sessions';
import {porticoAccountSessions, readPorticoAccountSecurity, type PorticoSessionRow} from '../../../../packages/client-core/src/portico-services-native';
import {AccountNotificationsClient, type AccountNotification} from '../../../../packages/client-core/src/account-notifications';
import {type SecurityState} from '../../../../packages/client-core/src/hosted-security';
import {presentError} from '../../../../packages/client-core/src/presentation/errors';
import {leavePorticoServer} from '../../../../packages/client-core/src/portico-servers';
import {hostedUrl} from './account-authority';
import {nativeNetworkFetch} from './server-connections';
import type {NativeAccounts} from './accounts';
import {accountExpectation, nativeAccountRead, nativeAccountWork} from './account-work';
import {NativeAccountSecurity} from './account-security';

type NotificationView = {epoch: number; cursors: (string | undefined)[]; index: number; next?: string};
type SessionView = {epoch: number};
type Sessions = ReturnType<typeof porticoAccountSessions>;
export type AccountWorkspacePage = 'menu' | 'security' | 'devices' | 'servers' | 'notifications';
export type AccountWorkspaceSnapshot = Readonly<{page: AccountWorkspacePage; phase: 'idle' | 'loading' | 'ready' | 'failed' | 'ended';
  failure?: string; mutationFailure?: string; notice?: string; pending?: string | null; endingOthers?: boolean; notificationsNext?: boolean; notificationsPrevious?: boolean; security?: SecurityState; sessions?: readonly PorticoSessionRow[]; servers?: readonly HostedServer[]; notifications?: readonly AccountNotification[]}>;
const changed = () => Object.assign(new Error('native_account_changed'), {code: 'account_changed'});

/** Account-only workspace. No server authority or credentials enter its snapshot. */
export class NativeAccountWorkspace {
  private readonly owner: NativeAccounts;
  private readonly credentials: ReturnType<NativeAccounts['accountCredentials']>;
  private readonly isCurrent: () => boolean;
  private readonly requests = new Set<AbortController>();
  private readonly scope: AccountSessionScope;
  private readonly controller = new AbortController();
  private readonly listeners = new Set<() => void>();
  private readonly off: () => void;
  private notificationView?: NotificationView;
  private sessionView?: SessionView;
  private securityView?: NativeAccountSecurity;
  private mutation = false;
  private epoch = 0;
  private state: AccountWorkspaceSnapshot = {page: 'menu', phase: 'idle'};
  constructor(owner: NativeAccounts, scope: AccountSessionScope, isCurrent: () => boolean) {
    this.isCurrent = isCurrent; this.owner = owner;
    this.scope = Object.freeze({...scope}); this.credentials = owner.accountCredentials(); this.assert();
    this.off = this.credentials.subscribe(() => {if (!this.current()) this.end();});
  }
  getSnapshot = () => this.state;
  subscribe = (listener: () => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  current = () => {const live = this.credentials.getSnapshot().session;
    return !this.controller.signal.aborted && this.isCurrent() && live?.account.id === this.scope.accountId && live.familyId === this.scope.familyId;};
  private assert() {if (!this.current()) throw changed();}
  private publish(state: AccountWorkspaceSnapshot) {this.state = Object.freeze(state); this.listeners.forEach(listener => listener());}
  private end() {if (this.controller.signal.aborted) return; ++this.epoch; this.retireReads(); this.controller.abort(); this.publish({page: 'menu', phase: 'ended'});}
  dispose() {this.listeners.clear(); this.end(); this.off();}
  private retireReads() {this.notificationView = undefined; this.sessionView = undefined; this.securityView?.dispose(); this.securityView = undefined; this.requests.forEach(request => request.abort()); this.requests.clear(); this.mutation = false;}
  menu() {this.assert(); ++this.epoch; this.retireReads(); this.publish({page: 'menu', phase: 'idle'});}
  private assertEpoch(epoch: number) {this.assert(); if (epoch !== this.epoch) throw changed();}
  private requestFor = (epoch: number, commands = false) => createNativeAccountWorkspaceRequest(this.credentials, nativeNetworkFetch, {
    assertCurrent: () => this.assertEpoch(epoch), current: () => this.current() && epoch === this.epoch, sameEpoch: () => epoch === this.epoch,
    signal: this.controller.signal, requests: this.requests, changed,
  }, commands);
  async open(page: Exclude<AccountWorkspacePage, 'menu'>) {
    this.assert(); const epoch = ++this.epoch; this.retireReads(); this.publish({page, phase: 'loading'});
    try {
      this.assertEpoch(epoch); const request = this.requestFor(epoch);
      let content: Pick<AccountWorkspaceSnapshot, 'security' | 'sessions' | 'servers' | 'notifications'>;
      if (page === 'security') {
        const session = this.credentials.getSnapshot().session; if (!session) throw changed();
        const request = new AbortController(), timer = setTimeout(() => request.abort(), 15000); this.requests.add(request);
        try {
          content = {security: await nativeAccountRead(this.credentials,
            issued => readPorticoAccountSecurity(issued.accessToken, hostedUrl, this.scope, nativeNetworkFetch, request.signal),
            {expected: accountExpectation(session), signal: request.signal, isCurrent: () => this.current() && epoch === this.epoch})};
        } finally {clearTimeout(timer); this.requests.delete(request);}
      }
      else if (page === 'servers') {
        const answer = await readHostedServers<{items: HostedServer[]}>({request});
        if (!answer || !Array.isArray(answer.items) || answer.items.some(row => !row || typeof row.id !== 'string' || !row.id || typeof row.name !== 'string' || !row.name)) throw new Error('native_account_servers_invalid');
        content = {servers: Object.freeze(answer.items.map(row => Object.freeze({...row, presence: parseServerPresence(row.presence)})))};
      }
      else if (page === 'notifications') {
        const view: NotificationView = {epoch, cursors: [undefined], index: 0}; this.notificationView = view;
        await this.readNotifications(view, 0, undefined); return;
      }
      else {
        const view: SessionView = {epoch}; this.sessionView = view;
        this.publish(await this.sessionsState(view)); return;
      }
      this.assert(); if (epoch === this.epoch) this.publish({page, phase: 'ready', ...content});
    } catch (error) {if (this.current() && epoch === this.epoch) this.publish({page, phase: 'failed', failure: presentError(error, 'portico-account').body}); else if (!this.current()) this.end();}
  }
  /** One call to the account's sign-ins at Portico Services, renewed once on a 401 as every account
   * call is; a read is then made again, a command never. Retiring the page or 15 s ends it. */
  private sessionsWork<T>(view: SessionView, work: (sessions: Sessions, signal: AbortSignal) => Promise<T>, read: boolean): Promise<T> {
    const live = this.credentials.getSnapshot().session; if (!live) throw changed();
    const request = new AbortController(), timer = setTimeout(() => request.abort(), 15000); this.requests.add(request);
    const opening = {expected: accountExpectation(live), isCurrent: () => this.current() && this.epoch === view.epoch && this.sessionView === view, signal: request.signal};
    const call = (issued: {accessToken: string}) => work(porticoAccountSessions(issued.accessToken, hostedUrl, nativeNetworkFetch), request.signal);
    return (read ? nativeAccountRead(this.credentials, call, opening) : nativeAccountWork(this.credentials, call, opening))
      .finally(() => {clearTimeout(timer); this.requests.delete(request);});
  }
  private async sessionsState(view: SessionView): Promise<AccountWorkspaceSnapshot> {
    const sessions = await this.sessionsWork(view, (account, signal) => account.list(signal), true);
    this.assertEpoch(view.epoch); if (this.sessionView !== view) throw changed();
    return {page: 'devices', phase: 'ready', sessions};
  }

  securityActions(): NativeAccountSecurity {
    this.assert(); const epoch = this.epoch, security = this.state.security;
    if (this.state.page !== 'security' || this.state.phase !== 'ready' || !security) throw changed();
    this.securityView?.dispose();
    return this.securityView = new NativeAccountSecurity(this.owner, this.scope, security,
      () => this.current() && this.epoch === epoch && this.state.page === 'security');
  }

  /** Signs one other device out. Ending a sign-in that is already gone is not an error, so the list
   * is read again either way: a failure is shown only while that device is still listed. */
  async revokeSession(row: PorticoSessionRow) {
    this.assert(); const view = this.sessionView;
    if (this.mutation || !view || this.state.page !== 'devices' || this.state.phase !== 'ready' || !this.state.sessions?.includes(row) || row.current) throw changed();
    const epoch = view.epoch; this.assertEpoch(epoch); this.mutation = true;
    this.publish({...this.state, pending: row.id, mutationFailure: undefined});
    try {await this.settleSessions(view, (account, signal) => account.end(row.id, signal), sessions => sessions.some(value => value.id === row.id));}
    finally {if (this.current() && this.epoch === epoch) {this.mutation = false; if (this.state.pending) this.publish({...this.state, pending: null});}}
  }

  /** Signs out every other sign-in of this Portico Account in one request; this device stays signed in. */
  async revokeOtherSessions() {
    this.assert(); const view = this.sessionView;
    if (this.mutation || !view || this.state.page !== 'devices' || this.state.phase !== 'ready') throw changed();
    const epoch = view.epoch; this.assertEpoch(epoch); this.mutation = true;
    this.publish({...this.state, endingOthers: true, mutationFailure: undefined});
    try {await this.settleSessions(view, (account, signal) => account.endOthers(signal), sessions => sessions.some(value => !value.current));}
    finally {if (this.current() && this.epoch === epoch) {this.mutation = false; if (this.state.endingOthers) this.publish({...this.state, endingOthers: false});}}
  }
  private async settleSessions(view: SessionView, command: (account: Sessions, signal: AbortSignal) => Promise<void>, stillThere: (sessions: readonly PorticoSessionRow[]) => boolean) {
    let failure: unknown;
    try {await this.sessionsWork(view, command, false);} catch (error) {failure = error;}
    try {
      const next = await this.sessionsState(view);
      this.publish(failure !== undefined && stillThere(next.sessions ?? []) ? {...next, mutationFailure: presentError(failure, 'portico-account').body} : next);
    } catch (error) {
      if (this.current() && this.epoch === view.epoch) this.publish({...this.state, mutationFailure: presentError(failure ?? error, 'portico-account').body});
    }
  }

  async leaveServer(row: HostedServer) {
    this.assert(); const epoch = this.epoch;
    if (this.mutation || this.state.page !== 'servers' || this.state.phase !== 'ready'
      || !this.state.servers?.includes(row) || row.ownedByMe !== false || !['admin', 'member'].includes(row.role ?? '')) throw changed();
    this.mutation = true; this.publish({...this.state, pending: row.id, mutationFailure: undefined});
    try {
      await leavePorticoServer({request: this.requestFor(epoch, true)}, hostedUrl, row.id, this.controller.signal);
      this.assertEpoch(epoch); this.publish({...this.state, servers: this.state.servers?.filter(value => value.id !== row.id), pending: null});
    } catch (error) {
      if (this.current() && this.epoch === epoch) this.publish({...this.state, pending: null, mutationFailure: presentError(error, 'portico-account').body});
    } finally {if (this.current() && this.epoch === epoch) this.mutation = false;}
  }
  private async readNotifications(view: NotificationView, index: number, cursor: string | undefined) {
    this.assertEpoch(view.epoch); if (this.notificationView !== view) throw changed();
    const value = await new AccountNotificationsClient(this.requestFor(view.epoch)).page(cursor, this.controller.signal);
    this.assertEpoch(view.epoch); if (this.notificationView !== view) throw changed();
    view.index = index; view.cursors = view.cursors.slice(0, index + 1); view.cursors[index] = cursor; view.next = value.nextCursor;
    this.publish({page: 'notifications', phase: 'ready', notifications: value.items,
      notificationsPrevious: index > 0, notificationsNext: !!value.nextCursor});
  }
  async pageNotifications(direction: 'next' | 'previous') {
    this.assert(); const view = this.notificationView;
    if (!view || this.state.page !== 'notifications' || this.state.phase !== 'ready') return;
    if (direction === 'next' ? !view.next : view.index === 0) return;
    const epoch = view.epoch, index = view.index + (direction === 'next' ? 1 : -1);
    const cursor = direction === 'next' ? view.next : view.cursors[index];
    this.assertEpoch(epoch); this.publish({...this.state, page: 'notifications', phase: 'loading'});
    try {this.assertEpoch(epoch); await this.readNotifications(view, index, cursor);}
    catch (error) {if (this.current() && epoch === this.epoch) this.publish({page: 'notifications', phase: 'failed', failure: presentError(error, 'portico-account').body}); else if (!this.current()) this.end();}
  }

}
