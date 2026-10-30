import {GUIDE_PROTOCOL} from '@core/channel-guide.ts';
import {FreshnessHub} from '@core/freshness-hub.ts';
import {parseDirectSnapshot} from '@core/profile-management.ts';
import {createShellFactOwner, shellAccount} from '../src/shell/facts.ts';
import type {ShellFacts} from '../src/shell/bootstrap.ts';
import type {LocalSession} from '@core/index.ts';

export function clock() {
  let now = 0, id = 0;
  const timers = new Map<number,{at:number;fn:()=>void}>();
  const flush = async () => {for (let i=0; i<40; i++) await Promise.resolve();};
  return {env:{now:()=>now, random:()=>0, setTimer:(fn:()=>void,ms:number)=>{timers.set(++id,{at:now+ms,fn});return id;}, clearTimer:(id:unknown)=>{timers.delete(id as number);}},
    async advance(ms:number) {const end=now+ms; for (;;) {let due:[number,{at:number;fn:()=>void}]|undefined;
      for (const entry of timers) if (entry[1].at<=end && (!due || entry[1].at<due[1].at)) due=entry;
      if (!due) break; timers.delete(due[0]); now=due[1].at; due[1].fn(); await flush();}
      now=end; await flush();}, pending:()=>timers.size};
}
export const direct = () => ({authority:'local', serverId:'server', canManage:true,
  capabilities:{manageAccount:true,managePeople:true,manageServer:true,manageSignIn:true},
  account:{id:'account',username:'viewer',primaryProfileId:'profile',role:'owner',revision:1,disabled:false,allowedLibraries:[]},
  profiles:[{id:'profile',name:'Evening',primary:true,position:0,art:'mint',revision:1,pinRevision:1,pinRequired:false}]});
export const system = () => ({id:'server',name:'Study',version:'1',signIn:'local' as const,move:null,setupRequired:false,hostedConfigured:false,hostedAttached:false, apiLevel: 1, apiVersion: '1.0', apiMajors: [1], capabilities: []});
export function harness(readonlyAccess=false) {
  const time=clock(), hub=new FreshnessHub(time.env), session={sessionFamilyId:'family',accessToken:'private-test-token',viewer:{serverId:'server',authority:'local',accountId:'account',profileId:'profile',role:'owner'}} as LocalSession;
  let record:unknown=direct(), info=system(), libraries=[{id:'library',name:'Films',kind:'movies'}], guideError:unknown, systemError:unknown, preferenceError:unknown, homeError:unknown, retired=0, permission=0, manifests=0;
  const calls:string[]=[];
  let libraryRead: (()=>Promise<unknown>) | undefined;
  const settings={effective:{},deviceClass:'web'} as ShellFacts['preferences'];
  const witnesses={system:system(),direct:parseDirectSnapshot(direct())};
  const initial:ShellFacts={serverName:'Study',preferences:settings,identity:{viewer:session.viewer,serverName:'Study',serverVersion:'1',libraries,
    ...shellAccount(witnesses.direct,'profile'),liveAvailable:true,unread:0,showNotificationDot:false}};
  const api={async request(path:string) {calls.push(path); if (path==='/v1/libraries') return libraryRead ? await libraryRead() : {items:libraries}; if (path==='/v1/direct') return record;
    if (path==='/v1/system') {if(systemError)throw systemError;return info;} if (path.startsWith('/v1/guide/sources?')) {if (guideError) throw guideError;return {protocolVersion:GUIDE_PROTOCOL,serverId:'server',viewerFence:'guide',revision:'1',sources:[]};}
    throw new Error('Unexpected read '+path);}};
  const owner=createShellFactOwner({api:api as never, session:()=>session, hub,initial,witnesses,readonlyAccess,
    isCurrent:()=>retired===0, retire:()=>{retired++;},
    home:{authority:async()=>{manifests++;if(homeError)throw homeError;return 'answered';}} as never,
    preferences:{observePreferences:async()=>{calls.push('/v1/preferences?deviceClass=web');return preferenceError?{kind:'unreadable',error:preferenceError}:{kind:'ok',value:settings};}} as never,
    auth:{reconcilePermissions:async()=>{permission++;return {kind:'confirmed-unchanged'};}} as never});
  return {time,hub,owner,calls,initial,witnesses,libraryRead:(read?:()=>Promise<unknown>)=>{libraryRead=read;},counts:()=>({retired,permission,manifests}),
    rename:()=>{info={...info,name:'Den'};libraries=[{id:'library',name:'Cinema',kind:'movies'}];const r=direct();r.account.username='updated';r.profiles[0].name='Night';r.account.revision=2;record=r;},
    systemFailure:(error:unknown)=>{systemError=error;},preferenceFailure:(error:unknown)=>{preferenceError=error;},homeFailure:(error:unknown)=>{homeError=error;},
    direct:(value:unknown)=>{record=value;},guideFailure:(error:unknown)=>{guideError=error;},dispose:()=>{owner.dispose();hub.dispose();}};
}
