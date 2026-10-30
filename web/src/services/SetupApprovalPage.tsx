import {useEffect, useLayoutEffect, useState, useSyncExternalStore, type ComponentType} from 'react';
import {goTo} from '../platform/hosted-navigation';
import {ApiError} from '@core/index.ts';
import type {ServicesAccount} from './session';
import type {ServicesBootstrap} from './bootstrap';
import {ServicesAuthStage,ServicesHeading,ServicesLead,ServicesActions,HostedError} from '../kit/accounts/ServicesAuth';
import {Button} from '../kit/controls';
import {servicesI18n} from './i18n';
import {servicesRelativeTime} from './time';
import {SetupApprovalFlow, type SetupApprovalSnapshot} from './setup-approval-flow';

/** Drawing stays with the frame lift. This adapter exposes authenticated display
 * facts and explicit actions, never a claim code, token or protected journal. */
export type SetupApprovalViewProps = Readonly<{
  phase: SetupApprovalSnapshot['phase']; serverName: string; address: string; accountName: string; requestedAt: string;
  approved: boolean; installed?: boolean; slow?: boolean; busy: boolean; error?: unknown; onApprove: () => void; onDeny: () => void; onReturn: () => void;
}>;
const empty: SetupApprovalSnapshot = Object.freeze({phase: 'loading', busy: false});
const emptySnapshot = () => empty;
const noSubscription = () => () => {};
type Instance = {scope: string; flow?: SetupApprovalFlow; error?: unknown};
export function SetupApprovalPage({account, bootstrap, Review}: {account: ServicesAccount; bootstrap: ServicesBootstrap; Review: ComponentType<SetupApprovalViewProps>}) {
  const session = useSyncExternalStore(account.service.subscribe, account.service.getSnapshot).session;
  const scope = session?.familyId ? JSON.stringify([session.account.id, session.familyId]) : '';
  const [instance, setInstance] = useState<Instance>();
  const active = instance?.scope === scope ? instance : undefined, flow = active?.flow;
  const state = useSyncExternalStore(flow?.subscribe ?? noSubscription, flow?.getSnapshot ?? emptySnapshot);
  useLayoutEffect(() => {
    if (!scope) return;
    let created: SetupApprovalFlow | undefined;
    try {
      const controller = bootstrap.setupApproval(account);
      if (!controller) throw new ApiError(409, 'claim_stale', 'claim_stale', false);
      created = new SetupApprovalFlow(account, controller, location => window.location.assign(location));
      setInstance({scope, flow: created}); void created.start();
    } catch (error) {setInstance({scope, error});}
    return () => created?.dispose();
  }, [account, bootstrap, scope]);
  useEffect(() => {
    if (!flow || !state.display) return;
    const timer = setTimeout(() => flow.expire(), Math.max(0, Date.parse(state.display.expiresAt) - Date.now()));
    return () => clearTimeout(timer);
  }, [flow, state.display]);
  const {t}=servicesI18n(), error=active?.error??state.error;
  if(!state.display){
    if(state.phase==='loading'&&!error)return <ServicesAuthStage><p role="status">{t('web.claim.loading')}</p></ServicesAuthStage>;
    return <ServicesAuthStage><ServicesHeading>{t('web.claim.titleGeneric')}</ServicesHeading>
      {state.phase==='expired'?<ServicesLead>{t('web.claim.expired')}</ServicesLead>:<HostedError error={error}/>}
      <ServicesActions><Button onClick={()=>goTo('/')}>{t('web.claim.backToPortico')}</Button></ServicesActions></ServicesAuthStage>;
  }
  const display=state.display, requested=Date.now()-Date.parse(display.requestedAt)<45000?t('services.claim.justNow'):servicesRelativeTime(servicesI18n(),display.requestedAt);
  return <Review phase={state.phase} serverName={display.name} address={new URL(display.address).host} accountName={display.accountName}
    requestedAt={requested} approved={state.approved === true} installed={state.installed === true} slow={state.slow === true} busy={state.busy || state.phase === 'returning'}
    error={error} onApprove={() => {void flow?.decide('approve');}} onDeny={() => {void flow?.decide('deny');}}
    onReturn={() => {if(state.approved)void flow?.complete();else flow?.returnReviewed();}} />;
}
