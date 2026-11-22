import test from 'node:test';
import assert from 'node:assert/strict';
import {OwnerChannelSource} from '../src-next/screens/settings-live/server/channels/source.ts';
import {blockOverlap,copyBlockPreset,validBuilder} from '../../../packages/client-core/src/server-admin/owner-channel-draft.ts';
import {channelPreviewLength} from '../../../packages/client-core/src/presentation/kit-model/channels.ts';
const builder={libraries:['library'],kinds:['movie'],quality:{},logoOnScreen:{}};
const row={id:'channel',revision:'1',number:1,name:'Original',on:true,timeZone:'UTC',summary:'Movies',libraries:['library'],state:'onAir',titles:3,builder};
function fixture(){
 const calls:{path:string;method:string;options:any}[]=[],reads:string[]=[];let current=true,failure:unknown,updated={...row,description:''};
 const api={request:async<T,>(path:string)=>{reads.push(path);return (path.includes('/templates')?{items:[],timeZone:'UTC',moodAvailable:false}:path.endsWith('/channel')?updated:{items:[updated]}) as T;},requestRaw:async(path:string,method:string,options:any)=>{calls.push({path,method,options});if(failure)throw failure;return {status:200,headers:{},body:{...updated,...options.body,revision:'2'}};}};
 const source=new OwnerChannelSource(api as any,()=>current,(error:unknown)=>assert.fail(String(error)));
 return {source,calls,reads,retire:()=>{current=false;},fail:(value:unknown)=>{failure=value;},update:()=>{updated={...row,revision:'2',description:'Changed elsewhere'};}};
}
test('a replaced editor cannot save; an edit sends its read revision and only changed fields',async()=>{
 const f=fixture();await f.source.load();await f.source.open(f.source.getSnapshot().channels[0]);const old=f.source.getSnapshot().editor!;
 f.source.edit(old,d=>({...d,name:'Changed'}));await f.source.save(old);assert.equal(f.calls.length,0);
 await f.source.save(f.source.getSnapshot().editor!);assert.equal(f.calls.length,1);assert.equal(f.calls[0].method,'PATCH');assert.equal(f.calls[0].options.headers['If-Match'],'"1"');assert.deepEqual(f.calls[0].options.body,{name:'Changed'});f.source.dispose();
});
test('a lost creation answer retries the same operation and a retired owner cannot publish it',async()=>{
 const f=fixture();await f.source.load();f.source.blank();f.source.edit(f.source.getSnapshot().editor!,()=>({name:'New',description:'',timeZone:'UTC',on:true,builder}));
 f.fail(new Error('lost'));await f.source.save(f.source.getSnapshot().editor!);assert.equal(f.source.getSnapshot().pendingCreate,true);const id=f.calls[0].options.headers['Idempotency-Key'];
 f.fail(undefined);await f.source.save(f.source.getSnapshot().editor!);assert.equal(f.calls[1].options.headers['Idempotency-Key'],id);assert.deepEqual(f.calls[1].options.body,f.calls[0].options.body);
 f.retire();assert.equal(f.source.getSnapshot().channels.length,0);await f.source.load();assert.equal(f.calls.length,2);f.source.dispose();
});
test('a concurrent edit keeps local changes and adopts untouched fresh fields before another save',async()=>{
 const f=fixture();await f.source.load();await f.source.open(f.source.getSnapshot().channels[0]);f.source.edit(f.source.getSnapshot().editor!,d=>({...d,name:'Local'}));f.update();f.fail({status:412});await f.source.save(f.source.getSnapshot().editor!);
 const editor=f.source.getSnapshot().editor!;assert.equal(editor.document?.revision,'2');assert.equal(editor.draft.name,'Local');assert.equal(editor.draft.description,'Changed elsewhere');assert.equal(editor.conflict,false);f.source.dispose();
});
test('a preset copy is detached without structuredClone and overlapping overnight blocks refuse saving',()=>{
 const saved=globalThis.structuredClone;delete (globalThis as any).structuredClone;
 try{const preset={id:'p',revision:'1',name:'Night',library:'library',recipe:{clauses:[{field:'genre',values:['Drama']}],order:'shuffle',minutes:120,startsAt:'23:00',days:['sun']}};
 const block=copyBlockPreset(preset,'movie','b');assert.equal(block.to,'01:00');block.selection!.conditions![0].values![0]='Comedy';assert.equal(preset.recipe.clauses[0].values[0],'Drama');
 const overlap={id:'c',days:['mon'],from:'00:30',to:'02:00'};assert.ok(blockOverlap([block,overlap]));assert.equal(validBuilder({...builder,blocks:[block,overlap]}),false);
 }finally{globalThis.structuredClone=saved;}
});

