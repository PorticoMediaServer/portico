import test from 'node:test';
import assert from 'node:assert/strict';
import {parseDashboardSessionFacts,ActivitySummaryContractError} from '../src/admin/activity-summary/parser.ts';
const hardware=()=>({backend:'vaapi',stages:[{operation:'decode',execution:'hardware'},{operation:'encode',execution:'hardware'}],execution:{launchCount:1,softwareLaunchCount:0,unknownLaunchCount:0,fallbackCount:0,lastLaunch:{sequence:1,generation:1,startedAt:'2026-10-28T00:00:00Z',encoder:{name:'h264_vaapi',execution:'hardware'},decoder:{name:'h264_vaapi',execution:'hardware'},renderNode:'/dev/dri/renderD128'}}});
test('strict dashboard parser retains the admin-only conversion diagnostic',()=>{
 const raw={delivery:{streams:[],hardware:hardware()}}, parsed=parseDashboardSessionFacts(raw);
 assert.deepEqual(parsed,raw);
 assert.equal(Object.isFrozen((parsed.delivery as any).hardware.execution),true);
 assert.deepEqual(parseDashboardSessionFacts({delivery:{streams:[]}}),{delivery:{streams:[]}});
});
test('dashboard hardware diagnostic rejects malformed execution and unknown fields',()=>{
 for(const bad of [null,{}, {...hardware(),unexpected:true},{...hardware(),execution:{...hardware().execution,launchCount:-1}},{...hardware(),execution:{...hardware().execution,lastLaunch:{...hardware().execution.lastLaunch,encoder:{name:'libx264',execution:'auto'}}}}]){
  assert.throws(()=>parseDashboardSessionFacts({delivery:{streams:[],hardware:bad}}),ActivitySummaryContractError);
 }
});
