import test from 'node:test';
import assert from 'node:assert/strict';
import {RecordingRootController} from '../src/screens/settings/server-livetv/recording/root-controller.ts';
const initial={folder:'',beforeMs:60000,afterMs:180000,floorBytes:10*1024**3,capBytes:0,keepDays:0,keepNewest:0,stopViewers:false};
test('the recording folder loads its effective default and saves one DVR field under the read ETag',async()=>{
 let values=initial;const writes:any[]=[];
 const controller=new RecordingRootController({async request<T>(path){return (path.endsWith('/folder')?{folder:values.folder||'/home/owner/Videos/Recorded TV',state:'not_made'}:{values,etag:'"7"'}) as T;},async requestRaw(path,method,options){writes.push({path,method,...options});values={...values,...options?.body as object};return {status:200,headers:{},body:{values,etag:'"8"'}};}},()=>true,()=>{});
 await controller.load();assert.equal(controller.getSnapshot().folder?.folder,'/home/owner/Videos/Recorded TV');assert.equal(controller.dirty(),false);controller.choose('/media/recordings');await controller.save();assert.deepEqual(writes[0].body,{folder:'/media/recordings'});assert.deepEqual(writes[0].headers,{'If-Match':'"7"'});assert.equal(controller.getSnapshot().folder?.folder,'/media/recordings');assert.equal(controller.dirty(),false);controller.dispose();
});
test('a lost folder-save answer reads back without replay, and retirement prevents further reads or writes',async()=>{
 let saved=false,reads=0,writes=0,current=true;
 const controller=new RecordingRootController({async request<T>(path){reads++;return (path.endsWith('/folder')?{folder:saved?'/media/recordings':'/default',state:'ready'}:{values:{...initial,folder:saved?'/media/recordings':''},etag:saved?'"8"':'"7"'}) as T;},async requestRaw(){saved=true;writes++;throw {status:503};}},()=>current,()=>{});
 await controller.load();controller.choose('/media/recordings');await controller.save();assert.equal(writes,1);assert.equal(controller.getSnapshot().folder?.folder,'/media/recordings');assert.ok(controller.getSnapshot().error);assert.equal(controller.dirty(),false);const before=reads;current=false;await controller.load();controller.choose('/other');await controller.save();assert.equal(reads,before);assert.equal(writes,1);assert.equal(controller.getSnapshot().document,undefined);
});
