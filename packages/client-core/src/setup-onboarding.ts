import {routeBody} from "../../contracts/src/routes.ts";
import {routePath} from "../../contracts/src/route-paths.ts";
import {randomId} from './random-id.ts';
import {ApiError,HttpLocalApi,type LocalSession} from './index.ts';
import {parseLocalSession} from './local-session.ts';
import {setupResultBody} from './setup-result.ts';
export type SetupDraft={version:1;serverId:string;requestId:string;setupToken:string;name:string;username?:string;authMode:'hosted'|'local';interactive?:boolean};
export type PendingAccountSetup=Readonly<{serverId:string;requestId:string;authMode:'hosted';claimRequired:true;revision:string}>;
export type SetupSession=LocalSession|PendingAccountSetup;
export function parseSetupSession(raw:unknown,id:string):SetupSession {
 const {tag,body:v}=setupResultBody(raw);
 if(tag==='claim-required'){
  if(v.claimRequired!==true)throw new Error('The pending setup did not match this server.');
  if(v.serverId!==id||v.authMode!=='hosted'||typeof v.requestId!=='string'||!/^[a-f0-9-]{36}$/.test(v.requestId)||typeof v.revision!=='string'||!/^[1-9][0-9]{0,18}$/.test(v.revision)||BigInt(v.revision)>9223372036854775807n||'accessToken' in v||'refreshToken' in v||'viewer' in v)throw new Error('The pending setup did not match this server.');
  return Object.freeze({serverId:id,requestId:v.requestId,authMode:'hosted',claimRequired:true,revision:v.revision});
 }
 return parseLocalSession(v,{serverId:id,authority:'local'});
}
export interface SetupDraftStore {read():Promise<SetupDraft|undefined>;change(update:(old:SetupDraft|undefined)=>SetupDraft|undefined):Promise<void>}
export type SetupInput=Omit<SetupDraft,'version'|'serverId'|'requestId'>&{password?:string};
function serverId(api:HttpLocalApi){const id=api.getRouteConnection()?.pin.serverId;if(!id)throw new Error('Verify this server identity before initialization.');return id;}
/** Passwords are never placed in the draft. The high-entropy setup secret and
 * operation ID live only in protected credential storage until durable adoption. */
export async function initializeServer(api:HttpLocalApi,store:SetupDraftStore,input:SetupInput,signal:AbortSignal,requestId:()=>Promise<string>=async()=>randomId()):Promise<SetupSession>{
 const id=serverId(api),newId=await requestId();let draft:SetupDraft|undefined;
 await store.change(old=>{if(old&&(old.serverId!==id||old.setupToken!==input.setupToken))throw new Error('A different setup is saved. Resume it before starting another setup.');draft=old?{...old,...(old.authMode!==input.authMode?{name:input.name,username:input.username,authMode:input.authMode}:{} )}:{version:1,serverId:id,requestId:newId,setupToken:input.setupToken,name:input.name,username:input.username,authMode:input.authMode,interactive:input.interactive};return draft;});
 try{const resumed=await resumeServerSetup(api,store,signal);if(!('claimRequired' in resumed)||draft!.authMode==='hosted')return resumed;}catch(e){if(!(e instanceof ApiError)||e.code!=='not_found')throw e;}
 return parseSetupSession(await api.request(routePath("setup_server"),'POST',routeBody("setup_server", {requestId:draft!.requestId,setupToken:draft!.setupToken,username:draft!.username,password:input.password,name:draft!.name,authMode:draft!.authMode,interactive:draft!.interactive}),signal),id);
}
export async function resumeServerSetup(api:HttpLocalApi,store:SetupDraftStore,signal:AbortSignal):Promise<SetupSession>{
 const id=serverId(api),draft=await store.read();if(!draft||draft.version!==1||draft.serverId!==id)throw new Error('No interrupted setup is saved for this server.');
 return parseSetupSession(await api.request(routePath("post_setup_resume"),'POST',routeBody("post_setup_resume", {requestId:draft.requestId,setupToken:draft.setupToken}),signal),id);
}
export async function completeServerSetupDraft(api:HttpLocalApi,store:SetupDraftStore){const id=serverId(api);await store.change(old=>old?.serverId===id?undefined:old);}
/** First run from an app (phone or tablet): the server's one-time setup secret. Browsers use
 * `start_browser_setup` from the server's own page; this route refuses any browser. */
export async function startAppSetup(api:HttpLocalApi,signal?:AbortSignal):Promise<{serverId:string;setupToken:string}>{
 const raw=await api.request<{serverId?:unknown;setupToken?:unknown}>(routePath("start_app_setup"),'POST',{},signal);
 if(typeof raw?.serverId!=='string'||typeof raw.setupToken!=='string'||raw.serverId!==serverId(api))throw new Error('The setup answer did not match this server.');
 return {serverId:raw.serverId,setupToken:raw.setupToken};
}
/** The installed claim's account and a fresh verified identity assertion are both
 * required. This never synthesizes a local owner while Portico is unavailable. */
export async function completeAccountServerSetup(api:HttpLocalApi,store:SetupDraftStore,porticoIdentity:unknown,signal?:AbortSignal){
 const id=serverId(api),draft=await store.read();if(!draft||draft.serverId!==id||draft.authMode!=='hosted')throw new Error('No pending account setup is saved for this server.');
 const {parseDirectSignIn}=await import('./profile-management.ts');
 const out=parseDirectSignIn(await api.request(routePath('post_setup_account_complete'),'POST',{requestId:draft.requestId,setupToken:draft.setupToken,porticoIdentity},signal));
 if(out.kind!=='signed-in'||out.snapshot.serverId!==id||out.snapshot.account.role!=='owner'||!out.snapshot.account.hostedAccountId||out.resetFileOwnerSession)throw new Error('Account setup did not return the claimed owner.');
 return out;
}
