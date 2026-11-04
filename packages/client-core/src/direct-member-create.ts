import {routeBody} from '../../contracts/src/routes.ts';
import {routePath} from '../../contracts/src/route-paths.ts';
import type {ProfileAPI} from './profile-management.ts';

/** A new account on a local-sign-in server (owner-people frames 6 and 25, server-people "Add account"):
 * its username, the first profile's name, its password, and the libraries it can see (empty: all,
 * including ones added later). The server makes one profile with the name. */
export type NewDirectMember = Readonly<{username: string; name: string; password: string; allowedLibraries: readonly string[]}>;
export type CreatedDirectMember = Readonly<{id: string; username: string}>;

/** The server's own rules, so the form never sends what it would refuse: 3 to 64 characters with no
 * spaces or @ (compared lower-cased and trimmed), a name, and a password of 8 to 72 bytes. */
export function directMemberUsername(value: string): string | undefined {
  const username = value.trim().toLowerCase();
  return username.length >= 3 && username.length <= 64 && !/[\s@]/.test(username) ? username : undefined;
}
export function directMemberInputReady(input: NewDirectMember): boolean {
  const bytes = new TextEncoder().encode(input.password).length;
  return !!directMemberUsername(input.username) && !!input.name.trim() && Array.from(input.password).length >= 8 && bytes <= 72;
}

/** Creates the account. Only the owner of a local-sign-in server may; anything else is the server's refusal. */
export async function createDirectMember(api: ProfileAPI, input: NewDirectMember, signal?: AbortSignal): Promise<CreatedDirectMember> {
  const username = directMemberUsername(input.username);
  if (!username || !directMemberInputReady(input)) throw Object.assign(new Error('The new account is incomplete.'), {code: 'invalid_request'});
  const raw = await api.request<{id?: unknown; username?: unknown}>(routePath('post_direct_members'), 'POST',
    routeBody('post_direct_members', {username, name: input.name.trim(), password: input.password, allowedLibraries: [...input.allowedLibraries]}), signal);
  if (typeof raw?.id !== 'string' || !raw.id || typeof raw.username !== 'string') throw new Error('The new account response was invalid.');
  return Object.freeze({id: raw.id, username: raw.username});
}

/** What a refused create means for the form: a taken username (409 conflict) goes under the username; anything else is the form's one error line. */
export function directMemberCreateFailure(error: unknown): 'taken' | 'failed' {
  const code = error && typeof error === 'object' ? (error as {code?: unknown}).code : undefined;
  return code === 'conflict' ? 'taken' : 'failed';
}
