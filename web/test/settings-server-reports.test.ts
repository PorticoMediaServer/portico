import test from 'node:test';
import assert from 'node:assert/strict';
import {SettingsReportsSource} from '../src/screens/settings/server/troubleshooting/reports-source.ts';
const report=(id:string)=>({id,reporter:'Owner',kind:'crash',status:'open',message:'Stopped',hasLog:true,deviceName:'Phone',createdAt:'2026-11-13T12:00:00Z',updatedAt:'2026-11-13T12:00:00Z'});
const skipped={libraryId:'lib_one',libraryName:'Films',path:'/films/one.mkv',reason:'unreadable',message:'The file could not be read.'};
function fixture(){
 const calls:{path:string;method:string}[]=[];let failing=false,deleted=false;
 const transport={async request<T>(path:string,method='GET'){calls.push({path,method});if(failing)throw new Error('down');if(method==='DELETE'){deleted=true;return undefined as T;}if(path.startsWith('/v1/admin/skipped'))return {items:[skipped],nextCursor:path.includes('cursor=')?undefined:'skip-next'} as T;if(path.startsWith('/v1/admin/logs'))return {items:[{time:'2026-11-13T12:00:00Z',level:'info',kind:'server',message:'Ready'}]} as T;if(path.startsWith('/v1/admin/audit'))return {items:[{at:'2026-11-13T12:00:00Z',actor:'Owner',action:path.includes('cursor=')?'second':'first'}],nextCursor:path.includes('cursor=')?undefined:'9'} as T;if(path.endsWith('/one'))return report('one') as T;return {items:deleted?[]:[report(path.includes('cursor=')?'two':'one')],nextCursor:deleted||path.includes('cursor=')?undefined:'next-page'} as T;},async readReportLog(id:string){calls.push({path:`/v1/admin/reports/${id}/log`,method:'GET'});return 'a private log naming a title';}};
 return {source:new SettingsReportsSource(transport,'server','viewer',()=>true),calls,fail:()=>{failing=true;},recover:()=>{failing=false;}};
}
test('App logs read real crash reports, runtime messages and skipped files with their cursors',async()=>{
 const f=fixture();await f.source.load();assert.equal(f.source.getSnapshot().phase,'ready');assert.equal(f.calls[0].path,'/v1/admin/reports?kind=crash');assert.deepEqual(f.source.getSnapshot().setAside?.items,[skipped]);assert.equal(f.source.getSnapshot().records?.items[0].message,'Ready');
 await f.source.more();assert.deepEqual(f.source.getSnapshot().reports.map(row=>row.id),['one','two']);const count=f.calls.length;await f.source.more();assert.equal(f.calls.length,count);
 await f.source.checkSetAside(true);assert.match(f.calls.at(-1)!.path,/skipped\?cursor=skip-next$/);f.source.dispose();
});
test('one report is read fresh and its consented plain-text log stays out of the shared snapshot',async()=>{
 const f=fixture();await f.source.load();const value=await f.source.read('one',new AbortController().signal);assert.equal(value.log,'a private log naming a title');assert.doesNotMatch(JSON.stringify(f.source.getSnapshot()),/private log/);await assert.rejects(f.source.read('not-listed',new AbortController().signal));f.source.dispose();
});
test('only a listed report can be deleted; the server list is reread afterwards',async()=>{
 const f=fixture();await f.source.load();assert.equal(await f.source.remove('not-listed'),false);assert.equal(await f.source.remove('one'),true);assert.equal(f.calls.filter(call=>call.method==='DELETE').length,1);assert.deepEqual(f.source.getSnapshot().reports,[]);f.source.dispose();
});
test('failed reads can be retried and a retired scope does no further I/O',async()=>{
 const f=fixture();f.fail();await f.source.load();assert.equal(f.source.getSnapshot().phase,'error');f.recover();await f.source.retry();assert.equal(f.source.getSnapshot().phase,'ready');f.source.dispose();const count=f.calls.length;await f.source.load();assert.equal(f.calls.length,count);assert.deepEqual(f.source.getSnapshot(),{phase:'idle',reports:[]});
});
test('activity pages use the actual actor/action facts and Older only follows the server cursor',async()=>{
 const f=fixture();await f.source.load();await f.source.selectLane('audit');assert.equal(f.source.getSnapshot().records?.items[0].actor,'Owner');await f.source.moreRecords();assert.deepEqual(f.source.getSnapshot().records?.items.map(row=>row.message),['first','second']);const count=f.calls.length;await f.source.moreRecords();assert.equal(f.calls.length,count);await f.source.latestRecords();assert.deepEqual(f.source.getSnapshot().records?.items.map(row=>row.message),['first']);f.source.dispose();
});
