import test from 'node:test';
import assert from 'node:assert/strict';
import {ViewerSelectionCommit,selectedViewerRecord,parseSelectedViewerRecord} from '../src/viewer-selection.ts';
import type {SelectedViewerRecord,SelectionHandoff,SelectedViewerStorage} from '../src/viewer-selection.ts';

const record=(profile:string,token=profile)=>selectedViewerRecord('https://server.example',{
 accessToken:token,expiresAt:new Date(Date.now()+3600000).toISOString(),
 authorizationHorizon:new Date(Date.now()+7200000).toISOString(),sessionFamilyId:'family-'+token,tokenGeneration:'1',
 viewer:{accountId:'account',profileId:profile,serverId:'server',authority:'local',role:'member'},
});
function fixture(){
 const previous=record('previous'),candidate=record('candidate');
 let saved:SelectedViewerRecord|null=previous,current:SelectedViewerRecord|null=previous;
 const events:string[]=[],revoked:string[]=[];
 const storage:SelectedViewerStorage={read:async()=>{events.push('read');return saved;},write:async value=>{events.push('save:'+value?.session.viewer.profileId);saved=value;}};
 const h:SelectionHandoff={current:()=>current,assertCurrent:()=>{},fence:()=>{events.push('fence');current=null;},
 verify:async value=>{events.push('verify');assert.equal(saved,value);assert.equal(current,null);return{viewer:value.session.viewer};},
 prepareContext:async()=>{events.push('prepare');},publish:value=>{events.push('publish');current=value;},
 restore:value=>{events.push('restore');current=value;},revoke:async value=>{revoked.push(value.session.accessToken);}};
 return{previous,candidate,events,revoked,storage,h,commit:new ViewerSelectionCommit(storage),saved:()=>saved,current:()=>current};
}
const tick=()=>new Promise(resolve=>setTimeout(resolve,0));
test('profile handoff fences, durably saves, verifies exact viewer, and only then publishes',async()=>{
 const f=fixture();await f.commit.commit(f.candidate,f.h);await tick();
 assert.deepEqual(f.events,['read','fence','save:candidate','verify','prepare','publish']);
 assert.equal(f.current(),f.candidate);assert.equal(f.saved(),f.candidate);
 assert.deepEqual(f.revoked,['previous']); // No account or other-device revocation.
});
test('wrong server me cannot publish and restores old selection without restarting playback',async()=>{
 const f=fixture();f.h.verify=async()=>({viewer:{...f.candidate.session.viewer,accountId:'another-account'}});
 await assert.rejects(f.commit.commit(f.candidate,f.h),/does not match/);await tick();
 assert.equal(f.saved(),f.previous);assert.equal(f.current(),f.previous);assert.ok(!f.events.includes('publish'));
 assert.deepEqual(f.revoked,['candidate']);
});
test('partially successful candidate write is rolled back on write/readback failure',async()=>{
 const f=fixture(),write=f.storage.write;let count=0;
 f.storage.write=async v=>{await write(v);if(++count===1)throw new Error('readback failed');};
 await assert.rejects(f.commit.commit(f.candidate,f.h),/readback failed/);await tick();
 assert.equal(f.saved(),f.previous);assert.equal(f.current(),f.previous);assert.deepEqual(f.revoked,['candidate']);
});
test('failed rollback clears recovery and retires both this-device server families',async()=>{
 const f=fixture(),write=f.storage.write;f.h.verify=async()=>{throw new Error('offline');};
 f.storage.write=async v=>{if(v===f.previous)throw new Error('rollback write failed');await write(v);};
 await assert.rejects(f.commit.commit(f.candidate,f.h),/could not be rolled back safely/);await tick();
 assert.equal(f.saved(),null);assert.equal(f.current(),null);assert.deepEqual(f.revoked.sort(),['candidate','previous']);
});
test('sign-out linearizes behind candidate storage and cannot resurrect a late selection',async()=>{
 const f=fixture(),write=f.storage.write;let release!:()=>void,started!:()=>void;
 const waiting=new Promise<void>(r=>release=r),entered=new Promise<void>(r=>started=r);
 f.storage.write=async v=>{if(v===f.candidate){started();await waiting;}await write(v);};
 const attempt=f.commit.commit(f.candidate,f.h);await entered;const clear=f.commit.clear();release();
 await assert.rejects(attempt,/cancelled/);await clear;await tick();
 assert.equal(f.saved(),null);assert.equal(f.current(),null);assert.ok(!f.events.includes('publish'));assert.deepEqual(f.revoked,['candidate']);
});
test('concurrent candidate is retired without disturbing the in-flight handoff',async()=>{
 const f=fixture();let release!:()=>void,entered!:()=>void;const wait=new Promise<void>(r=>release=r),start=new Promise<void>(r=>entered=r);
 f.h.verify=async v=>{entered();await wait;return{viewer:v.session.viewer};};const first=f.commit.commit(f.candidate,f.h);await start;
 await assert.rejects(f.commit.commit(record('third'),f.h),/Another profile/);release();await first;await tick();
 assert.equal(f.current(),f.candidate);assert.deepEqual(f.revoked,['third','previous']);
});
test('current snapshot failure retires candidate and never leaves the handoff permanently busy',async()=>{
 const f=fixture();const current=f.h.current;f.h.current=()=>{throw new Error('invalid saved current viewer');};
 await assert.rejects(f.commit.commit(f.candidate,f.h),/invalid saved/);await tick();assert.deepEqual(f.revoked,['candidate']);
 f.h.current=current;await f.commit.commit(record('next'),f.h);assert.equal(f.current()?.session.viewer.profileId,'next');
});
test('resuming an expired saved viewer renews it: the renewed family is committed, never retired',async()=>{
 // The saved record's access token has expired; the restore renewed it in the same family. The
 // viewer is read as stored (as the clients do), so the commit knows the sign-in it is renewing.
 const session=(token:string,expiresAt:string)=>({accessToken:token,expiresAt,authorizationHorizon:new Date(Date.now()+7200000).toISOString(),
  sessionFamilyId:'family-resumed',tokenGeneration:token==='old'?'1':'2',viewer:{accountId:'account',profileId:'resumed',serverId:'server',authority:'local',role:'member'}});
 const expired=session('old',new Date(Date.now()-60000).toISOString()),renewed=selectedViewerRecord('https://server.example',session('new',new Date(Date.now()+900000).toISOString()));
 assert.throws(()=>selectedViewerRecord('https://server.example',expired),/./);
 const f=fixture();const saved=selectedViewerRecord('https://server.example',expired,true);let current:SelectedViewerRecord|null=saved;
 f.h.current=()=>current;f.h.fence=()=>{current=null;};f.h.verify=async v=>({viewer:v.session.viewer});f.h.publish=v=>{current=v;};
 await f.commit.commit(renewed,f.h);await tick();
 assert.equal(current,renewed);assert.deepEqual(f.revoked,[]);
});
test('account/family scope change during verification cannot restore or publish private context',async()=>{
 const f=fixture();let valid=true;f.h.assertCurrent=()=>{if(!valid)throw new Error('account changed');};
 f.h.verify=async v=>{valid=false;return{viewer:v.session.viewer};};await assert.rejects(f.commit.commit(f.candidate,f.h));await tick();
 assert.equal(f.saved(),null);assert.equal(f.current(),null);assert.ok(!f.events.includes('publish'));assert.ok(!f.events.includes('restore'));
});
test('selected viewer records reject ambiguous envelopes and credential-bearing server URLs',()=>{
 const f=fixture();assert.deepEqual(parseSelectedViewerRecord(f.previous),f.previous);
 for(const server of ['https://user:password@server.example','https://server.example/?token=x','https://server.example/#x','file:///tmp/server'])assert.throws(()=>selectedViewerRecord(server,f.previous.session));
 assert.throws(()=>parseSelectedViewerRecord({...f.previous,version:0}));
 assert.throws(()=>selectedViewerRecord(f.previous.serverUrl,{...f.previous.session,accountToken:'not-a-viewer-token'}));
});
test('duplicate submission of the same candidate never retires the in-flight family',async()=>{
 const f=fixture();let release!:()=>void,entered!:()=>void;const wait=new Promise<void>(r=>release=r),start=new Promise<void>(r=>entered=r);
 f.h.verify=async v=>{entered();await wait;return{viewer:v.session.viewer};};const first=f.commit.commit(f.candidate,f.h);await start;
 await assert.rejects(f.commit.commit(f.candidate,f.h),/Another profile/);assert.deepEqual(f.revoked,[]);
 release();await first;await tick();assert.deepEqual(f.revoked,['previous']);
});
test('sign-out is not held hostage by a verification transport which ignores abort',async()=>{
 const f=fixture();let started!:()=>void,release!:(v:any)=>void;
 const entered=new Promise<void>(r=>started=r),pending=new Promise<any>(r=>release=r);
 f.h.verify=async()=>{started();return pending;};
 const attempt=f.commit.commit(f.candidate,f.h);const rejected=assert.rejects(attempt,/cancelled/);await entered;
 await Promise.race([f.commit.clear(),new Promise<never>((_,reject)=>setTimeout(()=>reject(new Error('clear hung on network verification')),250))]);await rejected;
 assert.equal(f.saved(),null);assert.equal(f.current(),null);
 release({viewer:f.candidate.session.viewer});await tick();
 assert.ok(!f.events.includes('restore'));assert.ok(!f.events.includes('publish'));assert.deepEqual(f.revoked,['candidate']);
 f.h.verify=async v=>({viewer:v.session.viewer});await f.commit.commit(record('new-login'),f.h);
});
test('sign-out during a pending rollback cannot restore the prior viewer after logout',async()=>{
 const f=fixture(),write=f.storage.write;let started!:()=>void,release!:()=>void;
 const entered=new Promise<void>(r=>started=r),pending=new Promise<void>(r=>release=r);
 f.h.verify=async()=>{throw new Error('verification rejected');};
 f.storage.write=async v=>{if(v===f.previous){started();await pending;}await write(v);};
 const attempt=f.commit.commit(f.candidate,f.h);const rejected=assert.rejects(attempt,/verification rejected/);await entered;
 const cleared=f.commit.clear();release();await rejected;await cleared;
 assert.equal(f.saved(),null);assert.equal(f.current(),null);assert.ok(!f.events.includes('restore'));assert.ok(!f.events.includes('publish'));
});

