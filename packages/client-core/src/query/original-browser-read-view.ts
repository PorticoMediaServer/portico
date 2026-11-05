import {hasCapturedAccessToken} from '../session-authority.ts';
import {CredentialSessionService, type CredentialRecord, type HostedSession} from '../index.ts';
import {parseBrowserCredentialRecord, type BrowserCredentialAuthority} from '../credential-format.ts';
import {BrowserAccountApi} from './browser-account-api.ts';
import {HostedAccountClient} from '../hosted-security.ts';
type OriginalBrowserReadProof = {original: {hostedOrigin: string; session: HostedSession; coordinator: string}; withUIRead<T>(read: () => Promise<T>, signal: AbortSignal): Promise<T>};
type BrowserReadAccount = {api: BrowserAccountApi; service: CredentialSessionService<BrowserCredentialAuthority>; client: HostedAccountClient};

function ended(): never {throw new DOMException('The original Account Settings opening ended.', 'AbortError');}

/** A genuine finite view of the existing canonical account record. It can
 * render the original workspace, but cannot write, refresh, revoke or adopt a
 * session. The separately verified proof port owns the credential mutation. */
export async function createOriginalBrowserReadView(proof: OriginalBrowserReadProof, signal: AbortSignal): Promise<BrowserReadAccount> {
  const original = proof.original;
  class ReadAPI extends BrowserAccountApi {
    override send<T>(path: string, method: string, body?: unknown, input?: AbortSignal, token?: string): Promise<T> {
      const abort = input ? AbortSignal.any([signal, input]) : signal;
      if (method.toUpperCase() !== 'GET' || token !== undefined && !hasCapturedAccessToken(original.session, token)) return Promise.reject(new Error('The credential task must finish before another account action.'));
      return proof.withUIRead(() => super.send<T>(path, 'GET', body, abort, token), abort);
    }
  }
  const api = new ReadAPI(original.hostedOrigin);
  const service = new CredentialSessionService<BrowserCredentialAuthority>(api, {
    load: async (): Promise<CredentialRecord<BrowserCredentialAuthority>> => proof.withUIRead(async () => {
      const record = parseBrowserCredentialRecord(JSON.parse(original.coordinator));
      if (!record.active || record.active.pendingRequestId || record.active.session.account.id !== original.session.account.id || record.active.session.familyId !== original.session.familyId) ended();
      return {...record, active: {...record.active, session: {...original.session, account: {...original.session.account}}}};
    }, signal),
    save: async () => ended(),
  });
  const client = new HostedAccountClient(original.hostedOrigin, 'browser', (path, method, body, input, token) => api.send(path, method, body, input, token));
  await proof.withUIRead(() => service.synchronize(), signal);signal.throwIfAborted();
  return {api, service, client};
}

