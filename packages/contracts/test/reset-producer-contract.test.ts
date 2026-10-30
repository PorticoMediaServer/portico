import test from 'node:test';
import assert from 'node:assert/strict';
import {execFileSync} from 'node:child_process';
import {mkdtempSync,readFileSync,rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {decode} from '../src/runtime.ts';
import {parseDirectSignIn} from '../../client-core/src/profile-management.ts';
import {parseResetFileLocation} from '../../client-core/src/account-reset.ts';

test('actual reset HTTP producers satisfy generated schemas and client authority parser',()=>{
 const directory=mkdtempSync(join(tmpdir(),'portico-reset-contract-'));
 try{
  const path=join(directory,'responses.json');
  execFileSync('go',['test','./internal/httpapi','-run','^TestResetFileHTTPProducerContractsAndAuthority$','-count=1'],{cwd:fileURLToPath(new URL('../../../server/',import.meta.url)),env:{...process.env,PORTICO_TEST_RESET_RESPONSES:path},timeout:120_000,stdio:'pipe'});
  const responses=JSON.parse(readFileSync(path,'utf8'));
  for(const mode of ['local','portico']){
   const result=decode('PostAuthOwnerResetVerifyResponse',responses[mode]);
   const parsed=parseDirectSignIn(result);assert.equal(parsed.kind,'signed-in');if(parsed.kind!=='signed-in')throw new Error('wrong state');
   assert.equal(parsed.passwordChangeRequired,mode==='local');assert.equal(!!parsed.resetFileOwnerSession,mode==='portico');assert.equal(parsed.session.refreshToken,undefined);
  }
  for(const bad of [{...responses.local,session:undefined},{...responses.local,passwordChangeRequired:undefined},{...responses.portico,resetFileOwnerSession:undefined},{...responses.portico,session:{...responses.portico.session,refreshToken:'x'.repeat(43)}}])assert.throws(()=>decode('PostAuthOwnerResetVerifyResponse',bad));
  const location=parseResetFileLocation(decode('PostAuthOwnerResetRequestResponse',responses.location));assert.equal(location.fileName,'reset-code.txt');
  assert.throws(()=>parseResetFileLocation({...responses.location,code:'private'}));
  assert.throws(()=>parseDirectSignIn({...responses.local,resetFileOwnerSession:true}));
  assert.throws(()=>parseDirectSignIn({...responses.portico,resetFileOwnerSession:true,session:{...responses.portico.session,refreshToken:'x'.repeat(43)}}));
  assert.throws(()=>parseDirectSignIn({...responses.portico,resetFileOwnerSession:true,account:{...responses.portico.account,role:'member'}}));
 }finally{rmSync(directory,{recursive:true,force:true})}
});
