import type {LocalSession} from './index.ts';
import {parseLocalSession} from './local-session.ts';
import {setupResultBody} from './setup-result.ts';
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
