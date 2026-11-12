/** Signing a Portico Account in to a server on the new server (`server-next/API.md`,
 * `POST /v1/auth/portico/challenge` and `/exchange`): the server's challenge, a sign-in note for it
 * from Portico Services, then the note back to the server, which checks it against the root it was
 * built with and answers with its own sign-in, exactly as for a password. */
import {callServer} from './query/server-api.ts';
import {CodedError} from './server-messages.ts';
import {envelopePayload} from './portico-account.ts';
import {openOnlyProfile, parseDirectSignIn, signInDevice, type DirectSignIn, type ProfileAPI} from './profile-management.ts';

/** What Portico Services answers for one server and challenge (`porticoAccount().serverIdentity`). */
export type SignInNote = Readonly<{identity: unknown; note?: unknown}>;

/** `api` is connected to the server already proved to hold `serverId`'s key. `noteFor` asks Portico
 * Services for the sign-in note. A note for a server that is behind is carried there afterwards; it
 * is Services' to send as well, so a failure to carry it never fails the sign-in. */
export async function porticoServerSignIn(api: ProfileAPI, serverId: string, noteFor: (challenge: string) => Promise<SignInNote>, signal?: AbortSignal,
  options: Readonly<{invitation?: string; accountId?: string; isCurrent?: () => boolean}> = {}): Promise<DirectSignIn> {
  return porticoNoteSignIn(api, serverId, noteFor, signal, options, (challenge, identity) => callServer(api, 'POST /v1/auth/portico/exchange',
    {body: {challenge, identity, device: signInDevice(), ...(options.invitation ? {invitation: options.invitation} : {})}, signal}));
}

/** The server's one-use challenge for a Portico Account sign-in (`POST /v1/auth/portico/challenge`),
 * checked to come from `serverId`. */
export async function askPorticoChallenge(api: ProfileAPI, serverId: string, signal?: AbortSignal): Promise<string> {
  const asked = await callServer(api, 'POST /v1/auth/portico/challenge', {signal});
  if (asked?.serverId !== serverId || typeof asked.challenge !== 'string' || !asked.challenge)
    throw new CodedError('server_mismatch', 'The server answered as a different server.');
  return asked.challenge;
}
/** Signing in at a server's own HTTPS page with a note web.getportico.tv sent back to it (option 2):
 * the challenge this page asked its server for before leaving, and the note Services made for it,
 * whose signed audience is this page's address. The server checks both; the note is used once. */
export async function porticoReturnedSignIn(api: ProfileAPI, serverId: string, challenge: string, identity: unknown, signal?: AbortSignal): Promise<DirectSignIn> {
  const signedIn = await openOnlyProfile(api, parseDirectSignIn(await callServer(api, 'POST /v1/auth/portico/exchange',
    {body: {challenge, identity, device: signInDevice()}, signal})), signal);
  if (signedIn.kind === 'signed-in' && signedIn.snapshot.serverId !== serverId) throw new CodedError('server_mismatch', 'The server answered as a different server.');
  return signedIn;
}

/** What finishes a waiting member's move to a Portico Account: the move code the owner gave them,
 * or the one-use ticket the server answered for their old password, which is typed only on the
 * server's own page and never travels (P2-66). */
export type PorticoMoveProof = Readonly<{code: string} | {ticket: string}>;
/** The server's own check of what a waiting member holds (`POST /v1/auth/move/check`), before
 * anything is asked of Portico Services: the owner's code, or the old username and password, which
 * the server answers with a ticket. */
