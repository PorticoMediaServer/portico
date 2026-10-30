import {getApiAccessOptions,type ApiAccessOptions,type ApiAccessTransport,type ApiScope} from '../api-access.ts';
import {createIntegrationAppPassword,listIntegrationAppPasswords,listIntegrationUsers,revealIntegrationAppPassword,revokeIntegrationAppPassword,type IntegrationAppPassword,type IntegrationPage,type IntegrationUser,type IssuedIntegrationAppPassword,type NewIntegrationAppPassword} from '../integration-access.ts';
import {randomId} from '../random-id.ts';

type Snapshot=Readonly<{phase:'idle'|'loading'|'ready'|'error';apps?:IntegrationPage<IntegrationAppPassword>;users?:IntegrationPage<IntegrationUser>;options?:ApiAccessOptions;busy:boolean;pendingCreate:boolean;error?:'read'|'create'|'revoke'|'reveal'}>;
const empty:Snapshot=Object.freeze({phase:'idle',busy:false,pendingCreate:false});
export type IntegrationAppDraft=Readonly<{name:string;accountId:string;profileId:string;scopes:readonly ApiScope[]}>;
/** Captures one owner/server authority. Secrets are returned to the caller,
 * never published in snapshots or retained across server/profile retirement. */
export class SettingsIntegrationsSource {
 private state=empty;private readonly lifetime=new AbortController();private readonly listeners=new Set<()=>void>();private work?:AbortController;private readonly reveals=new Set<AbortController>();private intent?:NewIntegrationAppPassword;
 constructor(private readonly api:ApiAccessTransport,private readonly isCurrent:()=>boolean,private readonly reportError:(e:unknown)=>void,private readonly operationId:()=>string=randomId){}
 private current=()=>!this.lifetime.signal.aborted&&this.isCurrent();
 getSnapshot=():Snapshot=>this.current()?this.state:empty;
 subscribe=(fn:()=>void)=>{this.listeners.add(fn);return()=>{this.listeners.delete(fn);};};
 private publish(p:Partial<Snapshot>){if(this.current()){this.state=Object.freeze({...this.state,...p});for(const fn of this.listeners)fn();}}
 private fail(e:unknown,error:Snapshot['error']){const value=e as {status?:number;code?:string};if(value?.status===401||value?.status===403||['unauthorized','not_permitted'].includes(value?.code??'')){this.dispose();this.reportError(e);}else this.publish({phase:'error',error});}
 private owns=(c:AbortController)=>this.current()&&this.work===c&&!c.signal.aborted;
 private transport(owns:()=>boolean):ApiAccessTransport{return{request:<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal)=>owns()?this.api.request<T>(path,method,body,signal):Promise.reject(Object.assign(new Error('The integration scope ended.'),{name:'AbortError'}))};}
 load=async(appCursor='',userCursor=''):Promise<void>=>{
  if(!this.current()||this.state.busy)return;
  this.work?.abort();const c=new AbortController();this.work=c;this.publish({phase:'loading',error:undefined});
  try{const api=this.transport(()=>this.owns(c));const [apps,users,options]=await Promise.all([listIntegrationAppPasswords(api,appCursor,c.signal),listIntegrationUsers(api,userCursor,c.signal),getApiAccessOptions(api,c.signal)]);if(this.owns(c))this.publish({phase:'ready',apps,users,options});}
  catch(e){if(this.owns(c))this.fail(e,'read');}finally{if(this.work===c)this.work=undefined;}
 };
 create=async(draft:IntegrationAppDraft):Promise<IssuedIntegrationAppPassword|undefined>=>{
  if(!this.current()||this.state.busy||this.intent||this.state.phase!=='ready')return;
  const user=this.state.users?.items.find(u=>u.accountId===draft.accountId&&u.profileId===draft.profileId&&!u.disabled),name=draft.name.trim(),scopes=[...new Set(draft.scopes)];
  if(!user||!name||name.length>80||!scopes.length||scopes.some(scope=>!this.state.options?.scopes.includes(scope))||scopes.includes('admin')&&scopes.length!==1)return;
  this.intent=Object.freeze({...draft,name,scopes:Object.freeze(scopes),operationId:this.operationId()});return this.issue();
 };
 retryCreate=()=>this.issue();
 private async issue():Promise<IssuedIntegrationAppPassword|undefined>{
  const intent=this.intent;if(!this.current()||this.state.busy||!intent)return;
  this.work?.abort();const c=new AbortController();this.work=c;this.publish({busy:true,pendingCreate:true,error:undefined});let out:IssuedIntegrationAppPassword|undefined;
  try{const value=await createIntegrationAppPassword(this.transport(()=>this.owns(c)),intent,c.signal);if(!this.owns(c))return;const app=value.appPassword;if(app.name!==intent.name||app.accountId!==intent.accountId||app.profileId!==intent.profileId||[...app.scopes].sort().join(',')!==[...intent.scopes].sort().join(','))throw new Error('The server returned a different app grant.');this.intent=undefined;out=value;this.publish({pendingCreate:false});}
  catch(e){if(this.owns(c))this.fail(e,'create');}finally{if(this.work===c){this.work=undefined;this.publish({busy:false});}}
  if(out&&this.current())await this.load();return this.current()?out:undefined;
 }
 revoke=async(app:IntegrationAppPassword):Promise<void>=>{
  if(!this.current()||this.state.busy||!this.state.apps?.items.includes(app)||app.state!=='active')return;
  this.work?.abort();const c=new AbortController();this.work=c;this.publish({busy:true,error:undefined});let changed=false;
  try{const receipt=await revokeIntegrationAppPassword(this.transport(()=>this.owns(c)),app.id,c.signal);if(this.owns(c)){if(receipt.id!==app.id||receipt.state!=='revoked')throw new Error('The revocation was not confirmed.');changed=true;}}
  catch(e){if(this.owns(c))this.fail(e,'revoke');}finally{if(this.work===c){this.work=undefined;this.publish({busy:false});}}
  if(changed&&this.current())await this.load();
 };
 reveal=async(app:IntegrationAppPassword):Promise<string|undefined>=>{
  if(!this.current()||this.state.busy||!this.state.apps?.items.includes(app)||app.state!=='active')return;
  const c=new AbortController();this.reveals.add(c);const owns=()=>this.current()&&!c.signal.aborted&&this.reveals.has(c)&&!!this.state.apps?.items.includes(app);
  try{const secret=await revealIntegrationAppPassword(this.transport(owns),app.id,c.signal);return owns()?secret:undefined;}catch(e){if(owns())this.fail(e,'reveal');return undefined;}finally{this.reveals.delete(c);}
 };
 dispose=()=>{this.work?.abort();for(const c of this.reveals)c.abort();this.reveals.clear();this.work=undefined;this.intent=undefined;this.lifetime.abort();this.state=empty;for(const fn of this.listeners)fn();this.listeners.clear();};
}
