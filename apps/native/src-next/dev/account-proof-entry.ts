import {routeOrigin} from '../../../../packages/client-core/src/route-identity';

/** DEV launch input only. No credential, authority, runtime or native record is
 * accepted from a link. The person still signs in through the real product flow. */
export function accountProofFromURL(value: string | null): Readonly<{serverAddress: string}> | undefined {
  if (!value) return;
  try {
    const url = new URL(value);
    if (url.protocol !== 'portico:' || url.hostname !== 'account-proof' || !['', '/'].includes(url.pathname) ||
      url.username || url.password || url.port || url.hash) return;
    const fields = [...url.searchParams.entries()];
    if (fields.length !== 1 || fields[0][0] !== 'server') return;
    const address = fields[0][1]; routeOrigin(address);
    return Object.freeze({serverAddress: address});
  } catch {return;}
}
