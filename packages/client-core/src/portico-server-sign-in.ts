/** Signing a Portico Account in to a server on the new server (`server-next/API.md`,
 * `POST /v1/auth/portico/challenge` and `/exchange`): the server's challenge, a sign-in note for it
 * from Portico Services, then the note back to the server, which checks it against the root it was
 * built with and answers with its own sign-in, exactly as for a password. */
import {callServer} from './query/server-api.ts';
import {CodedError} from './server-messages.ts';
import {openOnlyProfile, parseDirectSignIn, signInDevice, type DirectSignIn, type ProfileAPI} from './profile-management.ts';

/** What Portico Services answers for one server and challenge (`porticoAccount().serverIdentity`). */
export type SignInNote = Readonly<{identity: unknown; note?: unknown}>;

/** `api` is connected to the server already proved to hold `serverId`'s key. `noteFor` asks Portico
 * Services for the sign-in note. A note for a server that is behind is carried there afterwards; it
 * is Services' to send as well, so a failure to carry it never fails the sign-in. */
export async function porticoServerSignIn(api: ProfileAPI, serverId: string, noteFor: (challenge: string) => Promise<SignInNote>, signal?: AbortSignal): Promise<DirectSignIn> {
  const asked = await callServer(api, 'POST /v1/auth/portico/challenge', {signal});
  if (asked?.serverId !== serverId || typeof asked.challenge !== 'string' || !asked.challenge)
    throw new CodedError('server_mismatch', 'The server answered as a different server.');
  const answer = await noteFor(asked.challenge);
  const signedIn = await openOnlyProfile(api, parseDirectSignIn(await callServer(api, 'POST /v1/auth/portico/exchange',
    {body: {challenge: asked.challenge, identity: answer.identity, device: signInDevice()}, signal})), signal);
  if (signedIn.kind === 'signed-in' && signedIn.snapshot.serverId !== serverId) throw new CodedError('server_mismatch', 'The server answered as a different server.');
  if (answer.note !== undefined) void callServer(api, 'POST /v1/hosted/notices', {body: {note: answer.note}}).catch(() => undefined);
  return signedIn;
}
