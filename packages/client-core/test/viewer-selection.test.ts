import test from 'node:test';
import assert from 'node:assert/strict';
import {selectedViewerRecord,parseSelectedViewerRecord} from '../src/viewer-selection.ts';

const record=(profile:string,token=profile)=>selectedViewerRecord('https://server.example',{
 accessToken:token,expiresAt:new Date(Date.now()+3600000).toISOString(),
 authorizationHorizon:new Date(Date.now()+7200000).toISOString(),sessionFamilyId:'family-'+token,tokenGeneration:'1',
 viewer:{accountId:'account',profileId:profile,serverId:'server',authority:'local',role:'member'},
});

test('selected viewer records reject ambiguous envelopes and credential-bearing server URLs',()=>{
 const previous=record('previous');assert.deepEqual(parseSelectedViewerRecord(previous),previous);
 for(const server of ['https://user:password@server.example','https://server.example/?token=x','https://server.example/#x','file:///tmp/server'])assert.throws(()=>selectedViewerRecord(server,previous.session));
 assert.throws(()=>parseSelectedViewerRecord({...previous,version:0}));
 assert.throws(()=>selectedViewerRecord(previous.serverUrl,{...previous.session,accountToken:'not-a-viewer-token'}));
});