export async function checkPorticoMove(api: ProfileAPI, held: Readonly<{code: string} | {username: string; password: string}>, signal?: AbortSignal): Promise<Readonly<{username: string; proof: PorticoMoveProof}>> {
  const answer = await callServer(api, 'POST /v1/auth/move/check', {body: {...held}, signal});
  if (answer?.next !== 'portico_account' || typeof answer.username !== 'string' || !answer.username)
    throw new CodedError('move_not_portico', 'This server is not moving its accounts to Portico Accounts.');
  const proof: PorticoMoveProof | undefined = 'code' in held ? {code: held.code} : typeof answer.ticket === 'string' && answer.ticket ? {ticket: answer.ticket} : undefined;
  if (!proof) throw new CodedError('invalid_response', 'The server did not answer with a move ticket.');
  return Object.freeze({username: answer.username, proof: Object.freeze(proof)});
}
/** Which waiting account a move ticket is for, read from the server without spending it
 * (`POST /v1/auth/move/ticket`), so the page finishing the move names the account from the server,
 * never from the link that brought the ticket. */
export async function peekPorticoMoveTicket(api: ProfileAPI, ticket: string, signal?: AbortSignal): Promise<Readonly<{username: string; expiresAt: string}>> {
  const answer = await callServer(api, 'POST /v1/auth/move/ticket', {body: {ticket}, signal});
  if (typeof answer?.username !== 'string' || !answer.username || typeof answer.expiresAt !== 'string')
    throw new CodedError('invalid_response', 'The server did not say which account this move is for.');
  return Object.freeze({username: answer.username, expiresAt: answer.expiresAt});
}
/** Moves the waiting member's account to the Portico Account: the server's challenge, the note for
 * it, and the proof with the note (`POST /v1/auth/move/complete`), whose answer is the sign-in. */
export async function porticoMove(api: ProfileAPI, serverId: string, proof: PorticoMoveProof, noteFor: (challenge: string) => Promise<SignInNote>, signal?: AbortSignal,
  options: Readonly<{accountId?: string; isCurrent?: () => boolean}> = {}): Promise<DirectSignIn> {
  return porticoNoteSignIn(api, serverId, noteFor, signal, options, (challenge, identity) => callServer(api, 'POST /v1/auth/move/complete',
    {body: {...proof, challenge, identity, device: signInDevice()}, signal}));
}

async function porticoNoteSignIn(api: ProfileAPI, serverId: string, noteFor: (challenge: string) => Promise<SignInNote>, signal: AbortSignal | undefined,
  options: Readonly<{accountId?: string; isCurrent?: () => boolean}>, finish: (challenge: string, identity: unknown) => Promise<unknown>): Promise<DirectSignIn> {
  // Nothing more goes to the server or to Services once the caller's sign-in is no longer the current one.
  const check = () => {if (options.isCurrent && !options.isCurrent()) throw new CodedError('cancelled', 'The Portico Account changed.');};
  check();
  const asked = await callServer(api, 'POST /v1/auth/portico/challenge', {signal});
  check();
  if (asked?.serverId !== serverId || typeof asked.challenge !== 'string' || !asked.challenge)
    throw new CodedError('server_mismatch', 'The server answered as a different server.');
  const answer = await noteFor(asked.challenge);
  check();
  // A page holding one account's sign-in sends only that account's note (another tab may have
  // signed in as someone else meanwhile).
  if (options.accountId !== undefined && notedAccount(answer.identity) !== options.accountId)
    throw new CodedError('account_changed', 'The Portico Account changed. Try again.');
  const signedIn = await openOnlyProfile(api, parseDirectSignIn(await finish(asked.challenge, answer.identity)), signal);
  if (signedIn.kind === 'signed-in' && signedIn.snapshot.serverId !== serverId) throw new CodedError('server_mismatch', 'The server answered as a different server.');
  if (answer.note !== undefined) void callServer(api, 'POST /v1/hosted/notices', {body: {note: answer.note}}).catch(() => undefined);
  return signedIn;
}

/** The account a sign-in note names, read for comparing only: the server checks the signature. */
export function notedAccount(identity: unknown): string | undefined {
  try { return envelopePayload<{accountId?: unknown}>(identity as {payload: string}).accountId as string | undefined; } catch { return undefined; }
}
