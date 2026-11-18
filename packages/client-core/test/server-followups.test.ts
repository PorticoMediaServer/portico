import {test} from 'node:test';
import assert from 'node:assert/strict';
import {parseStorageStatus,parseFFmpegValidation,storageStatusService} from '../src/storage-status.ts';
import {parseVersionDeletePreview} from '../src/administration.ts';

test('storage status reads destinations and validates reclamation facts',async()=>{
 const data={databasePath:'/state/db',cacheDirectory:'/cache',backupDirectory:'/backups',reclaimableBytes:4096,lastReclaimedBytes:2048};
 const out=parseStorageStatus(data);assert.equal(out.reclaimableBytes,4096);assert.throws(()=>parseStorageStatus({...data,reclaimableBytes:-1}));
 const calls:unknown[]=[];const service=storageStatusService(async <T>(...args:unknown[])=>{calls.push(args);return data as T});assert.deepEqual(await service.read(),out);assert.deepEqual(calls,[['/v1/admin/storage/status']]);
 assert.equal(parseFFmpegValidation({path:'/bad',state:'refused',inUse:false,reasonCode:'ffmpeg.validation.unreadable',missingFeatures:[],checkedAt:'2026-10-13T12:00:00Z'}).state,'refused');
});
test('a version preview binds exactly one file',()=>{
 const file={assetId:'version',path:'/film.mp4',bytes:10,present:true,shared:false};const target={itemId:'film',title:'Film',kind:'movie',libraryId:'films',libraryName:'Films',allowMediaDeletion:true,titles:1,files:[file],bytes:10,dependents:[]};
 const raw={assetId:'version',previewKey:'a'.repeat(64),revision:1,targets:[target],confirmation:'Film',confirmationKind:'title',totalBytes:10,totalFiles:1,totalBytesText:'10 B',blocked:[]};
 assert.equal(parseVersionDeletePreview(raw).assetId,'version');assert.throws(()=>parseVersionDeletePreview({...raw,assetId:'different'}));
});
