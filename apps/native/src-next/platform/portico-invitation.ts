import {ApiError} from '../../../../packages/client-core/src';
import {PeopleInvitationsClient, type PublicInvitation} from '../../../../packages/client-core/src/direct-account';
import type {ProfileAPI} from '../../../../packages/client-core/src/profile-management';
import {parseJoinLink} from '../../../../packages/client-core/src/join-link';
import {parseShortInvitationCode} from '../../../../packages/client-core/src/short-invitation-code';

export type NativeInvitationEntry = Readonly<{raw: string; server: string; id: string; fingerprint: string; signIn: 'portico'; expiresAt: string}>;
export type NativeInvitationCapability = Readonly<{code: string} | {shortCode: string}>;
type Ports<T> = {
  api: ProfileAPI; origin: string; serverId: string; fingerprint: string; username: string; scope: Readonly<{accountId: string; familyId: string}>; expiresAt?: string; current: () => boolean;
  accept: (capability: NativeInvitationCapability) => Promise<T>;
};
class InvitationExpired extends Error {}
const cancelled = () => Object.assign(new Error('invitation_retired'), {name: 'AbortError'});

export class NativeInvitationUnavailable extends Error {}

/** Public, proved-server preview for the signed-out entry. No Hosted API or token. */
export async function readPorticoInvitationPreview(api: ProfileAPI, raw: string, target: Readonly<{origin: string; id: string; fingerprint: string}>,
  signal: AbortSignal, current: () => boolean): Promise<Readonly<{preview: PublicInvitation; entry: NativeInvitationEntry}>> {
  const check = () => {if (signal.aborted || !current()) throw cancelled();};
  check();
  const shortCode = parseShortInvitationCode(raw), link = shortCode ? undefined : parseJoinLink(raw);
  if (!shortCode && (!link || link.server !== target.origin || link.id !== undefined && link.id !== target.id)) throw cancelled();
  const client = new PeopleInvitationsClient({request: async <R,>(path: string, method?: string, body?: unknown) => {
    check(); try {const value = await api.request<R>(path, method, body, signal); check(); return value;}
    catch (error) {check(); throw error;}
  }});
  try {
    const preview = shortCode ? await client.previewShort(shortCode, signal) : await client.preview(link!.code, signal);
    check(); if (preview.signInMethod !== 'portico') throw cancelled();
    if (Date.parse(preview.expiresAt) <= Date.now()) throw new InvitationExpired();
    return Object.freeze({preview, entry: Object.freeze({raw, server: target.origin, id: target.id, fingerprint: target.fingerprint,
      signIn: 'portico' as const, expiresAt: preview.expiresAt})});
  } catch (error) {
    check();
    if (shortCode && (error instanceof InvitationExpired || error instanceof ApiError && (
      error.status === 410 && error.code === 'invitation_expired'
      || error.status === 403 && error.code === 'invitation_for_another_account'))) throw new NativeInvitationUnavailable();
    throw error;
  }
}

/** The anonymous preview grants nothing; only the canonical assertion/accept command joins. */
export class NativePorticoInvitation<T> {
  readonly username: string;
  private readonly ports: Ports<T>;
  private readonly abort = new AbortController();
  private readonly client: PeopleInvitationsClient;
  private invitation?: {raw: string; capability: NativeInvitationCapability; preview: PublicInvitation};
  private shortEntry = false;
  private busy = false;
  constructor(ports: Ports<T>) {
    this.ports = ports; this.username = ports.username;
    this.client = new PeopleInvitationsClient({request: async <R,>(path: string, method?: string, body?: unknown) => {
      this.assertCurrent();
      try {const result = await ports.api.request<R>(path, method, body, this.abort.signal); this.assertCurrent(); return result;}
      catch (error) {this.assertCurrent(); throw error;}
    }});
  }
  current = () => !this.abort.signal.aborted;
  private assertCurrent() {if (!this.current() || !this.ports.current()) throw cancelled();}
  async preview(raw: string): Promise<PublicInvitation> {
    this.assertCurrent(); if (this.busy) throw cancelled();
    const input = raw, shortCode = parseShortInvitationCode(input), link = shortCode ? undefined : parseJoinLink(input);
    this.invitation = undefined; this.shortEntry = !!shortCode;
    if (!shortCode && (!link || link.server !== this.ports.origin || link.id !== undefined && link.id !== this.ports.serverId))
      throw Object.assign(new Error('invitation_incomplete'), {code: 'invitation_incomplete'});
    const capability: NativeInvitationCapability = shortCode ? {shortCode} : {code: link!.code};
    this.busy = true; this.invitation = undefined;
    try {
      const preview = 'shortCode' in capability ? await this.client.previewShort(capability.shortCode, this.abort.signal) : await this.client.preview(capability.code, this.abort.signal);
      this.assertCurrent(); if (preview.signInMethod !== 'portico') throw cancelled();
      if (Date.parse(preview.expiresAt) <= Date.now() || this.ports.expiresAt && Date.parse(this.ports.expiresAt) <= Date.now()) throw new InvitationExpired();
      this.invitation = {raw: input, capability, preview}; return preview;
    } catch (error) {this.assertCurrent(); throw error;} finally {this.busy = false;}
  }
  async join(preview: PublicInvitation): Promise<T> {
    this.assertCurrent(); const invitation = this.invitation;
    if (this.busy || !invitation || invitation.preview !== preview) throw cancelled();
    if (Date.parse(preview.expiresAt) <= Date.now() || this.ports.expiresAt && Date.parse(this.ports.expiresAt) <= Date.now()) throw new InvitationExpired();
    this.busy = true;
    try {return await this.ports.accept(Object.freeze({...invitation.capability}));}
    catch (error) {this.assertCurrent(); throw error;} finally {this.busy = false;}
  }
  captureAccountSwitch(): Readonly<{entry: NativeInvitationEntry; scope: Readonly<{accountId: string; familyId: string}>; username: string}> {
    this.assertCurrent(); const invitation = this.invitation;
    if (this.busy || !invitation) throw cancelled();
    return Object.freeze({entry: Object.freeze({raw: invitation.raw, server: this.ports.origin, id: this.ports.serverId,
      fingerprint: this.ports.fingerprint, signIn: 'portico' as const, expiresAt: this.ports.expiresAt ?? invitation.preview.expiresAt}),
      scope: Object.freeze({...this.ports.scope}), username: this.username});
  }
  failure(error: unknown): 'expired' | 'other-account' | 'unavailable' | undefined {
    if (this.shortEntry && (error instanceof InvitationExpired || error instanceof ApiError && (
      error.status === 410 && error.code === 'invitation_expired'
      || error.status === 403 && error.code === 'invitation_for_another_account'))) return 'unavailable';
    if (error instanceof InvitationExpired || error instanceof ApiError && error.status === 410 && error.code === 'invitation_expired') return 'expired';
    if (error instanceof ApiError && error.status === 403 && error.code === 'invitation_for_another_account') return 'other-account';
    return undefined;
  }
  dispose() {this.abort.abort(); this.invitation = undefined;}
}
