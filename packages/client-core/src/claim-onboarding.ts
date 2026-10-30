import {routePrefix} from "../../contracts/src/routes.ts";
import {routePath} from "../../contracts/src/route-paths.ts";
import {ApiError,HttpLocalApi,transportJSON} from './index.ts';
import type {ServerPin} from './route-identity.ts';
export type ClaimExpected={operationId:string;localGeneration:string;revision:string};
export type ClaimApproval={operationId:string;serverId:string;publicKey:string;localGeneration:string;name:string;expectedRevision:string;purpose:"setup"|"switch"|"change-owner";currentOwnerAccountId:string;previousOwnerRole:string;returnRegistrationId:string};
export type ClaimStatus={state:string;identity:{serverId:string;localGeneration:string};operation?:ClaimExpected;accountId?:string;installationAcknowledged:boolean;approvalRequired:boolean;actions:string[];approvalRequest?:ClaimApproval};
export type ClaimAccount={accountId:string;profileId:string;approve:(input:ClaimApproval&{ownerProfileId:string},signal:AbortSignal)=>Promise<unknown>};
export type ClaimSnapshot={status?:ClaimStatus;busy:boolean;error?:string;needsAccount?:boolean};
const counter=(value:unknown)=>typeof value==='string'&&/^(0|[1-9][0-9]{0,18})$/.test(value)&&BigInt(value)<=9223372036854775807n;
export function parseClaimStatus(value:unknown,pin:ServerPin):ClaimStatus{
 const s=value as ClaimStatus;
 if(!s||typeof s.state!=='string'||s.identity?.serverId!==pin.serverId||!counter(s.identity?.localGeneration)||!Array.isArray(s.actions)||s.actions.some(x=>!['prepare','approve','continue','cancel'].includes(x))||typeof s.installationAcknowledged!=='boolean'||typeof s.approvalRequired!=='boolean')throw new Error('The claim response did not match this server.');
 if(s.operation&&(!counter(s.operation.revision)||!counter(s.operation.localGeneration)||typeof s.operation.operationId!=='string'||s.operation.operationId.length>128))throw new Error('The claim operation was invalid.');
 if(s.approvalRequest&&(s.approvalRequest.serverId!==pin.serverId||s.approvalRequest.publicKey!==pin.publicKey||s.approvalRequest.operationId!==s.operation?.operationId||!counter(s.approvalRequest.expectedRevision)||s.approvalRequest.localGeneration!==s.identity.localGeneration))throw new Error('The approval belongs to another server identity.');
 return s;
}
/** The server's installed claim journal is the durable owner. UI remounts only
 * read/continue it, and never create a new claim after an ambiguous result. */
export class ClaimOnboarding {
 private state:ClaimSnapshot={busy:false};private listeners=new Set<()=>void>();private controller?:AbortController;private timer?:ReturnType<typeof setTimeout>;private disposed=false;private failures=0;
 private api:HttpLocalApi;private pin:ServerPin;
 constructor(api:HttpLocalApi,pin:ServerPin){this.api=api;this.pin=pin;}
 getSnapshot=()=>this.state;
 subscribe=(fn:()=>void)=>{this.listeners.add(fn);return()=>{this.listeners.delete(fn);};};
 private emit(value:ClaimSnapshot){if(this.disposed)return;this.state=value;for(const fn of this.listeners)fn();}
 private async load(signal:AbortSignal){return parseClaimStatus(await this.api.request(routePath("get_networking_claim"),'GET',undefined,signal),this.pin);}
 private async post(action:string,body:unknown,signal:AbortSignal){return parseClaimStatus(await this.api.request(routePrefix("post_networking_claim_approve", 3)+action,'POST',body,signal),this.pin);}
 private schedule(status:ClaimStatus,delay=5000){clearTimeout(this.timer);if(!this.disposed&&status.actions.includes('continue')&&!status.approvalRequired)this.timer=setTimeout(()=>void this.refresh(true),delay);}
 private async run(work:(signal:AbortSignal)=>Promise<ClaimStatus>){
  if(this.disposed||this.state.busy)return;clearTimeout(this.timer);const controller=new AbortController();this.controller=controller;const timeout=setTimeout(()=>controller.abort(),20000);this.emit({...this.state,busy:true,error:undefined});
  try{const status=await work(controller.signal);if(controller.signal.aborted||this.disposed||this.controller!==controller)return;this.failures=0;this.emit({status,busy:false});this.schedule(status);}
  catch(e){if(this.disposed||this.controller!==controller)return;this.failures++;this.emit({...this.state,busy:false,error:e instanceof Error?e.message:'The claim will resume when its authority is available.'});const retry=e instanceof ApiError?e.retryAfterSeconds??0:0;clearTimeout(this.timer);this.timer=setTimeout(()=>void this.refresh(true),Math.max(retry*1000,Math.min(60000,5000*2**Math.min(4,this.failures-1))));}
  finally{clearTimeout(timeout);if(this.controller===controller)this.controller=undefined;}
 }
 start(){this.disposed=false;this.state={...this.state,busy:false};return this.refresh(true);}
 refresh(continuePending=false){return this.run(async signal=>{const status=await this.load(signal);if(continuePending&&status.operation&&status.actions.includes('continue')&&!status.approvalRequired)return this.post('continue',{expected:status.operation},signal);return status;});}
 connect(account:ClaimAccount){return this.run(async signal=>{
  let status=await this.load(signal);
  if(status.actions.includes('prepare'))status=await this.post('prepare',{accountId:account.accountId,expected:status.identity},signal);
  if(status.accountId!==account.accountId)throw new Error('This claim belongs to a different Portico Account. Resume that account or explicitly cancel this claim.');
  if(status.approvalRequired){const request=status.approvalRequest;if(!request||!status.operation)throw new Error('The claim needs to be refreshed.');
   // Preserve the producer's exact field order and selected owner profile.
   const input={operationId:request.operationId,serverId:request.serverId,publicKey:request.publicKey,localGeneration:request.localGeneration,name:request.name,ownerProfileId:account.profileId,expectedRevision:request.expectedRevision,purpose:request.purpose,currentOwnerAccountId:request.currentOwnerAccountId,previousOwnerRole:request.previousOwnerRole,returnRegistrationId:request.returnRegistrationId};
   const envelope=await account.approve(input,signal);
   if(signal.aborted)throw new Error('The claim was interrupted. Reopen to resume it.');
   status=await this.post('approve',{expected:status.operation,approvalEnvelope:transportJSON(envelope)},signal);
  }
  if(status.operation&&status.actions.includes('continue'))status=await this.post('continue',{expected:status.operation},signal);
  return status;
 });}
 cancel(){return this.run(async signal=>{const status=await this.load(signal);if(!status.operation||!status.actions.includes('cancel'))return status;return this.post('cancel',{expected:status.operation},signal);});}
 dispose(){this.disposed=true;this.controller?.abort();clearTimeout(this.timer);this.listeners.clear();}
}
