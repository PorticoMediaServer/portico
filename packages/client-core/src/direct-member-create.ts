import {callServer} from './query/server-api.ts';
import type {ProfileAPI} from './profile-management.ts';

/** A new account on a local-sign-in server (owner-people frames 6 and 25, server-people "Add account"):
 * its username, the first profile's name, its password, and the libraries it can see (empty: all,
 * including ones added later). The server makes one profile with the name. */
export type NewDirectMember = Readonly<{username: string; name: string; password: string; allowedLibraries: readonly string[]}>;
export type CreatedDirectMember = Readonly<{id: string; username: string}>;

/** The server's own rules (identity CreateDirectMember), so the form never sends what it would refuse. Lengths are
 * UTF-8 bytes, as the server measures them: a username trimmed and lower-cased to 3 to 64 bytes with no space, tab,
 * line break or @; a trimmed profile name of 1 to 120 bytes with no control characters; a password of at least 8
 * characters and at most 72 bytes. */
const bytes = (value: string) => new TextEncoder().encode(value).length;
export function directMemberUsername(value: string): string | undefined {
  const username = value.trim().toLowerCase(), size = bytes(username);
  return size >= 3 && size <= 64 && !/[ \t\r\n@]/.test(username) ? username : undefined;
}
export function directMemberName(value: string): string | undefined {
  const name = value.trim();
  // eslint-disable-next-line no-control-regex
  return name && bytes(name) <= 120 && !/[\u0000-\u001f\u007f]/.test(name) ? name : undefined;
}
export function directMemberInputReady(input: NewDirectMember): boolean {
  return !!directMemberUsername(input.username) && !!directMemberName(input.name) && Array.from(input.password).length >= 8 && bytes(input.password) <= 72;
}

/** Creates the account, then limits it to its libraries when not all of them. Only the owner of a local-sign-in
 * server may; anything else is the server's refusal. */
export async function createDirectMember(api: ProfileAPI, input: NewDirectMember, signal?: AbortSignal): Promise<CreatedDirectMember> {
  const username = directMemberUsername(input.username);
  if (!username || !directMemberInputReady(input)) throw Object.assign(new Error('The new account is incomplete.'), {code: 'invalid_request'});
  const raw = await callServer(api, 'POST /v1/admin/members', {body: {username, profileName: directMemberName(input.name)!, password: input.password}, signal});
  if (typeof raw?.id !== 'string' || !raw.id || typeof raw.username !== 'string') throw new Error('The new account response was invalid.');
  if (input.allowedLibraries.length) await callServer(api, 'PATCH /v1/admin/members/{id}', {id: raw.id, body: {allLibraries: false, libraries: [...input.allowedLibraries]}, signal});
  return Object.freeze({id: raw.id, username: raw.username});
}

/** What a refused create means for the form: a taken username (409 `username_taken`) goes under the username; anything else is the form's one error line. */
export function directMemberCreateFailure(error: unknown): 'taken' | 'failed' {
  const code = error && typeof error === 'object' ? (error as {code?: unknown}).code : undefined;
  return code === 'username_taken' ? 'taken' : 'failed';
}
