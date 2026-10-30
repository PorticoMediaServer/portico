import test from 'node:test';
import assert from 'node:assert/strict';
import {parseScanIssues,parseInventoryChanges,readScanIssues} from '../src/scan-issues.ts';
const issue={sequence:4,line:2,path:'Movies/.plexignore',reason:'invalid_ignore_pattern',messageKey:'scan.issue.invalid_ignore_pattern',warning:true};
test('owner diagnostics preserve the path and line and reject broken pagination',()=>{
 const parsed=parseScanIssues({jobId:'job',items:[issue],nextSequence:4,hasMore:false},'job');assert.equal(parsed.items[0].line,2);assert(Object.isFrozen(parsed.items));
 assert.throws(()=>parseScanIssues({jobId:'other',items:[issue],nextSequence:4,hasMore:false},'job'));
 assert.throws(()=>parseScanIssues({jobId:'job',items:[issue,issue],nextSequence:4,hasMore:false},'job'));
 assert.throws(()=>parseScanIssues({jobId:'job',items:[issue],nextSequence:3,hasMore:false},'job'));
});
test('an expired change cursor is an explicit reset, including an empty feed',()=>{
 assert.equal(parseInventoryChanges({sourceId:'source',items:[],nextSequence:1,oldestSequence:0,hasMore:false,resetRequired:true},'source').resetRequired,true);
});
test('the diagnostics service binds both library and job',async()=>{
 let path='';const api={async request<T>(requested:string){path=requested;return {jobId:'job',items:[issue],nextSequence:4,hasMore:false} as T;}};
 await readScanIssues(api,'library','job',2);assert.equal(path,'/v1/admin/libraries/library/scans/job/issues?after=2&limit=128');
});
