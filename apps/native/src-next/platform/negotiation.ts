import {API_LEVEL_MISMATCH, APP_UPDATE_REQUIRED, negotiateServer, type ServerAPI, type ServerSignIn} from '../../../../packages/client-core/src/sign-in';

/** O-159, Justin's rule: if this app and a server can talk, they connect. The two refusals are
 * decided from the server's public /v1/system answer, before credentials or a stored session: a
 * pre-release server (or one whose majors are all older) needs an update; a server that only
 * speaks newer majors needs a newer app. The refusal carries the server's name for its page. */
export type NegotiationRefusal = Readonly<{kind: 'server' | 'app'; serverName?: string}>;

export function negotiateNativeServer(server: Pick<ServerSignIn, 'name' | 'apiLevel' | 'apiVersion' | 'apiMajors' | 'capabilities'>): ServerAPI {
  try {return negotiateServer(server);}
  catch (error) {throw Object.assign(error as Error, {serverName: server.name});}
}

export function negotiationRefusal(error: unknown): NegotiationRefusal | undefined {
  const value = error as {code?: unknown; serverName?: unknown} | null;
  const serverName = typeof value?.serverName === 'string' ? value.serverName : undefined;
  if (value?.code === API_LEVEL_MISMATCH) return {kind: 'server', serverName};
  if (value?.code === APP_UPDATE_REQUIRED) return {kind: 'app', serverName};
  return undefined;
}
