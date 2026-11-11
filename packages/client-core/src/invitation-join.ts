import type {InvitationPreview} from '../../contracts/src/next/api.ts';
import {parsePublicInvitation, type PublicInvitation} from './direct-account.ts';
import {openOnlyProfile, parseDirectSignIn, signInDevice, type DirectSignIn, type ProfileAPI} from './profile-management.ts';
import {callServer} from './query/server-api.ts';
import {formatShortInvitationCode} from './short-invitation-code.ts';
import {CodedError} from './server-messages.ts';

/** What the person holds: the code from a link, or the code they typed. On the rebuilt server both are the
 * invitation's own code (K7QP-2MXD-9HRT-4WNC). */
export type InvitationCapability = Readonly<{code: string} | {shortCode: string}>;

/** The invitation's code as the server reads it. Anything else is refused before it is sent. */
function invitationCode(capability: InvitationCapability): string {
  const code = formatShortInvitationCode('shortCode' in capability ? capability.shortCode : capability.code);
  if (!code) throw new CodedError('invitation_incomplete', 'This invitation code is not complete.');
  return code;
}

/** What the holder of a code sees before joining (`GET /v1/auth/invitations/{code}`): the server's name, the role
 * and the libraries shared. It never names who invited them. Joining the rebuilt server is always with a username
 * and password on that server. A used, ended or unknown code is the server's 404 `invitation_not_found`. */
export async function previewServerInvitation(api: ProfileAPI, capability: InvitationCapability, signal?: AbortSignal): Promise<PublicInvitation> {
  const raw: InvitationPreview = await callServer(api, 'GET /v1/auth/invitations/{id}', {id: invitationCode(capability), signal});
  return parsePublicInvitation({serverName: raw?.serverName, expiresAt: raw?.expiresAt, role: raw?.role, allLibraries: raw?.allLibraries,
    libraries: Array.isArray(raw?.libraries) ? raw.libraries.map(name => ({name})) : raw?.libraries, signInMethod: 'local'});
}

/** Joins: creates the person's account with the invitation and signs this device in to it
 * (`POST /v1/auth/invitations/{code}/accept`). The answer is the sign-in itself, kept as any password sign-in is;
 * an account with one profile and no PIN opens it. Sent again after a lost answer, the server signs in to the
 * account the first one made. A taken username is the server's 409 `username_taken`. */
export async function acceptServerInvitation(api: ProfileAPI, capability: InvitationCapability, account: Readonly<{username: string; password: string; profileName?: string}>, signal?: AbortSignal): Promise<DirectSignIn> {
  const profileName = account.profileName?.trim();
  const raw = await callServer(api, 'POST /v1/auth/invitations/{id}/accept', {id: invitationCode(capability), signal,
    body: {username: account.username.trim(), password: account.password, ...(profileName ? {profileName} : {}), device: signInDevice()}});
  return openOnlyProfile(api, parseDirectSignIn(raw), signal);
}
