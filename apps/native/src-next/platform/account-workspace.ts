import {HttpLocalApi, parseServerPresence, type HostedServer} from '../../../../packages/client-core/src';
import {AccountSessionReviewService, type AccountSessionRow, type AccountSessionScope, type AccountSessionReviewSnapshot} from '../../../../packages/client-core/src/account-sessions';
import {AccountNotificationsClient, type AccountNotification} from '../../../../packages/client-core/src/account-notifications';
import {parseSecurityState, type SecurityState} from '../../../../packages/client-core/src/hosted-security';
import {presentError} from '../../../../packages/client-core/src/presentation/errors';
import {routePath} from '../../../../packages/contracts/src/hosted/routes';
import {leavePorticoServer} from '../../../../packages/client-core/src/portico-servers';
import {hostedUrl} from './account-authority';
import {nativeNetworkFetch} from './server-connections';
import type {NativeAccounts} from './accounts';
import {accountExpectation, nativeAccountRead, nativeAccountWork} from './account-work';
import {NativeAccountSecurity} from './account-security';

type NotificationView = {epoch: number; cursors: (string | undefined)[]; index: number; next?: string};
export type AccountWorkspacePage = 'menu' | 'security' | 'devices' | 'servers' | 'notifications';
export type AccountWorkspaceSnapshot = Readonly<{page: AccountWorkspacePage; phase: 'idle' | 'loading' | 'ready' | 'failed' | 'ended';
  failure?: string; mutationFailure?: string; notice?: string; pending?: string | null; endingOthers?: boolean; retrySession?: string | null; retryOthers?: boolean; notificationsNext?: boolean; notificationsPrevious?: boolean; sessionsNext?: boolean; sessionsPrevious?: boolean; security?: SecurityState; sessions?: readonly AccountSessionRow[]; servers?: readonly HostedServer[]; notifications?: readonly AccountNotification[]}>;
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
  private sessionReview?: {epoch: number; service: AccountSessionReviewService; currentRevoked: () => boolean};
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
  private retireReads() {this.notificationView = undefined; this.sessionReview?.service.dispose(); this.sessionReview = undefined; this.securityView?.dispose(); this.securityView = undefined; this.requests.forEach(request => request.abort()); this.requests.clear(); this.mutation = false;}
  menu() {this.assert(); ++this.epoch; this.retireReads(); this.publish({page: 'menu', phase: 'idle'});}
  private assertEpoch(epoch: number) {this.assert(); if (epoch !== this.epoch) throw changed();}
  private requestFor = (epoch: number, commands = false) => async <T,>(path: string, method = 'GET', body?: unknown, signal?: AbortSignal): Promise<T> => {
    this.assertEpoch(epoch); if (!commands && (method !== 'GET' || body !== undefined) || method === 'GET' && body !== undefined) throw changed();
    const request = new AbortController(), abort = () => request.abort(); this.requests.add(request);
    this.controller.signal.addEventListener('abort', abort, {once: true}); signal?.addEventListener('abort', abort, {once: true});
    if (signal?.aborted) abort(); const timer = setTimeout(abort, 15000);
    try {
      const original = this.credentials.getSnapshot().session;
      if (!original) throw changed();
      const work = method === 'GET' ? nativeAccountRead : nativeAccountWork;
      const result = await work(this.credentials, session =>
        new HttpLocalApi(hostedUrl, session.accessToken, nativeNetworkFetch).request<T>(path, method, body, request.signal),
        {expected: accountExpectation(original), signal: request.signal, isCurrent: () => this.current() && epoch === this.epoch});
      this.assertEpoch(epoch);
      if (epoch !== this.epoch || request.signal.aborted) throw changed(); return result;
    } finally {this.requests.delete(request); clearTimeout(timer); this.controller.signal.removeEventListener('abort', abort); signal?.removeEventListener('abort', abort);}
  };
  async open(page: Exclude<AccountWorkspacePage, 'menu'>) {
    this.assert(); const epoch = ++this.epoch; this.retireReads(); this.publish({page, phase: 'loading'});
    try {
      this.assertEpoch(epoch); const request = this.requestFor(epoch);
      let content: Pick<AccountWorkspaceSnapshot, 'security' | 'sessions' | 'servers' | 'notifications'>;
      if (page === 'security') content = {security: parseSecurityState(await request(routePath('account_security_get')), this.scope)};
      else if (page === 'servers') {
        const answer = await request<{items: HostedServer[]}>(routePath('servers_list'));
        if (!answer || !Array.isArray(answer.items) || answer.items.some(row => !row || typeof row.id !== 'string' || !row.id || typeof row.name !== 'string' || !row.name)) throw new Error('native_account_servers_invalid');
        content = {servers: Object.freeze(answer.items.map(row => Object.freeze({...row, presence: parseServerPresence(row.presence)})))};
      }
      else if (page === 'notifications') {
        const view: NotificationView = {epoch, cursors: [undefined], index: 0}; this.notificationView = view;
        await this.readNotifications(view, 0, undefined); return;
      }
      else {
        let currentRevoked = false;
        const commands = this.requestFor(epoch, true);
        const service = new AccountSessionReviewService({api: {request: async <T,>(path: string, method = 'GET', body?: unknown, signal?: AbortSignal): Promise<T> => {
          const result = await commands<T>(path, method, body, signal);
          if (method === 'DELETE' && result && typeof result === 'object') {
            const receipt = result as {id?: unknown; revoked?: unknown; current?: unknown};
            if (receipt.id === this.scope.familyId && receipt.revoked === true && receipt.current === true) currentRevoked = true;
          }
          return result;
        }}, scope: this.scope});
        const review = {epoch, service, currentRevoked: () => currentRevoked}; this.sessionReview = review;
        this.assertEpoch(epoch); await service.refresh(); this.assertEpoch(epoch);
        if (this.sessionReview !== review) throw changed();
        this.publish(this.sessionState(service.getSnapshot())); return;
      }
      this.assert(); if (epoch === this.epoch) this.publish({page, phase: 'ready', ...content});
    } catch (error) {if (this.current() && epoch === this.epoch) this.publish({page, phase: 'failed', failure: presentError(error, 'portico-account').body}); else if (!this.current()) this.end();}
  }
  private sessionState(value: AccountSessionReviewSnapshot): AccountWorkspaceSnapshot {
    return value.phase === 'ready' ? {page: 'devices', phase: 'ready', sessions: value.items,
      sessionsNext: !!value.nextCursor, sessionsPrevious: value.hasPrevious, pending: value.pending, endingOthers: value.endingOthers, retrySession: value.retryId, retryOthers: value.retryOthers,
      mutationFailure: value.mutationError?.message, notice: value.notice || undefined}
      : {page: 'devices', phase: 'failed', failure: value.error?.message ?? presentError(new Error('native_account_read_failed'), 'portico-account').body};
  }

  securityActions(): NativeAccountSecurity {
    this.assert(); const epoch = this.epoch, security = this.state.security;
    if (this.state.page !== 'security' || this.state.phase !== 'ready' || !security) throw changed();
    this.securityView?.dispose();
    return this.securityView = new NativeAccountSecurity(this.owner, this.scope, security,
      () => this.current() && this.epoch === epoch && this.state.page === 'security');
  }

  async revokeSession(row: AccountSessionRow | string) {
    this.assert(); const review = this.sessionReview;
    const familyId = typeof row === 'string' ? row : row.id, retry = review?.service.getSnapshot().retryId;
    if (this.mutation || !review || this.state.page !== 'devices' || this.state.phase !== 'ready'
      || (typeof row === 'string' ? row !== retry : !this.state.sessions?.includes(row) || row.status !== 'active' && row.id !== retry)) throw changed();
    const epoch = review.epoch; this.assertEpoch(epoch); this.mutation = true;
    this.publish({...this.state, pending: familyId, mutationFailure: undefined});
    try {
      const previous = review.service.getSnapshot();
      if (previous.retryId) {if (previous.retryId !== familyId) throw changed(); await review.service.retryRevoke();}
      else await review.service.revoke(familyId);
      this.assertEpoch(epoch); if (this.sessionReview !== review) throw changed();
      const result = review.service.getSnapshot();
      if (result.phase === 'ended') {
        if (review.currentRevoked()) {await this.owner.signOutPorticoAccount(this.scope); return;}
        // A command's issuing bearer may have expired and renewed. The command is
        // never replayed, and its old 401 must not sign out that live family.
        await review.service.refresh(); this.assertEpoch(epoch);
        this.publish({...this.sessionState(review.service.getSnapshot()), mutationFailure: presentError(new Error('native_account_session_outcome_unconfirmed'), 'portico-account').body}); return;
      }
      this.publish(this.sessionState(result));
    } finally {if (this.current() && this.epoch === epoch) {this.mutation = false; if (this.state.pending) this.publish({...this.state, pending: null});}}
  }

  /** Signs out every other sign-in of this Portico Account in one request; this device stays signed in.
   * An unconfirmed outcome is only ever retried as that same operation. */
  async revokeOtherSessions() {
    this.assert(); const review = this.sessionReview;
    if (this.mutation || !review || this.state.page !== 'devices' || this.state.phase !== 'ready') throw changed();
    const epoch = review.epoch; this.assertEpoch(epoch); this.mutation = true;
    this.publish({...this.state, endingOthers: true, mutationFailure: undefined, notice: undefined});
    try {
      if (review.service.getSnapshot().retryOthers) await review.service.retryRevoke(); else await review.service.revokeOthers();
      this.assertEpoch(epoch); if (this.sessionReview !== review) throw changed();
      const result = review.service.getSnapshot();
      if (result.phase === 'ended') {
        // This sign-in is never among the ones ended, so a 401 means its bearer lapsed or was
        // ended elsewhere. Re-read with the renewed session rather than signing it out.
        await review.service.refresh(); this.assertEpoch(epoch);
        this.publish({...this.sessionState(review.service.getSnapshot()), mutationFailure: presentError(new Error('native_account_session_outcome_unconfirmed'), 'portico-account').body}); return;
      }
      this.publish(this.sessionState(result));
    } finally {if (this.current() && this.epoch === epoch) {this.mutation = false; if (this.state.endingOthers) this.publish({...this.state, endingOthers: false});}}
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
  async pageSessions(direction: 'next' | 'previous') {
    this.assert(); const review = this.sessionReview;
    if (this.mutation || !review || this.state.page !== 'devices' || this.state.phase !== 'ready') return;
    const before = review.service.getSnapshot(), epoch = review.epoch;
    if (direction === 'next' ? !before.nextCursor : !before.hasPrevious) return;
    // H14: paging keeps the painted list until the next page lands.
    this.assertEpoch(epoch); this.publish({...this.state, page: 'devices', phase: 'loading'});
    try {
      this.assertEpoch(epoch);
      if (direction === 'next') await review.service.next(); else await review.service.previous();
      this.assertEpoch(epoch); if (this.sessionReview !== review) throw changed();
      this.publish(this.sessionState(review.service.getSnapshot()));
    } catch (error) {if (this.current() && epoch === this.epoch) this.publish({page: 'devices', phase: 'failed', failure: presentError(error, 'portico-account').body}); else if (!this.current()) this.end();}
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
