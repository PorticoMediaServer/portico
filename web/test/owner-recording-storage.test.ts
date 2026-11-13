import test from 'node:test';
import assert from 'node:assert/strict';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {RecordingStorageController} from '../src/screens/settings/server-livetv/recording/storage-controller.ts';
import {RecordingStorage} from '../src/screens/settings/server-livetv/recording/RecordingStorage.tsx';
const initial={folder:'/recordings',beforeMs:60000,afterMs:180000,floorBytes:10*1024**3,capBytes:0,keepDays:0,keepNewest:0,stopViewers:false};
const overview={folderOk:true,usedBytes:1024**3,freeOnDriveBytes:100*1024**3,freeBytes:90*1024**3,hoursLeft:30,keepRule:'Until deleted',upcomingBytes:0,upcomingHours:0};
test('storage saves only actual DVR fields under its captured ETag and shows real measurements',async()=>{
 let values=initial;const writes:any[]=[];
 const controller=new RecordingStorageController({async request<T>(path){return (path==='/v1/dvr'?overview:{values,etag:'"7"'}) as T;},async requestRaw(path,method,options){writes.push({path,method,...options});values={...values,...options?.body as object};return {status:200,headers:{},body:{values,etag:'"8"'}};}},'server',()=>true,()=>{});
 await controller.load();controller.edit({keepNewest:5});await controller.save();
 assert.deepEqual(writes[0].body,{keepNewest:5});assert.deepEqual(writes[0].headers,{'If-Match':'"7"'});
 controller.edit({keepDays:30});await controller.save();assert.deepEqual(writes[1].body,{keepDays:30,keepNewest:0});
 const html=renderToStaticMarkup(createElement(RecordingStorage,{controller}));assert.match(html,/Free on the drive/);assert.match(html,/About 30 hours/);assert.match(html,/Until deleted/);assert.doesNotMatch(html,/cannot be written/);controller.dispose();
});
test('an uncertain storage save reads back without replay, and locks edits if the read also fails',async()=>{
 let broken=false,reads=0,writes=0;
 const controller=new RecordingStorageController({async request<T>(path){reads++;if(broken)throw {status:503};return (path==='/v1/dvr'?overview:{values:initial,etag:'"7"'}) as T;},async requestRaw(){writes++;broken=true;throw {status:503};}},'server',()=>true,()=>{});
 await controller.load();controller.edit({keepDays:30});await controller.save();assert.equal(writes,1);assert.equal(controller.getSnapshot().uncertain,true);controller.edit({keepDays:90});assert.equal(controller.getSnapshot().draft?.keepDays,30);
 broken=false;await controller.load();assert.equal(controller.getSnapshot().uncertain,undefined);assert.equal(controller.getSnapshot().draft?.keepDays,0);const before=reads;controller.dispose();await controller.load();assert.equal(reads,before);assert.equal(writes,1);
});
