import test from 'node:test';
import assert from 'node:assert/strict';
import {ownerLiveDefaultsForm} from '../src/server-admin/live-defaults.ts';
const values={bufferSeconds:8,retrySeconds:60,rewindMinutes:45,guideDays:7,importImages:true,discoveryEnabled:true,userAgent:''};
test('Live defaults read and patch the rebuilt document with the displayed ETag and actual units',async()=>{
 let change:any;const form=ownerLiveDefaultsForm({async request<T>(path,method){assert.equal(path,'/v1/admin/settings/live');assert.equal(method,'GET');return {values,etag:'"7"'} as T;},async requestRaw(path,method,options){assert.equal(path,'/v1/admin/settings/live');assert.equal(method,'PUT');assert.deepEqual(options?.headers,{'If-Match':'"7"'});change=options?.body;return {status:200,headers:{},body:{values:{...values,...change},etag:'"8"'}};}});
 await form.load();assert.equal(form.getSnapshot().draft?.rewindMinutes,45);form.set({bufferSeconds:20,retrySeconds:180,userAgent:'Provider-client'});await form.save();assert.deepEqual(change,{bufferSeconds:20,retrySeconds:180,userAgent:'Provider-client'});assert.equal(form.getSnapshot().dirty,false);assert.equal(form.getSnapshot().status?.etag,'"8"');form.dispose();
});
