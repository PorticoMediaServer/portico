import {test} from 'node:test';
import assert from 'node:assert/strict';
import {noticeWords,type Notification} from '../src/notifications.ts';
import {createI18n,resolveRegion} from '../../i18n/src/index.ts';

const notice=(over:Partial<Notification>):Notification=>({id:'n',audience:'account-admin',severity:'warning',source:'dvr',category:'dvr.conflict',title:'Server words',body:'Server body',
 arguments:{},actions:[],dedupeKey:'d',revision:1,createdAt:1,updatedAt:1,expiresAt:2,readAt:null,archivedAt:null,read:false,archived:false,cursor:'1',...over});

test('recording notices read from the catalogue in the viewer language; others keep the server words',()=>{
 const gb=createI18n(resolveRegion(undefined,{locales:['en-GB']}));
 const late=noticeWords(notice({arguments:{title:'The News',reason:'tuner-in-use-live-late'}}),gb);
 assert.equal(late.title,'Recording started late');
 assert.match(late.body,/^The News started late.*programme is missing\.$/);
 const plain=noticeWords(notice({arguments:{title:'The News'}}),gb);
 assert.equal(plain.body,'Portico could not record The News because no tuner was free.');
 assert.deepEqual(noticeWords(notice({category:'download.finished'}),gb),{title:'Server words',body:'Server body'});
});
