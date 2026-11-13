import {useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {takeTwoStepSetup} from '../../../platform/two-step-continuation';
import {defaultI18n} from '@i18n';
import {IdentityClient, type TwoFactorEnrolment} from '@core/identity-client.ts';
import type {TwoFactorState} from '@core/identity-restrictions.ts';
import type {LocalSession} from '@core/index.ts';
import type {ProfileAPI} from '@core/profile-management.ts';
import {passwordStrength} from '@core/password-strength.ts';
import {Button, Switch, TextField} from '../../../kit/controls';
import {Dialog} from '../../../kit/overlays';
import {SettingsGroup} from '../../../kit/settings/SettingsGroups';
import {SettingsRow} from '../../../kit/shell/SettingsSurfaces';
import {AuthCodeBoxes} from '../../../kit/accounts';
import {HostedError, ServicesQuiet} from '../../../kit/accounts/ServicesAuth';
import {ServicesAuthenticator, ServicesQuietLink, ServicesRecoveryCodes} from '../../../kit/accounts/ServicesWorkspace';
import {browserAccountOrigin} from '../../../platform/account-origin';
import {accountSecurityUIURL} from '../../../platform/account-security-policy';
import {HostedSecurityAction} from './HostedSecurityAction';
import type {ProfileSettingsSource} from './source';
import {AccountPasswordFields} from './AccountPasswordFields';
import {AccountFactorProofFields} from './AccountFactorProofFields';
import styles from './profile.module.css';

export type ProfileSecurityFacts=Readonly<{mfa?:boolean;remaining?:number;email?:string}>;
const t=defaultI18n.t;
type Task='password'|'enrol'|'disable'|'codes';
type Opening={
  live:boolean;busy:boolean;readonly abort:AbortController;readonly current:()=>boolean;
  readonly local:IdentityClient;readonly accountId:string;readonly link:string;
};
type LocalIntent={readonly owner:Opening;readonly task:Task;live:boolean;enrolPassword?:string};
const ended=()=>new DOMException('The original account security opening ended.','AbortError');

type ProfileSignInProps={
  profiles:ProfileSettingsSource;api:ProfileAPI;commandApi:ProfileAPI;signIn:'local'|'portico';isCurrent:()=>boolean;onFacts?:(facts:ProfileSecurityFacts)=>void;session:()=>LocalSession;
};

/** Sign-in belongs to the account, not the selected editable profile. A Portico Account's password,
 * email and two-step are managed on its own Security page at web.getportico.tv, on every address. */
/** (Earlier note:) Sign-in belongs to the account, not the selected editable profile.
 * A Portico Account server's Sign-in is managed at the Portico Account itself (H09, Claude's product
 * call, 28 Oct): the server's own session never holds a Hosted session on this origin, so the section
 * shows what the server knows and opens the Portico Account's own Security page. Local servers are unchanged. */
export function ProfileSignIn(props:ProfileSignInProps){
 // At web.getportico.tv the Portico Account session is this page's own, so its password, email and
 // two-step are managed right here (22 Oct inline rule, P2-66). A server's own address has no
 // Portico Account session and links to the account instead (H09).
 const inline=props.signIn!=='portico';
 return inline?<AccountSecurity {...props}/>:<PorticoAccountSignIn {...props}/>;
}

/** This page is served from the Portico Account's own address. */
export function porticoAccountHere(page:Pick<Location,'origin'|'hostname'>=globalThis.location):boolean{
 try{return browserAccountOrigin(import.meta.env,page)===page.origin;}catch{return false;}
}

function PorticoAccountSignIn({profiles,isCurrent,session}:ProfileSignInProps){
 const document=useSyncExternalStore(profiles.subscribe,profiles.getSnapshot).document;
 const account=document?.account,viewer=session().viewer;
 // The same bindings as the security owner: the actual main-profile document, this server, this
 // account and profile in use, a manageable Portico Account member, and the original Settings lifetime.
 if(!document?.canManage||!document.capabilities.manageAccount||!account||account.disabled||!account.hostedAccountId||!isCurrent()
  ||viewer.serverId!==document.serverId||viewer.accountId!==account.id||viewer.authority!=='local'||viewer.profileId!==profiles.getSnapshot().inUseId)return null;
 const origin=browserAccountOrigin(import.meta.env,globalThis.location),page=`${origin}/account?tab=security`;
 const href=accountSecurityUIURL(origin,page)?page:undefined;
 return <SettingsGroup title={t('next.settingsSpec.account.profile.groups.5.title')}>
  <SettingsRow id="profile.porticoAccount" label={t('settings.row.porticoAccount')} help={<>{account.username}<br/>{t('web.signIn.managedThere')}</>}>
   <Button size="small" variant="glass" disabled={!href} onClick={()=>{if(href&&isCurrent())globalThis.open(href,'_blank','noopener,noreferrer');}}>{t('web.signIn.manage')}</Button>
  </SettingsRow>
 </SettingsGroup>;
}

function AccountSecurity({profiles,api,commandApi,signIn,isCurrent,onFacts,session}:ProfileSignInProps){
 const document=useSyncExternalStore(profiles.subscribe,profiles.getSnapshot).document;
 const account=document?.account;
 const scope=document?.canManage&&document.capabilities.manageAccount&&account&&!account.disabled?
  JSON.stringify([document.serverId,session().sessionFamilyId,account.id,account.role,account.hostedAccountId??'',profiles.getSnapshot().inUseId]):'';
 const active=useRef<Opening|undefined>(undefined),localIntentRef=useRef<LocalIntent|undefined>(undefined);
 const [opening,setOpening]=useState<Opening>(),[factor,setFactor]=useState<TwoFactorState>();
 const [task,setTask]=useState<Task>();
 const [enrolment,setEnrolment]=useState<TwoFactorEnrolment>(),[codes,setCodes]=useState<readonly string[]>();
 const [password,setPassword]=useState(''),[next,setNext]=useState(''),[code,setCode]=useState('');
 const [busy,setBusy]=useState(false),[error,setError]=useState<unknown>(),[retry,setRetry]=useState(0);
 const clearSecrets=()=>{setPassword('');setNext('');setCode('');};
 // Publish only at commit, then sample once for this setup's original lifetime.
  const setupPorts = useRef({account, document});
  useLayoutEffect(() => {setupPorts.current = {account, document};});
  useLayoutEffect(()=>{
    const {account, document} = setupPorts.current;
  if(localIntentRef.current){localIntentRef.current.live=false;localIntentRef.current.enrolPassword=undefined;}localIntentRef.current=undefined;
  setOpening(undefined);setFactor(undefined);setTask(undefined);setEnrolment(undefined);setCodes(undefined);clearSecrets();setBusy(false);setError(undefined);
  if(!scope||!account||!isCurrent())return;
  const familyId=session().sessionFamilyId;
  const expected={serverId:document!.serverId,familyId,accountId:account.id,role:account.role,link:account.hostedAccountId??'',profileId:profiles.getSnapshot().inUseId};
  const ownedSession=session();
  if(ownedSession.viewer.serverId!==expected.serverId||ownedSession.viewer.accountId!==expected.accountId||ownedSession.viewer.profileId!==expected.profileId||ownedSession.viewer.authority!=='local'||ownedSession.sessionFamilyId!==familyId)return;
  const abort=new AbortController();
  const held:Opening={live:true,busy:false,abort,accountId:expected.accountId,link:expected.link,
   current:()=>{
    if(!held.live||active.current!==held||abort.signal.aborted||!isCurrent())return false;
    const now=profiles.getSnapshot(),doc=now.document,principal=session();
    return principal.sessionFamilyId===expected.familyId&&principal.viewer.serverId===expected.serverId&&principal.viewer.accountId===expected.accountId&&principal.viewer.profileId===expected.profileId&&principal.viewer.authority==='local'&&!!doc&&doc.serverId===expected.serverId&&doc.canManage&&doc.capabilities.manageAccount&&!doc.account.disabled&&doc.account.id===expected.accountId&&
     doc.account.role===expected.role&&(doc.account.hostedAccountId??'')===expected.link&&now.inUseId===expected.profileId;
   },local:new IdentityClient({request:(path,method,body,input)=>{
    if(!held.current())throw ended();
    return commandApi.request(path,method,body,AbortSignal.any([abort.signal,...(input?[input]:[])]));
   }})};
  active.current=held;setOpening(held);
  void(async()=>{
   if(!held.current()||(signIn==='local')===Boolean(expected.link))throw ended();
   // A Portico Account's sign-in is managed on its own Security page (`PorticoAccountSignIn`).
   if(signIn!=='local')throw ended();
   const value=await new IdentityClient(api).twoFactor(abort.signal);
   if(held.current())setFactor(value);
  })().catch(failure=>{if(held.current())setError(failure);});
  return()=>{held.live=false;const intent=localIntentRef.current;if(intent?.owner===held){intent.live=false;intent.enrolPassword=undefined;localIntentRef.current=undefined;}abort.abort();if(active.current===held)active.current=undefined;};
 },[api,commandApi,profiles,scope,signIn,isCurrent,retry,session]);
 const held=opening&&active.current===opening&&opening.current()?opening:undefined;
 const intent=localIntentRef.current?.owner===held?localIntentRef.current:undefined;
 const intentCurrent=(value:LocalIntent|undefined)=>!!held&&!!value&&value.live&&value.owner===held&&localIntentRef.current===value&&held.current();
 const close=()=>{if(!held||held.busy||!intentCurrent(intent))return;intent!.live=false;intent!.enrolPassword=undefined;localIntentRef.current=undefined;setTask(undefined);setEnrolment(undefined);setCodes(undefined);clearSecrets();setError(undefined);};
 const open=(value:Task)=>{
  if(!held||held.busy||localIntentRef.current||codes)return;
  clearSecrets();setError(undefined);
  localIntentRef.current={owner:held,task:value,live:true};setTask(value);
 };
 const submit=async()=>{
  if(!held||!intent||!intentCurrent(intent)||held.busy||!task||intent.task!==task)return;
  const ownedTask=task,proof=password,newPassword=next,submittedCode=code,prior=enrolment;
  if(ownedTask==='password'&&(!proof||!passwordStrength(newPassword).acceptable||factor?.enabled&&!submittedCode.trim()))return;
  if(ownedTask==='enrol'?(prior?!intent.enrolPassword||submittedCode.length!==6:!proof):!proof||ownedTask!=='password'&&!submittedCode.trim())return;
  held.busy=true;setBusy(true);setError(undefined);clearSecrets();
  try{
   if(!intentCurrent(intent))throw ended();
   // A new password signs the account's other devices out and keeps this one; turning two-step off signs nobody out.
   if(ownedTask==='password'){
    await held.local.changePassword(proof,newPassword,submittedCode);
    if(!intentCurrent(intent))return;intent.live=false;localIntentRef.current=undefined;setTask(undefined);return;
   }
   if(ownedTask==='disable'){
    await held.local.disableTwoFactor(proof,submittedCode);
    if(!intentCurrent(intent))return;intent.live=false;localIntentRef.current=undefined;setTask(undefined);setFactor(Object.freeze({enabled:false,pendingEnrolment:false,recoveryCodesRemaining:0}));return;
   }
   if(ownedTask==='codes'){
    const fresh=await held.local.replaceRecoveryCodes(proof,submittedCode,held.abort.signal);
    if(!intentCurrent(intent))return;setCodes(fresh);setTask(undefined);
   }else if(!prior){
    const fresh=await held.local.enrolTwoFactor(proof);
    if(!intentCurrent(intent))return;intent.enrolPassword=proof;setEnrolment(fresh);
   }else{
    const originalPassword=intent.enrolPassword!;intent.enrolPassword=undefined;
    const fresh=await held.local.verifyTwoFactor(originalPassword,submittedCode);
    if(!intentCurrent(intent))return;setFactor(fresh);setCodes(fresh.recoveryCodes);setTask(undefined);setEnrolment(undefined);
   }
  }catch(failure){if(intentCurrent(intent)){if(ownedTask==='enrol'&&prior){intent.enrolPassword=undefined;setEnrolment(undefined);}setError(failure);}}
  finally{held.busy=false;if(held.current())setBusy(false);}
 };
 const mfa=factor?.enabled;
 const remaining=factor?.recoveryCodesRemaining;
 useLayoutEffect(()=>{if(held?.current())onFacts?.({mfa,remaining});},[held,mfa,remaining,onFacts]);
 // The owner chose two-step while switching this server to local sign-in: open
 // its setup once, here, for that same server and account, only while it is off.
 useLayoutEffect(()=>{if(signIn==='local'&&held?.current()&&factor&&!task&&document&&account&&takeTwoStepSetup(document.serverId,account.id)&&!factor.enabled)open('enrol');});
 const available=!!held&&!!factor;
 const guardField=(change:(value:string)=>void)=>(value:string)=>{if(intentCurrent(intent)&&!held!.busy)change(value);};
 const ready=!!held&&!busy&&(task==='password'?!!password&&passwordStrength(next).acceptable&&(!factor?.enabled||!!code.trim()):task==='enrol'?enrolment?!!intent?.enrolPassword&&code.length===6:!!password:!!password&&!!code.trim());
 const title=t(task==='password'?'account.changePasswordTitle':task==='disable'?'web.twoStep.turnOffConfirmTitle':task==='codes'||codes?'web.hostedSecurity.newCodesTitle':enrolment?'web.hostedSecurity.addToAuthenticator':'web.twoStep.turnOnTitle');
 if(!scope||!document?.canManage)return null;
 return <><SettingsGroup title={t('next.settingsSpec.account.profile.groups.5.title')}>
  {error&&!task&&!codes?<HostedError error={error}/>:null}
  {Boolean(error)&&!task&&<Button size="small" onClick={()=>{if(held?.current())setRetry(value=>value+1);}}>{t('next.states.tryAgain')}</Button>}
  <SettingsRow id="profile.password" label={t('next.settingsSpec.account.profile.groups.5.rows.1.label')}>
   <HostedSecurityAction label={t('next.settingsSpec.account.profile.groups.5.rows.1.control.label')} disabled={!available||busy} onOpen={()=>open('password')}/>
  </SettingsRow>
  <SettingsRow id="profile.twoStep" label={t('next.settingsSpec.account.profile.groups.5.rows.2.label')} help={t('next.settingsSpec.account.profile.groups.5.rows.2.help')}>
   {mfa===undefined?<span role="status">{t('web.claim.loading')}</span>:<Switch aria-label={t('next.settingsSpec.account.profile.groups.5.rows.2.label')} checked={mfa===true} disabled={!available||busy} onCheckedChange={()=>open(mfa?'disable':'enrol')}/>}
  </SettingsRow>
  {mfa&&<SettingsRow id="profile.recoveryCodes" dependent label={t('next.settingsSpec.account.profile.groups.5.rows.2.children.0.label')} help={remaining===undefined?undefined:t('web.hostedSecurity.codesLeft',{count:remaining})}>
   <HostedSecurityAction label={t('next.settingsSpec.account.profile.groups.5.rows.2.children.0.control.label')} disabled={!available||busy} onOpen={()=>open('codes')}/>
  </SettingsRow>}
  {held&&intentCurrent(intent)&&(task||codes)&&<Dialog footerDismisses open title={title} showCloseButton={false} onOpenChange={value=>{if(!value)close();}}
   footer={codes?<Button variant="primary" onClick={close}>{t('action.done')}</Button>:<><Button disabled={busy} onClick={close}>{t('action.cancel')}</Button><Button variant="primary" disabled={!ready} loading={busy} onClick={()=>void submit()}>{t(task==='password'?'account.changePasswordTitle':'action.continue')}</Button></>}>
   {error?<HostedError error={error}/>:null}
   {codes?<><ServicesQuiet>{t('services.security.codesBody')}</ServicesQuiet><ServicesRecoveryCodes codes={codes}
    onCopy={()=>{if(intentCurrent(intent))void navigator.clipboard.writeText(codes.join('\n')).catch(failure=>{if(intentCurrent(intent))setError(failure);});}}
    onDownload={()=>{if(!intentCurrent(intent))return;let url:string|undefined;try{url=URL.createObjectURL(new Blob([codes.join('\n')],{type:'text/plain;charset=utf-8'}));const link=globalThis.document.createElement('a');link.href=url;link.download='portico-recovery-codes.txt';if(intentCurrent(intent))link.click();}catch(failure){if(intentCurrent(intent))setError(failure);}finally{if(url)URL.revokeObjectURL(url);}}}/></>:
    task==='password'?<><ServicesQuiet>{t('web.hostedSecurity.othersSignedOut')}</ServicesQuiet><AccountPasswordFields currentPassword={password} newPassword={next} code={code} twoFactor={factor?.enabled===true} disabled={busy}
     onCurrentPasswordChange={guardField(setPassword)} onNewPasswordChange={guardField(setNext)} onCodeChange={guardField(setCode)}/></>:
    task==='enrol'?<div className={styles.fields}>{enrolment?<><ServicesQuiet>{t('web.hostedSecurity.enterKey')}</ServicesQuiet><ServicesAuthenticator secret={enrolment.secret} uri={enrolment.uri} onCopy={()=>{if(intentCurrent(intent))void navigator.clipboard.writeText(enrolment.secret).catch(failure=>{if(intentCurrent(intent))setError(failure);});}} onError={failure=>{if(intentCurrent(intent))setError(failure);}}/>
     <AuthCodeBoxes length={6} value={code} onChange={guardField(setCode)} onComplete={()=>{}} label={t('web.twoStep.code')} disabled={busy}/></>:
     <TextField type="password" autoComplete="current-password" label={t('account.currentPassword')} value={password} onChange={event=>guardField(setPassword)(event.currentTarget.value)} disabled={busy}/>}</div>:
    <><ServicesQuiet>{t(task==='disable'?'services.security.disableBody':'next.settingsSpec.account.profile.groups.5.rows.2.children.0.control.confirm')}</ServicesQuiet><AccountFactorProofFields password={password} code={code} disabled={busy} onPasswordChange={guardField(setPassword)} onCodeChange={guardField(setCode)}/></>}
  </Dialog>}
 </SettingsGroup>
 </>;
}