test('a replaced native channel menu cannot invoke or dismiss its predecessor',async()=>{
 const {hooks,screenModule}=await import('./helpers/screen-harness.mjs'),host=hooks(),calls:string[]=[];
 Object.assign(host.react,{useLayoutEffect:()=>{}});Object.assign(host.react.default,{useLayoutEffect:()=>{}});
 const rows=[row,{...row,id:'second',name:'Second'}],state={channels:rows,choices:{items:[]},presets:[]},source={subscribe:()=>()=>{},getSnapshot:()=>state,action:async(value:any)=>{calls.push(value.id);},load:async()=>{},open:async(value:any)=>{calls.push(value.id);}};
 const i18n={defaultI18n:{t:(v:string)=>v,number:String,dateTime:String}};
 const hook=await screenModule(new URL('../src-next/pages/shared/live/useLibraryChannels.ts',import.meta.url),{
  react:host.react,'../../../../../../packages/i18n/src':i18n,'../../../screens/settings-live/server/channels/model':{},
  '../../../../../../packages/client-core/src/presentation/kit-model/channels':{channelMonograms:()=>new Map(rows.map(v=>[v.id,new Map([[v.id,{text:'CH',seed:v.id}]])]))},
 });
 const {LibraryChannelsList:Channels}=await screenModule(new URL('../src-next/pages/touch/live/LibraryChannelsTab.tsx',import.meta.url),{
  react:host.react,'../../../../../../packages/i18n/src':i18n,'../../shared/live/useLibraryChannels':hook,
  '../../../kit/controls':{Button:'Button'},'../../../kit/frames':{SectionCard:'SectionCard'},'../../../kit/overlays':{BottomSheet:'BottomSheet',MenuRow:'MenuRow'},
  '../../../kit/settings/StorageParts':{StorageActions:'StorageActions',StorageNote:'StorageNote',StorageSheetBody:'StorageSheetBody'},'../../../kit/live/LiveSourceParts':{LiveSourcesEmpty:'Empty'},
  '../../../kit/live/ChannelParts':{HealthFacts:'HealthFacts',LibraryChannelCard:'LibraryChannelCard',MenuGroup:'MenuGroup'},
  '../../../kit/live/channels/ChannelEditor':{NativeChannelEditor:'Editor'},'../../../kit/live/channels/BlockPresets':{NativeBlockPresets:'Presets'},'../../../kit/live/channels/ChannelLogo':{NativeChannelLogo:'Logo'},
 });
 const nodes=(v:any):any[]=>Array.isArray(v)?v.flatMap(nodes):v?.props?[v,...nodes(v.props.children),...nodes(v.props.footer)]:[];
 const group={source,current:()=>true},render=()=>host.render(()=>Channels({group,runtime:{artwork:()=>undefined}})),menu=(tree:any)=>nodes(tree).filter(v=>v.type==='BottomSheet')[1];let tree=render();
 nodes(tree).find(v=>v.props.testID==='channel-channel').props.onPress();tree=render();const old=menu(tree),edit=nodes(old).find(v=>v.type==='MenuRow'&&v.props.label==='web.selection.edit').props.onPress;
 old.props.onOpenChange(false);tree=render();nodes(tree).find(v=>v.props.testID==='channel-second').props.onPress();tree=render();old.props.onOpenChange(false);edit();tree=render();assert.equal(menu(tree).props.title,'Second');assert.equal(menu(tree).props.open,true);assert.deepEqual(calls,[]);
 nodes(menu(tree)).find(v=>v.type==='MenuRow'&&v.props.label==='web.selection.edit').props.onPress();assert.deepEqual(calls,['second']);
});

test('the native channel preview requests the server-projected sample poster', async () => {
 const {hooks,screenModule}=await import('./helpers/screen-harness.mjs'),host=hooks(),paths:unknown[]=[],presences:boolean[]=[];
 const hook=await screenModule(new URL('../src-next/pages/shared/live/channels/useChannelEditor.ts',import.meta.url),{
  react:host.react,'../../../../../../../packages/client-core/src/server-admin/owner-channel-draft':{validBuilder},
 });
 const {NativeChannelEditor}=await screenModule(new URL('../src-next/kit/live/channels/ChannelEditor.tsx',import.meta.url),{
  react:host.react,'react-native':{Modal:'Modal',Text:'Text',View:'View'},
  'react-native-safe-area-context':{useSafeAreaInsets:()=>({top:0,bottom:0})},
  '../../../../../../packages/client-core/src/server-admin/owner-channel-draft':{validBuilder},
  '../../../../../../packages/client-core/src/presentation/kit-model/channels':{channelPreviewLength},
  '../../../../../../packages/i18n/src':{defaultI18n:{t:(value:string)=>value}},
  '../../artwork':{Artwork:'Artwork',StandardBackdrop:'Backdrop',missingArt:()=>'Missing'},'../../layout/TopBar':{TopBar:'TopBar'},'../../shell/Layer':{Layer:'Layer'},
  '../../controls':{Button:'Button',Select:'Select',Switch:'Switch',TextField:'TextField'},
  '../../theme':{useTheme:()=>({colors:{},type:{sectionLabel:{},rowLabel:{},rowHelp:{},rowHeading:{}}}),textLines:(count:number)=>({numberOfLines:count})},
  '../../useMotion':{useMotion:()=>({reduce:true})},'../../../platform/keyboard':{FormScrollView:'ScrollView'},
  // Both windows go through the shared presence, so an iOS Modal dropped behind a leaving one is asked for again.
  '../../overlays/native-dismissal':{useNativeModalPresence:(present:boolean)=>{presences.push(present);return {mounted:true,visible:present,key:1,onShow(){},onDismiss(){}};}},
  './ChannelSelection':{NativeChannelSelection:'Selection'},'./ChannelHours':{NativeChannelHours:'Hours'},
  '../../../pages/shared/live/channels/useChannelEditor':hook,
 });
 const draft={name:'',description:'',timeZone:'UTC',on:true,builder},editor={draft};
 const poster='/v1/images/'+'a'.repeat(64),sample={id:'episode',title:'Show',images:{poster}};
 const state={editor,previewFor:draft,preview:{titles:1,unplaceable:0,hours:1,sample:[sample],today:[]}};
 const source={subscribe:()=>()=>{},getSnapshot:()=>state};
 host.render(()=>NativeChannelEditor({group:{source,current:()=>true},editor,runtime:{artwork:(path:unknown)=>{paths.push(path);}}}));
 assert.deepEqual(paths,[poster]);
 assert.deepEqual(presences.slice(0,2),[true,false],'the editor window and its closed preview window each ask the shared presence');
});