for (const boundary of ['read', 'write', 'verify', 'prepare'] as const) {
 test(`candidate cancellation during ${boundary} preserves previous viewer and retires only candidate`, async () => {
  const f=fixture();let valid=true;
  f.h.assertCandidateCurrent=()=>{if(!valid)throw new Error('candidate cancelled');};
  if(boundary==='read'){const read=f.storage.read;f.storage.read=async()=>{const value=await read();valid=false;return value;};}
  if(boundary==='write'){const write=f.storage.write;f.storage.write=async value=>{await write(value);if(value===f.candidate)valid=false;};}
  if(boundary==='verify')f.h.verify=async value=>{valid=false;return{viewer:value.session.viewer};};
  if(boundary==='prepare')f.h.prepareContext=async()=>{valid=false;};
  await assert.rejects(f.commit.commit(f.candidate,f.h),/candidate cancelled/);await tick();
  assert.equal(f.saved(),f.previous);assert.equal(f.current(),f.previous);
  assert.deepEqual(f.revoked,['candidate']);assert.ok(!f.events.includes('publish'));
 });
}

test('a tab whose own sign-in ended does not erase the profile another tab has chosen since',async()=>{
 // The browser's saved record is now the other tab's new profile.
 const mine=record('previous'),theirs=record('candidate');
 let saved:SelectedViewerRecord|null=theirs;
 const storage:SelectedViewerStorage={read:async()=>saved,write:async value=>{saved=value;}};
 await new ViewerSelectionCommit(storage).clear(mine.session);
 assert.equal(saved,theirs,'the newer sign-in stays saved for the next reload');
 // The same tab signing out of the sign-in that is saved removes it.
 await new ViewerSelectionCommit(storage).clear(theirs.session);
 assert.equal(saved,null);
});
test('clearing with no sign-in of its own, or over a record that cannot be read, removes the record',async()=>{
 let saved:SelectedViewerRecord|null=record('candidate');
 const storage:SelectedViewerStorage={read:async()=>saved,write:async value=>{saved=value;}};
 await new ViewerSelectionCommit(storage).clear();
 assert.equal(saved,null);
 saved=record('candidate');
 const unreadable:SelectedViewerStorage={read:async()=>{throw new Error('damaged');},write:async value=>{saved=value;}};
 await new ViewerSelectionCommit(unreadable).clear(record('previous').session);
 assert.equal(saved,null);
});
test('a store that can compare and remove in one step is asked to',async()=>{
 const asked:string[]=[];
 const storage:SelectedViewerStorage={read:async()=>{throw new Error('not used');},write:async()=>{throw new Error('not used');},clearIfFamily:async family=>{asked.push(family);}};
 await new ViewerSelectionCommit(storage).clear(record('previous').session);
 assert.deepEqual(asked,['family-previous']);
});
test('a refreshed token of the sign-in in use is the same family: neither the old nor a failed candidate is retired',async()=>{
 const rotated=(r:SelectedViewerRecord,token:string)=>selectedViewerRecord(r.serverUrl,{...r.session,accessToken:token,expiresAt:new Date(Date.now()+3600000).toISOString()});
 const f=fixture(),fresh=rotated(f.previous,'previous-rotated');
 await f.commit.commit(fresh,f.h);await tick();
 assert.equal(f.current(),fresh);assert.deepEqual(f.revoked,[]);
 const g=fixture(),failing=rotated(g.previous,'previous-rotated');g.h.verify=async()=>{throw new Error('offline');};
 await assert.rejects(g.commit.commit(failing,g.h),/offline/);await tick();
 assert.deepEqual(g.revoked,[]);assert.equal(g.current(),g.previous);
});
