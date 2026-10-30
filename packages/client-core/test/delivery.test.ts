import {canonicalQualityLadder} from '../../contracts/src/types.ts';
import test from 'node:test';import assert from 'node:assert/strict';
import {parseResolvedDeliveryPolicy,parseSessionDelivery,parseQualityRungs,parseDeliveryPolicyDocument,normalizeTransportClass,transportClassHeader,deviceClassHeader,networkClasses,transportClasses} from '../src/delivery.ts';
import {HttpLocalApi} from '../src/index.ts';

function policy(overrides:Record<string,unknown>={}){return {networkClass:'remote',serverLocality:'remote',transportClass:'cellular',preferenceLane:'remote',directPlay:'prefer',directStream:'allow',transcode:'allow',qualityMode:'720p-high',maxVideoBitrateBps:8000000,maxAudioBitrateBps:256000,maxVideoHeight:720,allowHDR:false,planningPolicy:'maximum_fidelity',clamps:[{field:'maxVideoBitrateBps',source:'server_clamp',requested:8000000,applied:4000000}],...overrides};}

test('a resolved policy is accepted only in the shape the server publishes',()=>{
 const parsed=parseResolvedDeliveryPolicy(policy());
 assert.equal(parsed.networkClass,'remote');
 assert.equal(parsed.qualityMode,'720p-high');
 assert.equal(parsed.allowHDR,false);
 assert.equal(parsed.clamps[0].applied,4000000);
 assert(Object.isFrozen(parsed)&&Object.isFrozen(parsed.clamps));
 // Home reads the home default; everything else, and a class the server could not tell, the remote one.
 assert.equal(parseResolvedDeliveryPolicy(policy({networkClass:'local',preferenceLane:'home'})).preferenceLane,'home');
 assert.equal(parseResolvedDeliveryPolicy(policy({networkClass:'unknown'})).preferenceLane,'remote');
 for(const broken of [
  policy({networkClass:'satellite'}),
  policy({networkClass:'cellular'}),                                  // the transport is a hint, never a class
  policy({preferenceLane:'cellular'}),                                // there are two saved defaults, home and remote
  policy({networkClass:'local'}),                                     // the default must follow the class
  policy({directPlay:'maybe'}),
  policy({planningPolicy:'fastest'}),
  policy({transportClass:'satellite'}),
  policy({allowHDR:'false'}),
  policy({maxVideoHeight:8640}),
  policy({maxVideoBitrateBps:-1}),
  // A clamp that widened is not a clamp.
  policy({clamps:[{field:'maxVideoHeight',source:'server_clamp',requested:720,applied:1080}]}),
  null,
 ])assert.throws(()=>parseResolvedDeliveryPolicy(broken),{code:'invalid_delivery_payload'});
 // Every published class and transport is accepted.
 for(const networkClass of networkClasses)for(const transportClass of transportClasses){
  const lane=networkClass==='local'?'home':'remote';
  assert.doesNotThrow(()=>parseResolvedDeliveryPolicy(policy({networkClass,transportClass,preferenceLane:lane})));
 }
});

test('a rung set carries exactly one automatic rung, and only fixed rungs carry ceilings',()=>{
 const rungs=parseQualityRungs([
  {id:'original',kind:'original',label:'Original',enabled:false,reason:'original_exceeds_policy'},
  {id:'automatic',kind:'automatic',label:'Automatic',enabled:true},
  {id:'720p-high',kind:'fixed',label:'720p-high',enabled:true,maxVideoBitrateBps:4000000,maxAudioBitrateBps:128000,targetDisplayHeight:720},
 ]);
 assert.equal(rungs.length,3);
 assert.equal(rungs[0].kind,'original');
 assert.equal(rungs[0].reason,'original_exceeds_policy');
 assert.equal(rungs[2].targetDisplayHeight,720);
 assert.equal(parseQualityRungs([]).length,0);
 for(const broken of [
  [{id:'720p-high',kind:'fixed',label:'720p-high',enabled:true,targetDisplayHeight:720}],                    // no automatic rung
  [{id:'auto',kind:'automatic',label:'A',enabled:true},{id:'auto2',kind:'automatic',label:'B',enabled:true}],
  [{id:'auto',kind:'automatic',label:'A',enabled:true,targetDisplayHeight:1080}],                  // automatic with a ceiling
  [{id:'auto',kind:'automatic',label:'A',enabled:true},{id:'x',kind:'fixed',label:'x',enabled:true}], // fixed with none
  [{id:'auto',kind:'automatic',label:'A',enabled:true},{id:'auto',kind:'original',label:'O',enabled:true}], // duplicate id
  [{id:'bad id',kind:'automatic',label:'A',enabled:true}],
 ])assert.throws(()=>parseQualityRungs(broken),{code:'invalid_delivery_payload'});
});

