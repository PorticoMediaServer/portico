import test from 'node:test';import assert from 'node:assert/strict';
import {parseHardwareExecution,parseSessionDelivery,type HardwareExecution} from '../src/delivery.ts';

/** The P2-65 diagnostic addition: where each successful conversion start ran.
 * The fixture that fails software, unknown and fallback launches reads this
 * parser, so a value outside the published set must never become hardware. */

function base(){return {mode:'hls',strategy:'video_conversion',reasonCodes:['video_codec_requires_conversion'],policy:null,qualityId:'720p-high',
 hardware:{backend:'vaapi',stages:[{operation:'decode',execution:'hardware'},{operation:'scale',execution:'hardware'},{operation:'encode',execution:'hardware'}]},
 streams:[{kind:'video',inputCodec:'hevc',outputCodec:'h264',action:'convert'},{kind:'audio',inputCodec:'eac3',outputCodec:'aac',action:'convert'}],
 throttled:true,convertedThroughMs:126000,toneMap:false,targetDisplayHeight:720,targetVideoBitrateBps:4000000,targetAudioBitrateBps:128000,
 playedRetentionMs:300000,throttleBufferMs:60000};}

function execution(overrides:Record<string,unknown>={}){return {launchCount:2,softwareLaunchCount:0,unknownLaunchCount:0,fallbackCount:0,
 lastLaunch:{sequence:2,generation:1,startedAt:'2026-10-28T17:41:09.418Z',
  encoder:{name:'h264_vaapi',execution:'hardware'},decoder:{name:'hevc_vaapi',execution:'hardware'},renderNode:'/dev/dri/renderD128'},...overrides};}

test('a hardware execution block is read exactly as the server published it',()=>{
 const raw=base().hardware as Record<string,unknown>;
 const parsed=parseSessionDelivery({...base(),hardware:{...raw,execution:execution()}});
 const read=parsed.hardware.execution!;
 assert.equal(read.launchCount,2);
 assert.equal(read.softwareLaunchCount,0);
 assert.equal(read.unknownLaunchCount,0);
 assert.equal(read.fallbackCount,0);
 assert.equal(read.lastLaunch.encoder.name,'h264_vaapi');
 assert.equal(read.lastLaunch.decoder.execution,'hardware');
 assert.equal(read.lastLaunch.renderNode,'/dev/dri/renderD128');
 assert(Object.isFrozen(parsed.hardware)&&Object.isFrozen(read)&&Object.isFrozen(read.lastLaunch)&&Object.isFrozen(read.lastLaunch.encoder));
 // The launch fields the parser does not need to explain are unchanged.
 assert.equal(parsed.hardware.backend,'vaapi');
 assert.equal(parsed.hardware.stages.length,3);
 // A launch that needs no render node omits it, and the block still reads.
 const noNode=parseHardwareExecution({...execution(),lastLaunch:{...execution().lastLaunch,renderNode:undefined}}) as HardwareExecution;
 assert.equal(noNode.lastLaunch.renderNode,undefined);
 assert.equal(parseHardwareExecution({...execution(),lastLaunch:{...execution().lastLaunch,encoder:{name:'h264_videotoolbox',execution:'hardware'},renderNode:undefined}})!.lastLaunch.encoder.name,'h264_videotoolbox');
});

test('a viewer payload without execution stays a valid diagnostic',()=>{
 const parsed=parseSessionDelivery(base());
 assert.equal(parsed.hardware.execution,undefined);
 assert.equal(parsed.hardware.backend,'vaapi');
 assert.equal(parseHardwareExecution(undefined),undefined);
 assert.equal(parseHardwareExecution(null),undefined);
});

test('software and unknown launches are reported, never repaired into hardware',()=>{
 // Diagnostics must be able to say software; the fixture is what refuses it.
 assert.equal(parseHardwareExecution(execution({softwareLaunchCount:1,lastLaunch:{...execution().lastLaunch,encoder:{name:'libx264',execution:'software'}}}))!.lastLaunch.encoder.execution,'software');
 assert.equal(parseHardwareExecution(execution({unknownLaunchCount:2,lastLaunch:{...execution().lastLaunch,decoder:{name:'hevc',execution:'unknown'}}}))!.lastLaunch.decoder.execution,'unknown');
 for(const wrong of ['HARDWARE','gpu','auto','videotoolbox',true,1,null])assert.equal(parseHardwareExecution(execution({lastLaunch:{...execution().lastLaunch,encoder:{name:'h264_vaapi',execution:wrong}}})),undefined,`encoder execution ${String(wrong)}`);
 for(const wrong of ['HARDWARE','gpu','',false])assert.equal(parseHardwareExecution(execution({lastLaunch:{...execution().lastLaunch,decoder:{name:'hevc_vaapi',execution:wrong}}})),undefined,`decoder execution ${String(wrong)}`);
});

