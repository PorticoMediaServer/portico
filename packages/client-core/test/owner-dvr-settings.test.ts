import test from 'node:test';
import assert from 'node:assert/strict';
import {ownerRecordingDefaultsForm} from '../src/server-admin/owner-dvr-settings.ts';
const values={folder:'',beforeMs:60000,afterMs:180000,floorBytes:10*1024**3,capBytes:0,keepDays:0,keepNewest:0,stopViewers:false};
test('recording defaults patch actual milliseconds and stop-viewers fields with the displayed document ETag',async()=>{
 let change:any;const form=ownerRecordingDefaultsForm({async request<T>(path){assert.equal(path,'/v1/admin/settings/dvr');return {values,etag:'"7"'} as T;},async requestRaw(path,method,options){assert.equal(path,'/v1/admin/settings/dvr');assert.equal(method,'PUT');assert.deepEqual(options?.headers,{'If-Match':'"7"'});change=options?.body;return {status:200,headers:{},body:{values:{...values,...change},etag:'"8"'}};}});
 await form.load();form.set({beforeMs:300000,afterMs:600000,stopViewers:true});await form.save();assert.deepEqual(change,{beforeMs:300000,afterMs:600000,stopViewers:true});assert.equal(form.getSnapshot().draft?.floorBytes,values.floorBytes);assert.equal(form.getSnapshot().dirty,false);form.dispose();
});
