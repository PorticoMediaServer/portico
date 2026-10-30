import {test} from 'node:test';
import assert from 'node:assert/strict';
import {parseLocalSession} from '../src/local-session.ts';
import {parseSelectedViewerRecord} from '../src/viewer-selection.ts';

const session=(expiresAt:string)=>({accessToken:'t'.repeat(43),expiresAt,viewer:{accountId:'a',profileId:'p',serverId:'s',authority:'local',role:'owner'},sessionFamilyId:'fam',tokenGeneration:'1',authorizationHorizon:expiresAt});
const past='2026-01-01T00:00:00Z';

test('a fresh session that has already expired is refused',()=>{
 assert.throws(()=>parseLocalSession(session(past)),{code:'invalid_current_session'});
});
test('a saved session that expired while the device was off can still be read, so it can be replaced',()=>{
 assert.equal(parseLocalSession(session(past),{},{stored:true}).viewer.accountId,'a');
 assert.equal(parseSelectedViewerRecord({version:1,serverUrl:'https://s.example',session:session(past)})?.session.sessionFamilyId,'fam');
 // Expiry is the only thing forgiven: a damaged record is still refused.
 assert.throws(()=>parseLocalSession({...session(past),sessionFamilyId:'bad id!'},{},{stored:true}));
});

// 23 Sep: both simulators came back signed out after the demo was down past the access-token
// lifetime. The saved record was fine; showing it at launch went through the fresh-session check.
test('a saved sign-in whose access token expired can be shown before it is verified',async()=>{
 const {ViewerService}=await import('../src/index.ts');
 const viewer=new ViewerService();
 assert.throws(()=>viewer.select('https://s.example',session(past) as never));
 viewer.select('https://s.example',session(past) as never,{stored:true});
 assert.equal(viewer.getSnapshot().session?.sessionFamilyId,'fam');
});
test('a saved record from an earlier client (no device or installation binding) reads back',()=>{
 const previous={version:1,serverUrl:'https://s.example',session:session(past)};
 const read=parseSelectedViewerRecord(JSON.parse(JSON.stringify(previous)));
 assert.equal(read?.session.viewer.profileId,'p');
 assert.equal(read?.session.installationId,undefined);
});
test('a saved record with fields this client does not know reads back instead of becoming unreadable',()=>{
 const later={version:1,serverUrl:'https://s.example',session:{...session(past),refreshExpiresAt:past,installationId:'inst_1',viewer:{...session(past).viewer,displayName:'Justin'}}};
 const read=parseSelectedViewerRecord(later);
 assert.equal(read?.session.installationId,'inst_1');
 assert.equal((read?.session as Record<string,unknown>).refreshExpiresAt,undefined);
 // A fresh server answer stays exact.
 assert.throws(()=>parseLocalSession({...session('2099-01-01T00:00:00Z'),refreshExpiresAt:past}));
});
