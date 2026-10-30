import {test} from 'node:test';
import assert from 'node:assert/strict';
import {parseStorageStatus,parseFFmpegValidation,storageStatusService} from '../src/storage-status.ts';
import {parseSessionControls,playbackControlsService} from '../src/playback-controls.ts';
import {parseVersionDeletePreview,parseLiveSourceChoicePage} from '../src/administration.ts';
import {parseMetadataAttentionCounts} from '../src/metadata-attention.ts';

test('storage status reads destinations and validates reclamation facts',async()=>{
 const data={databasePath:'/state/db',cacheDirectory:'/cache',backupDirectory:'/backups',reclaimableBytes:4096,lastReclaimedBytes:2048};
 const out=parseStorageStatus(data);assert.equal(out.reclaimableBytes,4096);assert.throws(()=>parseStorageStatus({...data,reclaimableBytes:-1}));
 const calls:unknown[]=[];const service=storageStatusService(async <T>(...args:unknown[])=>{calls.push(args);return data as T});assert.deepEqual(await service.read(),out);assert.deepEqual(calls,[['/v1/admin/storage/status']]);
 assert.equal(parseFFmpegValidation({path:'/bad',state:'refused',inUse:false,reasonCode:'ffmpeg.validation.unreadable',missingFeatures:[],checkedAt:'2026-10-13T12:00:00Z'}).state,'refused');
});
test('administrator controls carry the session revision and no media URL',async()=>{
 const control={sessionId:'session',revision:'3',state:'paused',positionMs:9000,actions:['pause','resume','seek']};const calls:unknown[]=[];
 const service=playbackControlsService(async <T>(...args:unknown[])=>{calls.push(args);return control as T});await service.controlAdmin(parseSessionControls(control),'seek',12000);
 assert.deepEqual(calls,[['/v1/admin/sessions/session/controls','POST',{action:'seek',expectedRevision:'3',positionMs:12000}]]);assert.throws(()=>parseSessionControls({...control,revision:3}));
});
test('attention counts and source choices keep true totals',()=>{
 assert.equal(parseMetadataAttentionCounts({total:57,libraries:[{libraryId:'films',total:57}]}).total,57);
 assert.equal(parseLiveSourceChoicePage({sourceId:'source',generation:'generation',kind:'channels',total:100,items:[{id:'provider-key',name:'Channel',group:'News',channels:0}],nextCursor:'next'}).items[0].id,'provider-key');
});
test('a version preview binds exactly one file',()=>{
 const file={assetId:'version',path:'/film.mp4',bytes:10,present:true,shared:false};const target={itemId:'film',title:'Film',kind:'movie',libraryId:'films',libraryName:'Films',allowMediaDeletion:true,titles:1,files:[file],bytes:10,dependents:[]};
 const raw={assetId:'version',previewKey:'a'.repeat(64),revision:1,targets:[target],confirmation:'Film',confirmationKind:'title',totalBytes:10,totalFiles:1,totalBytesText:'10 B',blocked:[]};
 assert.equal(parseVersionDeletePreview(raw).assetId,'version');assert.throws(()=>parseVersionDeletePreview({...raw,assetId:'different'}));
});