test('session delivery diagnostics publish the route, the stages and the progress',()=>{
 const raw={mode:'hls',strategy:'video_conversion',reasonCodes:['video_codec_requires_conversion','hdr_tone_mapping_required'],policy:policy(),qualityId:'720p-high',
  hardware:{backend:'videotoolbox',stages:[{operation:'decode',execution:'hardware'},{operation:'tone_map',execution:'software'},{operation:'encode',execution:'hardware'}]},
  streams:[{kind:'video',inputCodec:'hevc',outputCodec:'h264',action:'convert'},{kind:'audio',inputCodec:'eac3',outputCodec:'aac',action:'convert'},{kind:'subtitle',inputCodec:'',outputCodec:'',action:'burn_in'}],
  throttled:true,convertedThroughMs:126000,toneMap:true,toneMapAlgorithm:'hable',targetDisplayHeight:720,targetVideoBitrateBps:4000000,targetAudioBitrateBps:128000,
  playedRetentionMs:300000,throttleBufferMs:60000};
 const parsed=parseSessionDelivery(raw);
 assert.equal(parsed.strategy,'video_conversion');
 assert.equal(parsed.hardware.backend,'videotoolbox');
 assert.equal(parsed.hardware.stages.length,3);
 assert.equal(parsed.streams[2].action,'burn_in');
 assert.equal(parsed.throttled,true);
 assert.equal(parsed.convertedThroughMs,126000);
 assert.equal(parsed.policy!.networkClass,'remote');
 assert(Object.isFrozen(parsed.streams));
 // Reason codes are server vocabulary, not free text.
 assert.throws(()=>parseSessionDelivery({...raw,reasonCodes:['Video Codec Requires Conversion']}),{code:'invalid_delivery_payload'});
 for(const broken of [
  {...raw,mode:'magic'},
  {...raw,strategy:'reencode'},
  {...raw,hardware:{backend:'gpu',stages:[]}},
  {...raw,hardware:{backend:'software',stages:[{operation:'denoise',execution:'hardware'}]}},
  {...raw,streams:[{kind:'video',inputCodec:'h264',outputCodec:'h264',action:'transmute'}]},
  {...raw,convertedThroughMs:-1000},
  {...raw,throttled:'yes'},
  {...raw,playedRetentionMs:999999000},
 ])assert.throws(()=>parseSessionDelivery(broken),{code:'invalid_delivery_payload'});
 // A session with no policy yet is still a valid diagnostic.
 assert.equal(parseSessionDelivery({...raw,policy:null}).policy,null);
});

test('the delivery policy document carries the ladder the client renders',()=>{
 const document=parseDeliveryPolicyDocument({serverId:'server',policy:policy(),ladder:canonicalQualityLadder.map(q=>({id:q.id,label:q.label,targetDisplayHeight:q.height,maxVideoBitrateBps:q.videoKbps*1000,maxAudioBitrateBps:q.audioKbps*1000,dataGBPerHour:q.dataGBPerHour??0}))});
 assert.equal(document.ladder[0].id,'original');assert.equal(document.ladder[2].maxVideoBitrateBps,40000000);
 assert.equal(document.policy.networkClass,'remote');
 assert.throws(()=>parseDeliveryPolicyDocument({serverId:'server',policy:policy(),ladder:[{id:'1080p-low',label:'1080p-low',targetDisplayHeight:8640,maxVideoBitrateBps:1,maxAudioBitrateBps:1}]}),{code:'invalid_delivery_payload'});
});

test('the transport hint is normalized and sent as a header the server may narrow by',async()=>{
 assert.equal(normalizeTransportClass('Wi-Fi'),'wifi');
 assert.equal(normalizeTransportClass('WIRED'),'ethernet');
 assert.equal(normalizeTransportClass('cellular'),'cellular');
 assert.equal(normalizeTransportClass('satellite'),'unknown');
 assert.equal(normalizeTransportClass(undefined),'unknown');

 const sent:Record<string,string>[]=[];
 const api=new HttpLocalApi('http://127.0.0.1:19999','token',async(_input,init)=>{
  sent.push({...(init?.headers as Record<string,string>)});
  return new Response(JSON.stringify({ok:true}),{status:200,headers:{'Content-Type':'application/json'}});
 });
 // Unset: the client declares nothing rather than guessing.
 await api.request('/v1/system');
 assert.equal(sent[0][transportClassHeader],undefined);
 assert.equal(sent[0][deviceClassHeader],undefined);

 api.setTransportClass('Cellular');
 api.setDeviceClass('mobile');
 assert.equal(api.getTransportClass(),'cellular');
 assert.equal(api.getDeviceClass(),'mobile');
 await api.request('/v1/system');
 assert.equal(sent[1][transportClassHeader],'cellular');
 assert.equal(sent[1][deviceClassHeader],'mobile');

 // An unrecognised declaration falls back to saying nothing at all.
 api.setTransportClass('satellite');
 await api.request('/v1/system');
 assert.equal(sent[2][transportClassHeader],undefined);
});

