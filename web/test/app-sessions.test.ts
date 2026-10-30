import test from 'node:test';
import assert from 'node:assert/strict';
import {AppSessionStore,type AppSessionRecord,type AppServerSession} from '@core/app-sessions.ts';
import {BrowserAppSessionActions} from '../src/platform/app-sessions.ts';
import {createAppViewerStorage} from '../src/platform/viewer-selection-storage.ts';
const scope={origin:'https://accounts.test',accountId:'central-account'};
function entry(id:string,portico=false):AppServerSession{return {signIn:portico?'portico':'local',connection:{version:'1',selectionId:id.padEnd(32,'_'),logicalOrigin:'https://'+id+'.test',pin:{serverId:id,publicKey:'p',fingerprint:'f'},routes:{paired:['https://'+id+'.test']},...(portico?{hosted:{...scope,key:{keyId:'key',publicKey:'k'.repeat(43)}}}:{}),session:{sessionFamilyId:'family-'+id,tokenGeneration:'1',accessToken:'credential-'+id,expiresAt:'2099-01-01T00:00:00Z',authorizationHorizon:'2099-02-01T00:00:00Z',viewer:{serverId:id,accountId:'membership-'+id,profileId:'profile-'+id,authority:'local',role:'member'}}}};}
function setup(){
 let record:AppSessionRecord|undefined={version:1,revision:1,selectedServerId:'local',servers:[entry('local'),entry('first',true),entry('second',true)]};
 const store=new AppSessionStore({read:async()=>structuredClone(record),change:async update=>{record=structuredClone(update(structuredClone(record)));}});
 const revoked:AppServerSession[]=[];let offline=false;
 const actions=new BrowserAppSessionActions(store,()=>{throw new Error('Unexpected network environment.');},async value=>{revoked.push(value);if(offline)throw new Error('offline');});
 return {store,actions,revoked,setOffline:()=>{offline=true;},viewer:createAppViewerStorage(store)};
}
test('actual browser actions/viewer projection keep independent servers and remembered profiles across switches',async()=>{
 const f=setup();assert.equal((await f.viewer.read())?.session.viewer.profileId,'profile-local');
 await f.actions.switchServer('first');assert.equal((await f.viewer.read())?.session.viewer.profileId,'profile-first');
 await f.actions.switchServer('second');assert.equal((await f.viewer.read())?.session.viewer.profileId,'profile-second');
 assert.equal((await f.store.read()).servers.length,3);assert.equal(f.revoked.length,0);f.actions.dispose();
});
test('a cancelled queued server selection cannot commit or settle over its successor',async()=>{
 let raw:AppSessionRecord|undefined={version:1,revision:1,selectedServerId:'local',servers:[entry('local'),entry('first'),entry('second')]};
 let unlock!:()=>void,entered!:()=>void,first=true,current=true;
 const waiting=new Promise<void>(resolve=>{unlock=resolve;}),started=new Promise<void>(resolve=>{entered=resolve;});
 let tail=Promise.resolve();
 const store=new AppSessionStore({read:async()=>structuredClone(raw),change:(update,beforeCommit)=>{
  const result=tail.then(async()=>{const next=update(raw);if(first){first=false;entered();await waiting;}beforeCommit?.();raw=next;});
  tail=result.catch(()=>{});return result;
 }});
 const actions=new BrowserAppSessionActions(store,()=>{throw new Error('No network expected.');},async()=>{throw new Error('No revocation expected.');});
 const cancelled=assert.rejects(actions.switchServer('first',()=>current),{code:'cancelled'});
 await started;current=false;
 const successor=actions.switchServer('second');
 assert.equal((await store.read()).selectedServerId,'local');
 unlock();await cancelled;await successor;
 assert.equal((await store.read()).selectedServerId,'second');
 assert.equal((await store.read()).revision,2,'only the successor advanced the admission fence');
 actions.dispose();
});
test('server signout retires exact device-server family before remote call and offline failure never resurrects it',async()=>{
 const f=setup();await f.actions.switchServer('first');f.setOffline();const session=(await f.store.selected())!.connection.session;
 const result=await f.actions.signOutServer(session);assert.equal(result[0].status,'rejected');
 assert.equal(await f.viewer.read(),null);assert.deepEqual((await f.store.read()).servers.map(v=>v.connection.pin.serverId),['local','second']);
 assert.equal(f.revoked[0].connection.session.sessionFamilyId,'family-first');
 await f.actions.switchServer('local');assert.equal((await f.viewer.read())?.session.viewer.profileId,'profile-local');f.actions.dispose();
});
test('account signout ends only Portico families and account session, preserving Local through offline cleanup',async()=>{
 const f=setup();f.setOffline();let account=0;
 const result=await f.actions.signOutPorticoAccount(scope,async()=>{account++;throw new Error('offline account revoke queued');});
 assert.equal(account,1);assert.equal(result.filter(v=>v.status==='rejected').length,3);
 assert.deepEqual(f.revoked.map(v=>v.connection.pin.serverId),['first','second']);
 assert.deepEqual((await f.store.read()).servers.map(v=>v.connection.pin.serverId),['local']);assert.equal((await f.viewer.read())?.session.viewer.serverId,'local');f.actions.dispose();
});
test('an old tab cannot clear another server or a later replacement family',async()=>{
 const f=setup();await f.actions.switchServer('first');await f.viewer.clearIfFamily!('family-local');assert.equal((await f.viewer.read())?.session.viewer.serverId,'first');
 await assert.rejects(f.viewer.write({version:1,serverUrl:'https://wrong.test',session:entry('first',true).connection.session}));
 assert.equal((await f.viewer.read())?.session.viewer.serverId,'first');f.actions.dispose();
});
test('Forget removes the exact saved server pin/routes/family without any network or remote revoke',async()=>{
 const f=setup();await f.actions.switchServer('first');const captured=(await f.store.selected())!.connection.session;
 f.setOffline();assert.equal(await f.actions.forgetServer(captured),true);
 assert.equal(f.revoked.length,0,'the changed address must never receive the old token');
 assert.equal(await f.viewer.read(),null);
 const state=await f.store.read();assert.deepEqual(state.servers.map(row=>row.connection.pin.serverId),['local','second']);
 assert.equal(JSON.stringify(state).includes('https://first.test'),false,'the forgotten row owned all paired route memory');
 assert.equal(await f.actions.forgetServer(captured),false);assert.equal(f.revoked.length,0);
 await f.actions.switchServer('local');assert.equal((await f.viewer.read())?.session.viewer.serverId,'local');f.actions.dispose();
});
test('Forget with a stale family cannot remove a current replacement or switch another selected server',async()=>{
 const f=setup();const old={...entry('first',true).connection.session,sessionFamilyId:'retired-family'};
 assert.equal(await f.actions.forgetServer(old),false);
 assert.equal((await f.store.read()).servers.length,3);
 assert.equal((await f.viewer.read())?.session.viewer.serverId,'local');assert.equal(f.revoked.length,0);f.actions.dispose();
});
