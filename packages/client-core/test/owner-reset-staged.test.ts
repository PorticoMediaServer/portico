import test from 'node:test';
import assert from 'node:assert/strict';
import {parseDirectSignIn,type ProfileAPI} from '../src/profile-management.ts';
import {ownerResetSecurity,finishOwnerResetPassword} from '../src/account-reset.ts';

function grant(role='owner'){
 return parseDirectSignIn({authority:'local',serverId:'server',account:{id:'account',username:'actual_owner',primaryProfileId:'profile',role,revision:1,disabled:false,allowedLibraries:[]},profiles:[{id:'profile',name:'Owner',primary:true,position:0,art:'blue',revision:1,pinRevision:1,pinRequired:false}],canManage:false,capabilities:{manageAccount:false,managePeople:false,manageServer:false,manageSignIn:false},passwordChangeRequired:true,session:{accessToken:'a'.repeat(43),deviceId:'device',installationId:'d297a1dc-3adc-4b1c-a7f2-fbd4640f69e8',expiresAt:'2099-01-01T00:00:00Z',sessionFamilyId:'reset_family',tokenGeneration:'1',authorizationHorizon:'2099-01-01T00:00:00Z',viewer:{authority:'local',serverId:'server',accountId:'account',profileId:'profile',role:'account'}}});
}

test('post-proof conditional state uses actual owner and existing bounded security read',async()=>{
 const calls:unknown[]=[];const api:ProfileAPI={request:async<T>(path:string,method?:string,body?:unknown)=>{calls.push([path,method,body]);return {enabled:true,pendingEnrolment:false,recoveryCodesRemaining:7} as T;}};
 assert.deepEqual(await ownerResetSecurity(api,grant()),{username:'actual_owner',twoStepEnabled:true});
 assert.deepEqual(calls,[['/v1/direct/two-factor','GET',undefined]]);
 await assert.rejects(ownerResetSecurity(api,grant('member')));
 assert.equal(calls.length,1,'unsupported grant never queries security');
});

test('final choice is one existing password write, without reusing the spent file code',async()=>{
 const calls:unknown[]=[];const api:ProfileAPI={request:async<T>(path:string,method?:string,body?:unknown)=>{calls.push([path,method,body]);return undefined as T;}};
 const password=['new','password','1!'].join('-');
 await finishOwnerResetPassword(api,grant(),password,true);
 assert.deepEqual(calls,[['/v1/direct/password','POST',{currentPassword:'',newPassword:password,code:'',turnOffTwoStep:true}]]);
 await assert.rejects(finishOwnerResetPassword(api,grant('member'),password,true));
 assert.equal(calls.length,1);
});
