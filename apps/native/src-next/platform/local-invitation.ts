import {serverNow} from '../../../../packages/client-core/src/server-clock';
import {ApiError} from '../../../../packages/client-core/src';
import type {PublicInvitation} from '../../../../packages/client-core/src/direct-account';
import {previewServerInvitation} from '../../../../packages/client-core/src/invitation-join';
import type {ProfileAPI} from '../../../../packages/client-core/src/profile-management';
import {parseShortInvitationCode, formatShortInvitationCode} from '../../../../packages/client-core/src/short-invitation-code';
import {routeOrigin} from '../../../../packages/client-core/src/route-identity';
import {parseJoinLink} from '../../../../packages/client-core/src/join-link';
import {passwordStrength} from '../../../../packages/client-core/src/password-strength';

export type Capability = Readonly<{code: string} | {shortCode: string}>;
/** `accept` joins and keeps the sign-in the server answers with; `signIn` is the ordinary password sign-in, for a
 * join that already succeeded on this page. */
type Ports<T> = {api: ProfileAPI; origin: string; serverId: string; current: () => boolean; signIn: (username: string, password: string) => Promise<T>;
  accept: (capability: Capability, username: string, password: string, name: string) => Promise<T>};
class InvitationExpired extends Error {}
const cancelled = () => Object.assign(new Error('invitation_retired'), {name: 'AbortError'});

/** A memory-only recipient for an already proved, local-sign-in server. */
export class NativeLocalInvitation<T> {
  static parseEntry(raw: string, address: string): Readonly<{raw: string; server: string; id?: string}> | undefined {
    const input = raw, link = parseJoinLink(input);
    if (link) return Object.freeze({raw: input, server: link.server, ...(link.id ? {id: link.id} : {})});
    const shortCode = parseShortInvitationCode(input);
    if (!shortCode) return undefined;
    try {return Object.freeze({raw: shortCode, server: routeOrigin(address.trim())});} catch {return undefined;}
  }
  static parseShortEntry(raw: string, address: string): Readonly<{raw: string; server: string}> | undefined {
    const code = parseShortInvitationCode(raw); if (!code) return undefined;
    try {return Object.freeze({raw: code, server: routeOrigin(address.trim())});} catch {return undefined;}
  }
  static formatCode(raw: string) {return formatShortInvitationCode(raw) ?? raw;}
  private readonly ports: Ports<T>;
  private readonly abort = new AbortController();
  private readonly guarded: ProfileAPI;
  private invitation?: {capability: Capability; preview: PublicInvitation};
  private accepted?: {preview: PublicInvitation; username: string};
  private shortEntry = false;
  private busy = false;
  constructor(ports: Ports<T>) {
    this.ports = ports;
    this.guarded = {request: async <R,>(path: string, method?: string, body?: unknown) => {
      this.assertCurrent();
      try {const result = await ports.api.request<R>(path, method, body, this.abort.signal); this.assertCurrent(); return result;}
      catch (error) {this.assertCurrent(); throw error;}
    }} as ProfileAPI;
  }
  current = () => !this.abort.signal.aborted;
  private assertCurrent() {if (!this.current() || !this.ports.current()) throw cancelled();}
  async preview(raw: string): Promise<PublicInvitation> {
    this.assertCurrent(); if (this.busy || this.accepted) throw cancelled();
    const input = raw, shortCode = parseShortInvitationCode(input), link = shortCode ? undefined : parseJoinLink(input);
    this.invitation = undefined; this.shortEntry = !!shortCode;
    if (!shortCode && (!link || link.server !== this.ports.origin || link.id !== undefined && link.id !== this.ports.serverId))
      throw Object.assign(new Error('invitation_incomplete'), {code: 'invitation_incomplete'});
    const capability: Capability = shortCode ? {shortCode} : {code: link!.code};
    this.busy = true; this.invitation = undefined;
    try {
      const preview = await previewServerInvitation(this.guarded, capability, this.abort.signal); this.assertCurrent();
      if (preview.signInMethod !== 'local') throw cancelled();
      if (Date.parse(preview.expiresAt) <= serverNow()) throw new InvitationExpired();
      this.invitation = {capability, preview}; return preview;
    } catch (error) {this.assertCurrent(); throw error;} finally {this.busy = false;}
  }
  async join(preview: PublicInvitation, username: string, password: string, name: string): Promise<T> {
    this.assertCurrent(); const invitation = this.invitation;
    if (this.busy || (!this.accepted && (!invitation || invitation.preview !== preview))) throw cancelled();
    if (!this.accepted && Date.parse(preview.expiresAt) <= serverNow()) throw new InvitationExpired();
    const user = username.trim(), label = name.trim();
    if (!/^[A-Za-z0-9_.-]{3,64}$/.test(user) || !passwordStrength(password).acceptable || [...label].length > 120) throw cancelled();
    if (this.accepted && (this.accepted.preview !== preview || this.accepted.username !== user)) throw cancelled();
    this.busy = true;
    try {
      this.assertCurrent();
      if (this.accepted) return await this.ports.signIn(user, password);
      // Joining is the sign-in: the account is made and this device signed in by the same answer, which the sign-in
      // command keeps. Sent again after a lost answer, the server signs in to the account the first one made.
      const signedIn = await this.ports.accept(invitation!.capability, user, password, label);
      this.accepted = {preview, username: user}; this.invitation = undefined;
      return signedIn;
    } catch (error) {this.assertCurrent(); throw error;} finally {this.busy = false;}
  }
  /** `mistyped`: a code the person typed that the server does not know (404 `invitation_not_found`, which is also its
   * answer for a used or ended code). They stay on the code form to check it; a code opened from a link is `expired`. */
  failure(error: unknown): 'expired' | 'username' | 'unavailable' | 'mistyped' | undefined {
    if (this.shortEntry && !this.accepted && error instanceof ApiError && error.status === 404 && error.code === 'invitation_not_found') return 'mistyped';
    if (this.shortEntry && !this.accepted && (error instanceof InvitationExpired || error instanceof ApiError && (
      error.status === 410 && error.code === 'invitation_expired'
      || error.status === 403 && error.code === 'invitation_for_another_account'))) return 'unavailable';
    // The rebuilt server answers a used, ended or unknown code with 404 `invitation_not_found`.
    if (error instanceof InvitationExpired || error instanceof ApiError && (error.status === 410 && error.code === 'invitation_expired' || error.status === 404 && error.code === 'invitation_not_found')) return 'expired';
    if (error instanceof ApiError && error.status === 409 && error.code === 'username_taken') return 'username';
    return undefined;
  }
  dispose() {this.abort.abort(); this.invitation = undefined; this.accepted = undefined;}
}
