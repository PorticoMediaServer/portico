import test from 'node:test';
import assert from 'node:assert/strict';
import {RecordingPermissionsController} from '../src/screens/settings/server-livetv/recording/permissions-controller.ts';
const member={accountId:'usr_member',accountName:'Member',profileId:'pro_one',profileName:'One',authority:'local',accountAllowed:false,profileAllowed:true,liveAllowed:true,canRecord:false,ownerAccount:false,accountRevision:7,revision:1700000000000};
test('recording account and profile switches save the read revisions to the rebuilt route',async()=>{
 let row=member;const writes:any[]=[];
 const controller=new RecordingPermissionsController({async request<T>(path){assert.equal(path,'/v1/admin/dvr/permissions');return {items:[row]} as T;},async requestRaw(path,method,options){writes.push({path,method,...options});const body=options?.body as any;row=body.profileId?{...row,profileAllowed:body.enabled,canRecord:body.enabled}:{...row,accountAllowed:body.enabled,canRecord:body.enabled};return {status:200,headers:{},body:{items:[row]}};}},'server',()=>true,()=>{});
 await controller.load();await controller.change(controller.getSnapshot().owners[0]!,true,true);
 assert.deepEqual(writes[0].body,{accountId:member.accountId,enabled:true,revision:7});assert.equal(controller.getSnapshot().grants[0]?.enabled,true);
 await controller.change(controller.getSnapshot().owners[0]!,false);assert.deepEqual(writes[1].body,{accountId:member.accountId,profileId:member.profileId,enabled:false,revision:member.revision});assert.equal(controller.getSnapshot().grants[0]?.enabled,false);controller.dispose();
});
test('an unknown permission-save result reads back and never retries the write',async()=>{
 let reads=0,writes=0,broken=false;
 const controller=new RecordingPermissionsController({async request<T>(){reads++;if(broken)throw {status:503};return {items:[member]} as T;},async requestRaw(){writes++;broken=true;throw {status:503};}},'server',()=>true,()=>{});
 await controller.load();await controller.change(controller.getSnapshot().owners[0]!,true,true);assert.equal(writes,1);assert.equal(controller.getSnapshot().uncertain,true);await controller.change(controller.getSnapshot().owners[0]!,true,true);assert.equal(writes,1);broken=false;await controller.load();assert.equal(controller.getSnapshot().uncertain,undefined);const before=reads;controller.dispose();await controller.load();assert.equal(reads,before);
});
