import {PeopleInvitationsClient, type PeopleInvitationQuery, type ServerInvitation, type ServerShortInvitation} from '@core/direct-account.ts';
import {parseJoinLink} from '@core/join-link.ts';
import {CodedError} from '@core/server-messages.ts';
import type {RouteRequest} from '../../../../packages/contracts/src/routes.ts';
import type {SettingsPeopleApi} from '@core/settings-people.ts';
import type {TableQuery} from '../../kit/settings/table-model';

export type InvitationLifetime = Readonly<{signal: AbortSignal; isCurrent: () => boolean}>;
export function invitationQuery(query: TableQuery): PeopleInvitationQuery {
  const sort = query.sort.by, status = query.filters.status;
  if (sort !== 'sent' && sort !== 'status') throw new Error('invalid_invitation_query');
  let state: PeopleInvitationQuery['state'];
  if (status === 'waiting') state = 'pending';
  else if (status === 'used') state = 'accepted';
  else if (status === 'expired') state = 'expired';
  else if (status !== 'all') throw new Error('invalid_invitation_query');
  return {q: query.search, ...(state ? {state} : {}), sort, direction: query.sort.direction, limit: 40};
}
function shareLink(origin: string, serverId: string, code: string): string {
  const link = 'https://web.getportico.tv/join#' + new URLSearchParams({code, server: origin, id: serverId});
  const parsed = parseJoinLink(link);
  if (!parsed || parsed.server !== origin || parsed.id !== serverId || parsed.code !== code) throw new Error('invalid_invitation_link');
  return link;
}

/** One captured People authority. The producer owns filtering, counts and CAS; no list or code cache. */
export class SettingsInvitations {
  private readonly client: PeopleInvitationsClient;
  private readonly lifetime = new AbortController();
  constructor(api: SettingsPeopleApi, private readonly isCurrent: () => boolean, readonly origin: string, readonly serverId: string) {
    this.client = new PeopleInvitationsClient({request: async <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
      this.assertCurrent();signal?.throwIfAborted();
      try {
        const reply = await api.request<T>(path, method, body, signal);
        this.assertCurrent();signal?.throwIfAborted();return reply;
      } catch (error) {
        if (error && typeof error === 'object' && 'status' in error && (error.status === 401 || error.status === 403)) this.dispose();
        throw error;
      }
    }});
  }
  private assertCurrent() {if (this.lifetime.signal.aborted || !this.isCurrent()) throw new DOMException('Invitation authority retired', 'AbortError');}
  private checkInvitation(invitation: ServerInvitation) {
    if (invitation.serverId !== undefined && invitation.serverId !== this.serverId) throw new Error('invitation_server_changed');
    return invitation;
  }
  private async perform<T>(capture: InvitationLifetime, run: (signal: AbortSignal) => Promise<T>): Promise<T> {
    const request = new AbortController(), abort = () => request.abort();
    const signals = [capture.signal, this.lifetime.signal];
    for (const signal of signals) {signal.addEventListener('abort', abort, {once: true});if (signal.aborted) request.abort();}
    const check = () => {this.assertCurrent();request.signal.throwIfAborted();if (!capture.isCurrent()) throw new DOMException('Invitation selection retired', 'AbortError');};
    try {check();const result = await run(request.signal);check();return result;}
    finally {for (const signal of signals) signal.removeEventListener('abort', abort);}
  }
  read(query: TableQuery, cursor: string, capture: InvitationLifetime) {
    const selected = {...invitationQuery(query), ...(cursor ? {cursor} : {})};
    return this.perform(capture, async signal => {
      const page = await this.client.list(selected, signal);page.items.forEach(invitation => this.checkInvitation(invitation));return page;
    });
  }
  link(invitation: ServerInvitation, capture: InvitationLifetime): Promise<string> {
    return this.perform(capture, async signal => {
      this.checkInvitation(invitation);
      const link = await this.client.link(invitation, signal);
      if (link.serverId !== undefined && link.serverId !== this.serverId) throw new Error('invitation_server_changed');
      if (Date.parse(link.expiresAt) <= Date.now()) throw new CodedError('invitation_expired', 'This invitation is no longer usable.');
      return shareLink(this.origin, this.serverId, link.code);
    });
  }
  create(input: RouteRequest<'post_admin_access_invitations'>, capture: InvitationLifetime) {
    return this.perform(capture, async signal => this.checkInvitation(await this.client.create(input, signal)));
  }
  /** An explicit New code. The caller supplies a fresh operation ID for a new
   * issuance and the same one only to retry that exact request. A receipt
   * without a code is a replay: return the absence and never issue again. */
  shortCode(invitation: ServerInvitation, operationId: string, capture: InvitationLifetime): Promise<ServerShortInvitation> {
    if (!operationId) return Promise.reject(new Error('invitation_operation_required'));
    return this.perform(capture, async signal => {
      this.checkInvitation(invitation);
      const result = await this.client.shortCode(invitation, operationId, signal);
      if (result.serverId !== this.serverId) throw new Error('invitation_server_changed');
      if (Date.parse(result.expiresAt) <= Date.now()) throw new CodedError('invitation_expired', 'This invitation is no longer usable.');
      return result;
    });
  }
  edit(invitation: ServerInvitation, input: RouteRequest<'put_admin_access_invitation'>, capture: InvitationLifetime) {
    if (input.expectedRevision !== invitation.revision) return Promise.reject(new Error('invitation_revision_changed'));
    return this.perform(capture, async signal => {
      this.checkInvitation(invitation);
      const result = this.checkInvitation(await this.client.edit(invitation.id, input, signal));
      if (result.id !== invitation.id || result.revision <= invitation.revision) throw new Error('invitation_changed');
      return result;
    });
  }
  revoke(invitation: ServerInvitation, operationId: string, capture: InvitationLifetime) {
    return this.perform(capture, async signal => {
      this.checkInvitation(invitation);
      const result = this.checkInvitation(await this.client.revoke(invitation, operationId, signal));
      if (result.id !== invitation.id || result.revision <= invitation.revision || result.state !== 'revoked') throw new Error('invitation_changed');
      return result;
    });
  }
  dispose() {this.lifetime.abort();}
}
