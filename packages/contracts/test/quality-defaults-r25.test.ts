import test from 'node:test';
import assert from 'node:assert/strict';
import {schemas} from '../src/schemas.ts';
import {canonicalQualityLadder,canonicalMusicQualityLadder,type CreateDownloadPreparationsRequest,type CreateDownloadRequestRequest} from '../src/types.ts';
import {decode} from '../src/runtime.ts';

test('quality defaults r25: actual item and container request schemas preserve omission and the exact video/music quality union',()=>{
 const item:CreateDownloadPreparationsRequest={operationId:'op',mediaId:'item'};
 const container:CreateDownloadRequestRequest={operationId:'op',deviceId:'dev',target:{kind:'album',id:'album'},policy:{episodes:'all'}};
 for(const [name,body] of [['CreateDownloadPreparationsRequest',item],['CreateDownloadRequestRequest',container]] as const){
  const schema=schemas[name];
  assert.ok(!(schema.required as readonly string[]).includes('quality'));
  assert.equal(schema.properties.quality.default,undefined,'omission is resolved per admitted item, not by a global schema default');
  const allowed=[...new Set([...canonicalQualityLadder,...canonicalMusicQualityLadder].map(q=>q.id))];
  assert.deepEqual(schema.properties.quality.enum,allowed);
  assert.equal(allowed.length,12);
  for(const quality of allowed)assert.doesNotThrow(()=>decode(name,{...body,quality}));
  for(const quality of ['','invented-quality',42,false,{},[]])assert.throws(()=>decode(name,{...body,quality}));
  assert.doesNotThrow(()=>decode(name,body));
 }
 // Legacy open preparation objects omit optional nulls; the closed request
 // contract rejects null. Neither decoder inserts a quality default.
 assert.ok(!Object.hasOwn(decode('CreateDownloadPreparationsRequest',{...item,quality:null}) as object,'quality'));
 assert.throws(()=>decode('CreateDownloadRequestRequest',{...container,quality:null}));
});
