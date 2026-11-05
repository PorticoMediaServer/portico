import {createOriginalBrowserReadView as hostedReadView} from '@core/query/original-browser-read-view.ts';
import {useLayoutEffect, useRef, useState, type ReactNode} from 'react';
import {CredentialSessionService, type CredentialRecord} from '@core/index.ts';
import {parseBrowserCredentialRecord, type BrowserCredentialAuthority} from '@core/credential-format.ts';
import {HostedAccountClient} from '@core/hosted-security.ts';
import {BrowserAccountApi} from '../platform/account';
import type {OriginalProviderSecurity} from '../platform/account-security';
import {OriginalAccountSecurityContext} from '../platform/account-security-context';
import {AccountPage} from './AccountPage';
import {ServicesAuthStage, HostedError} from '../kit/accounts/ServicesAuth';
import type {ServicesAccount} from './session';


/** The same Account Settings page remains behind its canonical credential
 * dialog. Only original existing access is restored, with no normal bootstrap
 * renewal or new sign-in grant. Closing creates a normal new page opening. */
export function OriginalAccountSecurityWorkspace({proof, children, onRetired}: {proof: OriginalProviderSecurity; children: ReactNode; onRetired: () => void}) {
  const retired = useRef(onRetired);retired.current=onRetired;
  const [page, setPage] = useState<ReactNode>(), [error, setError] = useState<unknown>();
  useLayoutEffect(() => {
    const life = new AbortController();
    const moved = () => {if (!life.signal.aborted && !proof.current()) retired.current();};
    addEventListener('popstate',moved);
    const current = () => !life.signal.aborted && proof.current();
    void (async () => {
      const account = await hostedReadView(proof, life.signal);
      if (current()) setPage(<AccountPage account={account} session={proof.original.session} section="security"/>);
    })().catch(value => {if (!life.signal.aborted) setError(value);});
    return () => {life.abort();removeEventListener('popstate',moved);};
  }, [proof]);
  return <OriginalAccountSecurityContext value={proof}>
    {page ?? <ServicesAuthStage>{error ? <HostedError error={error}/> : null}</ServicesAuthStage>}
    {children}
  </OriginalAccountSecurityContext>;
}
