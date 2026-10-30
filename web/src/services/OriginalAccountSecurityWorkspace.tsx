import {useLayoutEffect, useRef, useState, type ReactNode} from 'react';
import {CredentialSessionService, type CredentialRecord} from '@core/index.ts';
import {parseBrowserCredentialRecord, type BrowserCredentialAuthority} from '@core/credential-format.ts';
import {HostedAccountClient} from '@core/hosted-security.ts';
import {selectedViewerRecord} from '@core/viewer-selection.ts';
import {BrowserAccountApi} from '../platform/account';
import type {OriginalProviderSecurity} from '../platform/account-security';
import {OriginalAccountSecurityContext} from '../platform/account-security-context';
import {AccountPage} from './AccountPage';
import {ServicesAuthStage, HostedError} from '../kit/accounts/ServicesAuth';
import type {ServicesAccount} from './session';

function ended(): never {throw new DOMException('The original Account Settings opening ended.', 'AbortError');}

/** A genuine finite view of the existing canonical account record. It can
 * render the original workspace, but cannot write, refresh, revoke or adopt a
 * session. The separately verified proof port owns the credential mutation. */
async function hostedReadView(proof: OriginalProviderSecurity, signal: AbortSignal): Promise<ServicesAccount> {
  const original = proof.original;
  class ReadAPI extends BrowserAccountApi {
    override send<T>(path: string, method: string, body?: unknown, input?: AbortSignal, token?: string): Promise<T> {
      const abort = input ? AbortSignal.any([signal, input]) : signal;
      if (method.toUpperCase() !== 'GET' || token !== undefined && token !== original.session.accessToken) return Promise.reject(new Error('The credential task must finish before another account action.'));
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

/** The same Account Settings page remains behind its canonical credential
 * dialog. Only original existing access is restored, with no normal bootstrap
 * renewal or new sign-in grant. Closing creates a normal new page opening. */
export function OriginalAccountSecurityWorkspace({proof, children, onRetired}: {proof: OriginalProviderSecurity; children: ReactNode; onRetired: () => void}) {
  const retired = useRef(onRetired);retired.current=onRetired;
  const [page, setPage] = useState<ReactNode>(), [error, setError] = useState<unknown>();
  useLayoutEffect(() => {
    const life = new AbortController(); let retire: (() => void) | undefined, offRoute: (() => void) | undefined;
    const moved = () => {if (!life.signal.aborted && !proof.current()) retired.current();};
    addEventListener('popstate',moved);
    const current = () => !life.signal.aborted && proof.current();
    void (async () => {
      if (!proof.original.server) {
        const account = await hostedReadView(proof, life.signal);
        if (current()) setPage(<AccountPage account={account} session={proof.original.session} section="security"/>);
        return;
      }
      const original = proof.original.server, origin = proof.pending.prepare?.origin;
      if (!origin) ended();
      const [app, shell, assembly, routes] = await Promise.all([import('../App'), import('../shell/bootstrap'), import('../shell/assembly'), import('../router')]);
      life.signal.throwIfAborted();
      const bootstrap = new shell.ShellBootstrap(assembly.assembleShellBindings, {
        selected: {read: async () => proof.withUIRead(async () => selectedViewerRecord(origin, original.session), life.signal)},
        restore: async expected => {
          if (JSON.stringify(expected) !== JSON.stringify(selectedViewerRecord(origin, original.session).session)) ended();
          return proof.openServerUI(life.signal);
        },
        readonlyAccess: true,
      });
      offRoute=routes.router.subscribe('onBeforeNavigate',event=>{if(!proof.acceptsUIURL(event.toLocation.href))retired.current();});
      retire = bootstrap.dispose;
      if (!current()) {retire();return;}
      setPage(<app.App bootstrap={bootstrap}/>);
    })().catch(value => {if (!life.signal.aborted) setError(value);});
    return () => {life.abort();removeEventListener('popstate',moved);offRoute?.();retire?.();};
  }, [proof]);
  return <OriginalAccountSecurityContext value={proof}>
    {page ?? <ServicesAuthStage>{error ? <HostedError error={error}/> : null}</ServicesAuthStage>}
    {children}
  </OriginalAccountSecurityContext>;
}
