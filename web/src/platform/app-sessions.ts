import {HttpLocalApi, type LocalSession} from '@core/index.ts';
import {AppSessionStore, type AppServerSession, type PorticoAccountScope} from '@core/app-sessions.ts';
import {ServerSignInProbe} from '@core/sign-in.ts';
import {parseDirectSnapshot, selectDirectProfile, shouldRetireAccountSession} from '@core/profile-management.ts';
import {rememberNativeSession, restoreNativeSession, type ConnectionEnvironment, type RememberedServer} from '@core/server-connections.ts';
import {browserAppSessions, browserConnectionEnvironment} from './server-connections';
import {browserAccount} from './account';
import {installationIdentity} from '@core/installation.ts';
import {browserTabProfileTrust} from './tab-profile-trust';
import {routePath} from '../../../packages/contracts/src/routes.ts';

/** Browser account actions share the canonical protected record. The caller retires its mounted
 * UI/player before switching/signing out. No account-service request is made for Local actions. */
export class BrowserAppSessionActions {
 constructor(readonly store:AppSessionStore, private readonly environment:(serverId:string)=>ConnectionEnvironment,
   private readonly revoke:(entry:AppServerSession)=>Promise<void>) {}
 readonly probe = new ServerSignInProbe();
 async switchServer(serverId:string,isCurrent:()=>boolean=()=>true,expectedRevision?:number){this.probe.cancel();await this.store.select(serverId,isCurrent,expectedRevision);}
 /** Forget follows an identity failure: the address may now be somebody else's.
  * Remove only the captured family's local record/pin/routes. Never reconnect,
  * revoke remotely, or send that family a token while forgetting it. */
 async forgetServer(session:LocalSession,capture?:Readonly<{isCurrent:()=>boolean;expectedRevision:number}>):Promise<boolean>{
  this.probe.cancel();const captured=await this.store.signOutServer(session.viewer.serverId,session.sessionFamilyId,capture?.isCurrent,capture?.expectedRevision);
  for(const entry of captured)browserTabProfileTrust.forgetAccount(entry.connection.pin.serverId,entry.connection.session.viewer.accountId);
  return captured.length>0;
 }
 async signOutServer(session:LocalSession){
  this.probe.cancel();const captured=await this.store.signOutServer(session.viewer.serverId,session.sessionFamilyId);
  for(const entry of captured)browserTabProfileTrust.forgetAccount(entry.connection.pin.serverId,entry.connection.session.viewer.accountId);
  return Promise.allSettled(captured.map(async entry=>this.revoke(entry)));
 }
 async signOutPorticoAccount(account:PorticoAccountScope,logout:()=>Promise<void>){
  this.probe.cancel();const captured=await this.store.signOutPorticoAccount(account);
  for(const entry of captured)browserTabProfileTrust.forgetAccount(entry.connection.pin.serverId,entry.connection.session.viewer.accountId);
  return Promise.allSettled([Promise.resolve().then(logout),...captured.map(async entry=>this.revoke(entry))]);
 }
 async switchProfile(api:HttpLocalApi,session:LocalSession,profileId:string,pin:string,signal:AbortSignal,isCurrent:()=>boolean,remember=false){
  const state=await this.store.read(),entry=state.servers.find(value=>value.connection.pin.serverId===session.viewer.serverId);
  if(!entry||entry.connection.session.sessionFamilyId!==session.sessionFamilyId)throw new Error('This server sign-in has ended.');
  const snapshot=parseDirectSnapshot(await api.request(routePath('get_direct_account'),'GET',undefined,signal),{authority:'local',serverId:session.viewer.serverId,accountId:session.viewer.accountId});
  if(!snapshot.profiles.some(value=>value.id===profileId))throw new Error('This profile is no longer available.');
  const installation=installationIdentity();
  if(!installation)throw new Error('This browser installation is no longer available.');
  const scope={authority:'local' as const,serverId:session.viewer.serverId,accountId:session.viewer.accountId,profileId,installationId:installation.installationId};
  const proof=browserTabProfileTrust.read(scope);
  let chosen:Awaited<ReturnType<typeof selectDirectProfile>>;
  try{chosen=await selectDirectProfile(api,scope,profileId,{pin,installationId:scope.installationId,...(remember?{trust:true}:{}),...(proof?{trustToken:proof.token}:{})},signal);}
  catch(error){if(['profile_pin','profile_pin_invalid','profile_pin_required','profile_pin_locked'].includes(String((error as {code?:unknown})?.code)))browserTabProfileTrust.forget(scope);throw error;}
  const env={...this.environment(session.viewer.serverId),storage:this.store.connectionStorage(session.viewer.serverId,{signIn:entry.signIn,hostedAccount:entry.connection.hosted,expectedRevision:state.revision})};
  try{await rememberNativeSession(api,chosen.session,env,entry.connection.hosted,()=>isCurrent()&&!signal.aborted);}
  catch(error){const cleanup=new HttpLocalApi(api.baseUrl,chosen.session.accessToken);const route=api.getRouteConnection();if(route)cleanup.useRoutes(route);await cleanup.logout().catch(()=>{});throw error;}
  if(remember&&chosen.trustedSelection&&isCurrent()&&!signal.aborted)browserTabProfileTrust.save(chosen.trustedSelection);
  if(shouldRetireAccountSession(session,chosen.session)){
   const cleanup=new HttpLocalApi(api.baseUrl,session.accessToken);const route=api.getRouteConnection();if(route)cleanup.useRoutes(route);
   await cleanup.logout().catch(()=>{});
  }
  return chosen.session;
 }
 dispose(){this.probe.dispose();}
}
let actions:BrowserAppSessionActions|undefined;
/** Reconnect only with this captured server's pin/routes, even after it was removed from the app.
 * A failed/offline remote revocation is returned to the caller; credentials stay removed locally. */
async function revokeCaptured(entry:AppServerSession){
 let record:RememberedServer|undefined=entry.connection;
 const env={...browserConnectionEnvironment(entry.connection.pin.serverId),storage:{read:async()=>record,change:async(update:(v:RememberedServer|undefined)=>RememberedServer|undefined)=>{record=update(record);}}};
 const restored=await restoreNativeSession(env,undefined,entry.connection.session);
 if(!restored)throw new Error('This server sign-in could not be ended remotely.');
 try{await restored.api.logout();}finally{restored.api.getRouteConnection()?.dispose();}
}
export function browserAppSessionActions(){return actions??=new BrowserAppSessionActions(browserAppSessions(),id=>browserConnectionEnvironment(id),revokeCaptured);}
export async function signOutBrowserPorticoAccount(){
 const central=browserAccount();await central.service.synchronize();const session=central.service.getSnapshot().session;
 const saved=(await browserAppSessions().read()).servers.find(value=>value.signIn==='portico')?.connection.hosted;
 const account=saved??(session?{origin:central.api.origin,accountId:session.account.id}:undefined);
 if(!account)return [];
 return browserAppSessionActions().signOutPorticoAccount(account,async()=>{if(session)await central.service.logoutIfCurrent({accountId:session.account.id,familyId:session.familyId});});
}