test('an execution block that does not fit is omitted on its own',()=>{
 const broken=execution();
 const cases:unknown[]=[
  execution({launchCount:0,lastLaunch:{...broken.lastLaunch,sequence:0}}),                    // successful Starts only
  execution({softwareLaunchCount:3}),                                                          // more than the launches
  execution({unknownLaunchCount:2,softwareLaunchCount:1}),                                     // subsets exceed the whole
  execution({fallbackCount:3}),
  execution({launchCount:-1}),
  execution({launchCount:2.5}),
  execution({launchCount:'2'}),
  execution({softwareLaunchCount:false}),
  execution({lastLaunch:{...broken.lastLaunch,sequence:3}}),                                    // beyond the launch set
  execution({lastLaunch:{...broken.lastLaunch,generation:-1}}),
  execution({lastLaunch:{...broken.lastLaunch,generation:'1'}}),
  execution({lastLaunch:{...broken.lastLaunch,startedAt:'2026-10-28 17:41:09'}}),
  execution({lastLaunch:{...broken.lastLaunch,startedAt:'2026-10-28T17:41:09'}}),                // no offset
  execution({lastLaunch:{...broken.lastLaunch,startedAt:'not a time'}}),
  execution({lastLaunch:{...broken.lastLaunch,startedAt:'2026-13-40T99:99:99Z'}}),
  execution({lastLaunch:{...broken.lastLaunch,encoder:{name:'',execution:'hardware'}}}),
  execution({lastLaunch:{...broken.lastLaunch,encoder:{name:' h264_vaapi',execution:'hardware'}}}),
  execution({lastLaunch:{...broken.lastLaunch,encoder:{name:'h264\x07vaapi',execution:'hardware'}}}),
  execution({lastLaunch:{...broken.lastLaunch,decoder:{name:'h'.repeat(65),execution:'hardware'}}}),
  execution({lastLaunch:{...broken.lastLaunch,decoder:null}}),
  execution({lastLaunch:{...broken.lastLaunch,renderNode:'/dev/dri/renderD128 '}}),
  execution({lastLaunch:{...broken.lastLaunch,renderNode:'r'.repeat(65)}}),
  execution({lastLaunch:undefined}),
  {lastLaunch:broken.lastLaunch},
  [],
  'hardware',
 ];
 for(const value of cases){
  assert.equal(parseHardwareExecution(value),undefined);
  // The rest of the diagnostic survives a block it cannot read.
  const raw=base().hardware as Record<string,unknown>;
  const parsed=parseSessionDelivery({...base(),hardware:{...raw,execution:value}});
  assert.equal(parsed.hardware.execution,undefined);
  assert.equal(parsed.hardware.backend,'vaapi');
  assert.equal(parsed.strategy,'video_conversion');
 }
});

test('the admin dashboard published execution fixture parses as written',()=>{
 // The same JSON the server's privacy test writes into playback_hls_stages.
 const raw=base().hardware as Record<string,unknown>;
 const parsed=parseSessionDelivery({...base(),hardware:{...raw,backend:'vaapi',stages:[{operation:'encode',execution:'hardware'}],execution:{
  launchCount:1,softwareLaunchCount:0,unknownLaunchCount:0,fallbackCount:0,
  lastLaunch:{sequence:1,generation:1,startedAt:'2026-10-28T00:00:00Z',
   encoder:{name:'h264_vaapi',execution:'hardware'},decoder:{name:'h264_vaapi',execution:'hardware'},renderNode:'/dev/dri/renderD128'}}}});
 assert.equal(parsed.hardware.execution!.launchCount,1);
 assert.equal(parsed.hardware.execution!.lastLaunch.encoder.name,'h264_vaapi');
 assert.equal(parsed.hardware.execution!.lastLaunch.decoder.execution,'hardware');
 assert.equal(parsed.hardware.execution!.lastLaunch.renderNode,'/dev/dri/renderD128');
});

test('a timestamp at the bounds of RFC 3339 is accepted',()=>{
 for(const startedAt of ['2026-10-28T17:41:09Z','2026-10-28T17:41:09.4Z','2026-10-28T17:41:09.418Z','2026-10-28T17:41:09+00:00','2026-10-28T17:41:09.418123456-04:00'])assert.ok(parseHardwareExecution(execution({lastLaunch:{...execution().lastLaunch,startedAt}})),startedAt);
});
