import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdirSync,mkdtempSync,writeFileSync,rmSync} from 'node:fs';
import {join} from 'node:path';
import {tmpdir} from 'node:os';
import {freeze,verify} from '../../../scripts/compat-corpus/freeze.mjs';
test('release snapshot refuses replacement, changed bytes and missing inventory',()=>{
 const scratch=mkdtempSync(join(tmpdir(),'portico-freeze-'));
 try {
  for(const path of ['server/api','packages/contracts/src','packages/client-core/test/compat-corpus/main'])mkdirSync(join(scratch,path),{recursive:true});
  writeFileSync(join(scratch,'server/api/portico.openapi.json'),'{}');
  writeFileSync(join(scratch,'packages/contracts/src/runtime.ts'),'export const version=1;');
  writeFileSync(join(scratch,'packages/client-core/test/compat-corpus/main/manifest.json'),'{}');
  const out=join(scratch,'snapshot'),pin='a'.repeat(40);
  freeze(scratch,out,'1.0.0',pin,pin);
  assert.equal(verify(out).serverCommit,pin);
  assert.throws(()=>freeze(scratch,out,'1.0.0',pin,pin),/exists/);
  writeFileSync(join(out,'server/api/portico.openapi.json'),'{"changed":true}');
  assert.throws(()=>verify(out),/evidence changed/);
  writeFileSync(join(out,'extra'),'unreviewed');
  assert.throws(()=>verify(out),/inventory changed/);
 } finally {rmSync(scratch,{recursive:true,force:true});}
});
