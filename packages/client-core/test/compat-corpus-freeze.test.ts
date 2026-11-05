import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdirSync,mkdtempSync,writeFileSync,rmSync,existsSync} from 'node:fs';
import {join} from 'node:path';
import {tmpdir} from 'node:os';
import {freeze,verify} from '../../../scripts/compat-corpus/freeze.mjs';
test('release snapshot refuses replacement, changed bytes and missing inventory',()=>{
 const scratch=mkdtempSync(join(tmpdir(),'portico-freeze-'));
 try {
  for(const path of ['server/api','packages/contracts/src','packages/client-core/test/compat-corpus/main'])mkdirSync(join(scratch,path),{recursive:true});
  writeFileSync(join(scratch,'server/api/portico.openapi.json'),'{}');
  writeFileSync(join(scratch,'packages/contracts/src/runtime.ts'),'export const version=1;');
  const pin='a'.repeat(40);
  const provenance={recordedFrom:{sourceRevision:pin,build:{sourceRevision:pin,modified:false,binaryHash:'b'.repeat(64)}}};
  const corpus=join(scratch,'packages/client-core/test/compat-corpus/main');
  writeFileSync(join(corpus,'manifest.json'),JSON.stringify(provenance));
  const out=join(scratch,'snapshot');
  assert.throws(()=>freeze(scratch,out,'1.0.0','c'.repeat(40),pin),/recorded producer/);
  assert.equal(existsSync(out),false,'stale recording must not create an archive');
  for (const build of [{...provenance.recordedFrom.build,sourceRevision:'d'.repeat(40)}, {...provenance.recordedFrom.build,modified:true}, undefined]) {
    writeFileSync(join(corpus,'manifest.json'),JSON.stringify({recordedFrom:{sourceRevision:pin,build}}));
    assert.throws(()=>freeze(scratch,out,'1.0.0',pin,pin),/build provenance/);
    assert.equal(existsSync(out),false,'invalid build must not create an archive');
  }
  writeFileSync(join(corpus,'manifest.json'),JSON.stringify(provenance));
  freeze(scratch,out,'1.0.0',pin,pin);
  assert.equal(verify(out).serverCommit,pin);
  const external=join(scratch,'external-corpus');mkdirSync(external);
  writeFileSync(join(external,'manifest.json'),JSON.stringify(provenance));
  writeFileSync(join(external,'fresh.json'),'{"status":200}');
  const externalOut=join(scratch,'external-snapshot');
  freeze(scratch,externalOut,'1.0.0',pin,pin,external);
  assert.ok(verify(externalOut).files['packages/client-core/test/compat-corpus/main/fresh.json']);

  assert.throws(()=>freeze(scratch,out,'1.0.0',pin,pin),/exists/);
  writeFileSync(join(out,'server/api/portico.openapi.json'),'{"changed":true}');
  assert.throws(()=>verify(out),/evidence changed/);
  writeFileSync(join(out,'extra'),'unreviewed');
  assert.throws(()=>verify(out),/inventory changed/);
 } finally {rmSync(scratch,{recursive:true,force:true});}
});