test('the decision trace is read, and a bad one costs only itself',async()=>{
 const {parseSessionDelivery,parseDecisionTrace,setDeliveryContext,deliveryContextHeaders}=await import('../src/delivery.ts');
 const base={mode:'hls',strategy:'video_conversion',reasonCodes:['video_codec_requires_conversion'],policy:null,qualityId:'auto',hardware:{backend:'software',stages:[{operation:'decode',execution:'software'},{operation:'deinterlace',execution:'software'},{operation:'encode',execution:'software'}]},streams:[],throttled:false,convertedThroughMs:0,toneMap:true,playedRetentionMs:180000,throttleBufferMs:60000};
 const decision={version:1,client:{family:'web',evidence:'probed',revision:'abc',published:true},container:'mkv',video:{codec:'hevc',bitDepth:10,height:2160,dynamicRange:'hdr10'},audio:{streamIndex:1,codec:'truehd',channels:8,objectAudio:'atmos',chosenBy:'default',tracks:2},routes:[{route:'original',transport:'direct',admissible:false,chosen:false,rejections:[{code:'container_unsupported',detail:'mkv'}]},{route:'video_conversion',transport:'hls_ts',admissible:true,chosen:true,rejections:[]}],notes:['source_detail_unobserved']};
 const parsed=parseSessionDelivery({...base,decision,audioChannels:2,downmix:'itu_limited',deinterlace:true,outputContainer:'mpegts_hls',failure:'hardware_encoder_failed:nvenc'});
 assert.equal(parsed.decision!.routes[0].rejections[0].code,'container_unsupported');
 assert.equal(parsed.decision!.audio!.objectAudio,'atmos');
 assert.equal(parsed.downmix,'itu_limited');assert.equal(parsed.failure,'hardware_encoder_failed:nvenc');
 assert.equal(parsed.hardware.stages[1].operation,'deinterlace');
 const broken=parseSessionDelivery({...base,decision:{client:{family:7}},failure:'<script>'});
 assert.equal(broken.decision,undefined);assert.equal(broken.failure,undefined);assert.equal(broken.toneMap,true);
 assert.equal(parseDecisionTrace({...decision,routes:[{route:'teleport',admissible:true,chosen:true}]}),undefined);
 setDeliveryContext({transportClass:'Wi-Fi',deviceClass:'television'});
 assert.deepEqual(deliveryContextHeaders(),{'X-Portico-Transport-Class':'wifi','X-Portico-Device-Class':'television'});
 setDeliveryContext({transportClass:'unknown',deviceClass:''});
 assert.deepEqual(deliveryContextHeaders(),{});
});

test('CD-25: audio rendition labels equal sanitized HLS NAME with exact-match identity',async()=>{
 const {parseDeliveryAudioRenditions}=await import('../src/delivery.ts');
 const renditions=[
  {ordinal:0,streamIndex:0,action:'copy',codec:'aac',channels:2,bitrateBps:128000,language:'en',label:'English',default:true},
  {ordinal:1,streamIndex:1,action:'copy',codec:'aac',channels:2,bitrateBps:128000,language:'en',label:'English Commentary',default:false},
  {ordinal:2,streamIndex:2,action:'convert',codec:'ac3',channels:6,bitrateBps:384000,language:'es',label:'Espanol',default:false},
 ];
 const parsed=parseDeliveryAudioRenditions(renditions)!;
 assert.equal(parsed.length,3);
 // Exact match: similar labels stay distinct; selection is by ordinal/id, never fuzzy.
 assert.notEqual(parsed[0].label,parsed[1].label);
 assert.equal(parsed.find(r=>r.label==='English Commentary')!.streamIndex,1);
 // Sanitized server labels (no quotes/backslashes/C0/DEL) decode; control fails closed.
 assert.equal(parseDeliveryAudioRenditions([{...renditions[0],label:'Bad\x07label'}]),undefined);
 assert.equal(parseDeliveryAudioRenditions([{...renditions[0],label:'Bad\x7flabel'}]),undefined);
 assert.equal(parseDeliveryAudioRenditions([{...renditions[0],channels:4,channelLayout:'quad'}])?.[0].channelLayout,'quad');
 assert.equal(parseDeliveryAudioRenditions([{...renditions[0],channelLayout:'stereo,volume=0'}]),undefined);
 assert.equal(parseDeliveryAudioRenditions([{...renditions[0],action:'convert',channels:4,channelLayout:'quad'}]),undefined);
});


test('tablet and the resolved app language reach actual API and playback transports',async()=>{
 const {setDeliveryContext,deliveryContextHeaders,appLanguageHeader}=await import('../src/delivery.ts');
 setDeliveryContext({transportClass:'unknown',deviceClass:'',appLanguage:''});
 const sent:Record<string,string>[]=[];
 const api=new HttpLocalApi('http://127.0.0.1:19999','token',async(_input,init)=>{sent.push({...init?.headers as Record<string,string>});return new Response(JSON.stringify({ok:true}),{status:200,headers:{'Content-Type':'application/json'}});});
 api.setDeviceClass('tablet');api.setAppLanguage('en-ca');
 await api.request('/v1/system');
 assert.equal(sent[0][deviceClassHeader],'tablet');assert.equal(sent[0][appLanguageHeader],'en-CA');
 assert.equal(deliveryContextHeaders()[appLanguageHeader],'en-CA');
 assert.throws(()=>api.setAppLanguage('not a language'));
 setDeliveryContext({transportClass:'unknown',deviceClass:'',appLanguage:''});
});
