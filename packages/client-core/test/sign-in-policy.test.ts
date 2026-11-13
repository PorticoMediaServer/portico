import test from 'node:test';
import assert from 'node:assert/strict';
import {readOwnerSignInConfiguration,signInPolicyRows,isSignInPolicyRow,signInPolicyValues,signInPolicyWith,patchSignInPolicy,signInPolicyConflict} from '../src/sign-in-policy.ts';
import {readSettingsBinding,settingsBinding} from '../src/presentation/settings-bindings.ts';

const document={name:'sign-in',etag:'"7"',values:{staySignedInDays:30,pinRememberDays:0,requireTwoStep:'admins'},defaults:{staySignedInDays:90,pinRememberDays:30,requireTwoStep:'nobody'}};
const api={request:async<T>(path:string)=>path==='/v1/admin/settings/sign-in'?document as T:{dataFolder:'/fixture/data'} as T};

test('rebuilt Sign-in reads only supported policy rows',async()=>{
 const calls:string[]=[];
 const config=await readOwnerSignInConfiguration({request:async<T>(path:string,...rest:unknown[])=>{calls.push(path);return api.request<T>(path);}});
 assert.deepEqual(calls,['/v1/admin/settings/sign-in']);
 assert.deepEqual(Object.keys(signInPolicyRows).sort(),['signin.pinRemember','signin.requireTwoStep','signin.stayLength']);
 assert.equal(isSignInPolicyRow('signin.passwordAttempts'),false);
 assert.equal(config.signInPolicyRevision,'"7"');
 const values=signInPolicyValues(config)!;
 assert.deepEqual(values,{staySignedInDays:30,pinRememberDays:0,requireTwoStep:'administrators'});
 assert.deepEqual(signInPolicyWith(values,'signin.stayLength','365'),{...values,staySignedInDays:365});
 assert.throws(()=>signInPolicyWith(values,'signin.stayLength','31'));
 for(const id of ['signin.passwordAttempts','signin.pinAttempts','signin.passwordLength','signin.resetCodeLife','signin.rememberAccounts','signin.endedKeep']){
  assert.equal(readSettingsBinding(settingsBinding(id)!,{configuration:config},id),undefined);
 }
});

test('policy writes name the read revision and translate the existing administrator choice',async()=>{
 const config=await readOwnerSignInConfiguration(api),values=signInPolicyValues(config)!;
 let sent:unknown[]=[];
 await patchSignInPolicy({...api,requestRaw:async(...args:any[])=>{sent=args;return {status:200,headers:{},body:document};}},config.signInPolicyRevision,values);
 assert.deepEqual(sent.slice(0,2),['/v1/admin/settings/sign-in','PUT']);
 assert.deepEqual((sent[2] as any).headers,{'If-Match':'"7"'});
 assert.deepEqual((sent[2] as any).body,{staySignedInDays:30,pinRememberDays:0,requireTwoStep:'admins'});
 await assert.rejects(patchSignInPolicy(api,config.signInPolicyRevision,values),{code:'unsupported'});
 await assert.rejects(patchSignInPolicy({...api,requestRaw:async()=>({status:412,headers:{},body:{error:{code:'changed_elsewhere',message:'Read again.'}}})},config.signInPolicyRevision,values),{code:'changed_elsewhere'});
 assert.equal(signInPolicyConflict({code:'changed_elsewhere'}),true);
 assert.equal(signInPolicyConflict({status:412}),true);
});

test('a malformed or missing fact cannot become a guessed setting',async()=>{
 await assert.rejects(readOwnerSignInConfiguration({request:async<T>(path:string)=>path.endsWith('/sign-in')?{...document,values:{}} as T:{dataFolder:'/fixture/data'} as T}));
 assert.equal(signInPolicyValues(undefined),undefined);
 const config=await readOwnerSignInConfiguration(api);
 assert.equal(signInPolicyValues({...config,fields:config.fields.filter(f=>f.id!=='signin.pinRemember')}),undefined);
});

// Portico Accounts own two-step; its absent row must not prevent the other saves.
test('Portico sign-in saves applicable rows without sending its account-owned two-step policy',async()=>{
 const config=await readOwnerSignInConfiguration({request:async<T>(path:string)=>path.endsWith('/sign-in')?{...document,notApplicable:{requireTwoStep:'Set on each Portico Account.'}} as T:{dataFolder:'/fixture/data'} as T});
 const values=signInPolicyValues(config)!;
 assert.deepEqual(values,{staySignedInDays:30,pinRememberDays:0});
 let body:unknown;
 await patchSignInPolicy({...api,requestRaw:async(_path,_method,options)=>{body=options?.body;return {status:200,headers:{},body:document};}},config.signInPolicyRevision,signInPolicyWith(values,'signin.stayLength','90'));
 assert.deepEqual(body,{staySignedInDays:90,pinRememberDays:0});
});
