import test from 'node:test';
import assert from 'node:assert/strict';
import {parsePersistedRemoteConfiguration} from '../src/linear-api.ts';
const remote={kind:'xtream',guideUrl:'https://guide.example/guide.xml',guideUrlRedacted:true,refreshMs:21600000,ownerLimit:4,useDiscoveredCapacity:false};
test('persisted acquisition projection reads every editable field without retaining private fields',()=>{
 const value=parsePersistedRemoteConfiguration({...remote,username:'secret-user',password:'secret-pass',futureSecret:'opaque'});
 assert.deepEqual(value,remote);assert(Object.isFrozen(value));assert(!JSON.stringify(value).includes('secret'));
});
