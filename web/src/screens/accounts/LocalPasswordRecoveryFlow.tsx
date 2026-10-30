import {useCallback, useEffect, useLayoutEffect, useId, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import {passwordStrength} from '@core/password-strength.ts';
import {ownerResetFailure, type OwnerResetFailure, type ResetFileLocation} from '@core/account-reset.ts';
import type {BrowserAccountEntry} from '../../platform/account-entry';
import type {AccountSignInForm} from './sign-in-form';
import {AuthError, AuthLinks, AuthPasswordField, AuthStage, LocalPasswordRecovery} from '../../kit/accounts';
import {ServicesStrength} from '../../kit/accounts/ServicesAuth';
import {Button, Switch, TextField} from '../../kit/controls';
import authStyles from '../../kit/accounts/accounts.module.css';
import styles from './local-password-recovery.module.css';
import {OwnerResetNotice} from './OwnerResetNotice';
import {OwnerResetRewriteButton} from './OwnerResetRewriteButton';

const t = defaultI18n.t;
export type LocalPasswordRecoveryFlowProps = Readonly<{
  entry: BrowserAccountEntry; form: AccountSignInForm; onBack: () => void;
}>;

/** Local proof stays in the inspected entry. This view never handles a grant,
 * saves a code, constructs an API, or admits an authenticated session itself. */
type RecoveryOpening = {live:boolean;pending:boolean;ownerPending:boolean;readonly onBack:()=>void};
type FileOffer = {readonly opening:RecoveryOpening;readonly location:ResetFileLocation;readonly id:number;live:boolean};
export type OwnerResetNoticeKind=OwnerResetFailure;
export {ownerResetFailure as ownerResetNotice};
export function LocalPasswordRecoveryFlow({entry, form, onBack}: LocalPasswordRecoveryFlowProps) {
 const state=useSyncExternalStore(entry.subscribe,entry.getSnapshot),input=useSyncExternalStore(form.subscribe,form.getSnapshot);
 const captured=useRef({entry,origin:state.origin,serverId:state.server?.id,pin:state.pin}).current;
 const active=useRef<RecoveryOpening|undefined>(undefined),offered=useRef<FileOffer|undefined>(undefined),sequence=useRef(0);
 const [opening,setOpening]=useState<RecoveryOpening>(),[file,setFile]=useState<FileOffer>(),[owner,setOwner]=useState(input.username),[working,setWorking]=useState(false);
 const [error,setError]=useState<unknown>(),[purpose,setPurpose]=useState<'request'|'verify'>();
 const sameEntry=useCallback(()=>{
  const current=entry.getSnapshot();
  return captured.entry===entry&&captured.origin===current.origin&&captured.serverId===current.server?.id&&Boolean(captured.pin)&&captured.pin===current.pin&&current.server?.signIn==='local'&&!current.invitation;
 },[captured,entry]);
 // Publish only at commit, then sample once for this setup's original lifetime.
  const setupPorts = useRef({onBack});
  useLayoutEffect(() => {setupPorts.current = {onBack};});
  useLayoutEffect(()=>{
    const {onBack} = setupPorts.current;
  const held:RecoveryOpening={live:true,pending:false,ownerPending:false,onBack};active.current=held;setOpening(held);setWorking(false);setFile(undefined);offered.current=undefined;setError(undefined);setPurpose(undefined);
  return()=>{
   held.live=false;if(active.current===held)active.current=undefined;
   if(offered.current?.opening===held){offered.current.live=false;offered.current=undefined;}
   if(sameEntry()&&(held.ownerPending||entry.getSnapshot().phase==='owner-reset-password'))entry.cancelLocalOwnerReset();
  };
 },[entry,sameEntry]);
 const current=()=>!!opening&&opening.live&&active.current===opening&&sameEntry();
 const available=()=>current()&&!opening!.pending&&!entry.getSnapshot().busy&&!form.getSnapshot().busy;
 const ownsFile=(offer:FileOffer)=>current()&&offer.live&&offer.opening===opening&&offered.current===offer;
 const begin=(ownerWork:boolean)=>{if(!available())return false;opening!.pending=true;opening!.ownerPending=ownerWork;setWorking(true);setError(undefined);return true;};
 const finish=()=>{if(!opening)return;opening.pending=false;opening.ownerPending=false;if(current())setWorking(false);};
 const back=()=>{
  if(!current())return;opening!.live=false;active.current=undefined;
  if(offered.current){offered.current.live=false;offered.current=undefined;}
  entry.cancelLocalOwnerReset();form.clear();opening!.onBack();
 };
 const requestFile=async(username:string,prior?:FileOffer)=>{
  if(!available()||entry.getSnapshot().phase!=='password'||!username.trim()||(prior?!ownsFile(prior):!!offered.current))return;
  if(!begin(true))return;setPurpose('request');
  // Reissuing spends the old offer before IO, including uncertain responses.
  // A retained old Verify/Rewrite can never operate on its replacement file.
  if(prior){prior.live=false;offered.current=undefined;setFile(undefined);}
  try{
   const location=await entry.requestLocalResetFile(username);
   if(current()&&location){const next:FileOffer={opening:opening!,location,id:++sequence.current,live:true};offered.current=next;setOwner(username);setFile(next);}
  }catch(failure){if(current())setError(failure);}
  finally{finish();}
 };
 const verifyFile=async(offer:FileOffer,code:string)=>{
  if(!ownsFile(offer)||!available()||entry.getSnapshot().phase!=='password'||!begin(true))return;
  setPurpose('verify');
  try{await entry.verifyLocalOwnerReset(code);}catch(failure){if(ownsFile(offer))setError(failure);}finally{finish();}
 };
 const resetMember=async(username:string,code:string,password:string)=>{
  if(!available()||offered.current||entry.getSnapshot().phase!=='password'||!begin(false))return;
  setPurpose(undefined);
  try{await form.resetMemberPassword(username,code,password);}catch(failure){if(current())setError(failure);}finally{finish();}
 };
 const finishOwner=async(password:string,turnOffTwoStep:boolean)=>{
  if(!available()||entry.getSnapshot().phase!=='owner-reset-password'||!entry.getSnapshot().ownerReset)return;
  const details=entry.getSnapshot().ownerReset!;if(!begin(true))return;setPurpose(undefined);
  try{const finished=await entry.finishLocalOwnerResetPassword(password,details.twoStepEnabled&&turnOffTwoStep);
   if(finished&&current())await form.signIn(details.username,password);
  }catch(failure){if(current())setError(failure);}finally{finish();}
 };
 if(!current()||state.phase!=='password'&&state.phase!=='owner-reset-password')return null;
 const busy=working||state.busy||input.busy,failure=state.error??error,notice=ownerResetFailure(failure,purpose);
 if(state.phase==='owner-reset-password'&&state.ownerReset)return <OwnerPassword serverName={state.server!.name} username={state.ownerReset.username} twoStepEnabled={state.ownerReset.twoStepEnabled} busy={busy} error={failure} onSubmit={finishOwner} onBack={back}/>;
 if(file&&ownsFile(file))return <OwnerFile key={file.id} serverName={state.server!.name} location={file.location} busy={busy} error={failure} notice={notice}
  onVerify={code=>verifyFile(file,code)} onRewrite={()=>{void requestFile(owner,file);}} onBack={back}/>;
 return <LocalPasswordRecovery serverName={state.server!.name} initialUsername={input.username} busy={busy} error={notice?undefined:failure} notice={notice?<OwnerResetNotice kind={notice}/>:undefined}
  onWriteFile={username=>{void requestFile(username);}} onMemberReset={(username,code,password)=>{void resetMember(username,code,password);}} onBack={back}/>;
}

export function OwnerFile({serverName, location, busy, error, notice, onVerify, onRewrite, onBack}: {
 serverName:string;location:ResetFileLocation;busy:boolean;error:unknown;notice?:OwnerResetNoticeKind;
 onVerify:(code:string)=>Promise<void>;onRewrite:()=>void;onBack:()=>void;
}){
 const [code,setCode]=useState(''),[deadline,setDeadline]=useState(()=>Date.now()+location.retryAfterSeconds*1000),[remaining,setRemaining]=useState(location.retryAfterSeconds);
 useLayoutEffect(()=>{setDeadline(Date.now()+location.retryAfterSeconds*1000);},[location]);
 useLayoutEffect(()=>{
  if(notice!=='limited')return;
  const retry=error&&typeof error==='object'?(error as {retryAfterSeconds?:unknown}).retryAfterSeconds:undefined;
  if(typeof retry==='number'&&Number.isFinite(retry)&&retry>0)setDeadline(prior=>Math.max(prior,Date.now()+retry*1000));
 },[error,notice]);
 useEffect(()=>{
  let timer:ReturnType<typeof setTimeout>|undefined,live=true;
  const tick=()=>{if(!live)return;const left=Math.max(0,Math.ceil((deadline-Date.now())/1000));setRemaining(left);if(left>0)timer=setTimeout(tick,Math.min(1000,Math.max(1,deadline-Date.now())));};tick();
  return()=>{live=false;if(timer!==undefined)clearTimeout(timer);};
 },[deadline]);
 const separator=location.systemKind==='windows'?'\\':'/';
 const path=`${location.fileDirectory}${/[\\/]$/.test(location.fileDirectory)?'':separator}${location.fileName}`;
 const terminal=notice==='expired'||notice==='limited';
 return <AuthStage shape="round" legal={false}>
  <p className={authStyles.eyebrow}>{serverName}</p><h1>{t('accounts.recovery.fileTitle')}</h1>
  <p className={authStyles.lede}>{t('accounts.recovery.fileBody')}</p>{notice?<OwnerResetNotice kind={notice}/>:<AuthError error={error}/>}
  {terminal?<OwnerResetRewriteButton primary remainingSeconds={remaining} busy={busy} onRewrite={()=>{setCode('');onRewrite();}}/>:<>
   <div className={styles.path}><small>{t('accounts.recovery.fileLocation')}</small><code>{path}</code></div>
   <form className={authStyles.form} onSubmit={event=>{event.preventDefault();if(!busy&&code.trim()){const proof=code;setCode('');void onVerify(proof);}}}>
    <TextField variant="auth" label={t('accounts.recovery.code')} autoComplete="one-time-code" value={code} onChange={event=>setCode(event.currentTarget.value)} disabled={busy} autoFocus/>
    <div className={styles.actions}><Button variant="primary" type="submit" disabled={!code.trim()} loading={busy}>{t('action.continue')}</Button>
     <OwnerResetRewriteButton remainingSeconds={remaining} busy={busy} onRewrite={()=>{setCode('');onRewrite();}}/></div>
   </form>
  </>}
  <p className={styles.quiet}>{t('accounts.recovery.fileHelp')}</p><AuthLinks><button type="button" onClick={onBack}>{t('accounts.recovery.back')}</button></AuthLinks>
 </AuthStage>;
}

function OwnerPassword({serverName, username, twoStepEnabled, busy, error, onSubmit, onBack}: {
  serverName: string; username: string; twoStepEnabled: boolean; busy: boolean; error: unknown;
  onSubmit: (password: string, turnOffTwoStep: boolean) => Promise<void>; onBack: () => void;
}) {
  const strengthId = useId();
  const [password, setPassword] = useState(''), [confirm, setConfirm] = useState(''), [turnOff, setTurnOff] = useState(false);
  const ready = passwordStrength(password).acceptable && password === confirm;
  return <AuthStage shape="round" legal={false}>
    <p className={authStyles.eyebrow}>{serverName}</p><h1>{t('web.direct.newPasswordTitle')}</h1>
    <p className={authStyles.lede}>{t('accounts.recovery.ownerPasswordBody', {username})}</p><AuthError error={error}/>
    <form className={authStyles.form} onSubmit={event => {event.preventDefault(); if (!busy && ready) {const next = password; setPassword(''); setConfirm(''); void onSubmit(next, turnOff);}}}>
      <AuthPasswordField label={t('accounts.recovery.newPassword')} autoComplete="new-password" value={password} onChange={event => setPassword(event.currentTarget.value)} disabled={busy} autoFocus/>
      <ServicesStrength password={password} id={strengthId}/>
      <AuthPasswordField label={t('web.reset.confirm')} autoComplete="new-password" value={confirm} onChange={event => setConfirm(event.currentTarget.value)} disabled={busy}/>
      {twoStepEnabled && <label className={styles.choice}><span>{t('security.twoStep.turnOff')}<small>{t('accounts.recovery.turnOffHelp')}</small></span><Switch checked={turnOff} onCheckedChange={setTurnOff} disabled={busy}/></label>}
      <Button variant="primary" type="submit" disabled={!ready} loading={busy}>{t('accounts.recovery.ownerSubmit')}</Button>
    </form><p className={styles.quiet}>{t('accounts.recovery.ownerSignedOut')}</p><AuthLinks><button type="button" onClick={onBack}>{t('accounts.recovery.back')}</button></AuthLinks>
  </AuthStage>;
}
