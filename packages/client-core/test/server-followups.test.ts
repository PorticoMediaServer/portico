import {test} from 'node:test';
import assert from 'node:assert/strict';
import {parseFFmpegValidation} from '../src/storage-status.ts';
import {parseVersionDeletePreview,parseLiveSourceChoicePage} from '../src/administration.ts';

test('FFmpeg validation reads a refused build',()=>{
 assert.equal(parseFFmpegValidation({path:'/bad',state:'refused',inUse:false,reasonCode:'ffmpeg.validation.unreadable',missingFeatures:[],checkedAt:'2026-10-13T12:00:00Z'}).state,'refused');
});
test('source choices keep true totals',()=>{
 assert.equal(parseLiveSourceChoicePage({sourceId:'source',generation:'generation',kind:'channels',total:100,items:[{id:'provider-key',name:'Channel',group:'News',channels:0}],nextCursor:'next'}).items[0].id,'provider-key');
});
test('a version preview binds exactly one file',()=>{
 const file={assetId:'version',path:'/film.mp4',bytes:10,present:true,shared:false};const target={itemId:'film',title:'Film',kind:'movie',libraryId:'films',libraryName:'Films',allowMediaDeletion:true,titles:1,files:[file],bytes:10,dependents:[]};
 const raw={assetId:'version',previewKey:'a'.repeat(64),revision:1,targets:[target],confirmation:'Film',confirmationKind:'title',totalBytes:10,totalFiles:1,totalBytesText:'10 B',blocked:[]};
 assert.equal(parseVersionDeletePreview(raw).assetId,'version');assert.throws(()=>parseVersionDeletePreview({...raw,assetId:'different'}));
});
