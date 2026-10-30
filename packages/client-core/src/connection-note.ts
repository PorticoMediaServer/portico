/**
 * The quiet line a signed-out screen shows when the server says this connection is plain HTTP
 * from outside the owner's home networks (`GET /v1/networking/connection`, read with
 * `HttpLocalApi.connection()`; open to anyone who can reach the server). The server alone decides
 * what counts as outside the home: the owner's home networks setting is the one definition, and
 * the app never guesses from an address. The line says where the owner turns on a secure
 * connection (Server settings › Remote access); there is no help page to send anyone to.
 */
import type {ConnectionDocument} from './server-administration.ts';

export interface ConnectionReader { connection(signal?: AbortSignal): Promise<ConnectionDocument> }

/**
 * Whether to show the line. It is only ever a note: any failure to find out (an older server, a
 * timeout, an answer that cannot be read) means no note, and nothing waits on it.
 */
export async function plainHttpOutsideHome(api: ConnectionReader, signal?: AbortSignal): Promise<boolean> {
  try {
    return (await api.connection(signal)).plainHttpOutsideHome === true;
  } catch {
    return false;
  }
}
