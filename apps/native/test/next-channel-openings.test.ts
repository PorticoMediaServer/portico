import test from 'node:test';
import assert from 'node:assert/strict';
import {OwnerChannelSource} from '../src-next/screens/settings-live/server/channels/source.ts';
import {blockOverlap,copyBlockPreset,validBuilder} from '../../../packages/client-core/src/server-admin/owner-channel-draft.ts';
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
 const {Channels}=await screenModule(new URL('../src-next/screens/settings-live/server/channels/LibraryChannelsPage.tsx',import.meta.url),{
  react:host.react,'react-native':{Text:'Text',View:'View'},'../../../../../../../packages/i18n/src':{defaultI18n:{t:(v:string)=>v,number:String,dateTime:String}},
  '../../../../../../../packages/client-core/src/presentation/kit-model/channels':{channelMonograms:()=>new Map(rows.map(v=>[v.id,new Map([[v.id,{text:'CH',seed:v.id}]])]))},
  '../../../../kit/artwork':{ChannelMark:'ChannelMark'},'../../../../kit/controls':{Button:'Button',Focusable:'Focusable'},'../../../../kit/overlays':{BottomSheet:'BottomSheet',MenuRow:'MenuRow'},'../../../../kit/shell/visual':{shellType:()=>({})},'../../../../kit/theme':{useTheme:()=>({colors:{}})},'./model':{},'./ChannelEditor':{NativeChannelEditor:'Editor'},'./BlockPresets':{NativeBlockPresets:'Presets'},'./ChannelLogo':{NativeChannelLogo:'Logo'},
 });
 const nodes=(v:any):any[]=>Array.isArray(v)?v.flatMap(nodes):v?.props?[v,...nodes(v.props.children),...nodes(v.props.footer)]:[];
 const group={source,current:()=>true},render=()=>host.render(()=>Channels({group,runtime:{artwork:()=>undefined}})),menu=(tree:any)=>nodes(tree).filter(v=>v.type==='BottomSheet')[1];let tree=render();
 nodes(tree).find(v=>v.props.testID==='channel-channel').props.onPress();tree=render();const old=menu(tree),edit=nodes(old).find(v=>v.type==='MenuRow'&&v.props.label==='web.selection.edit').props.onPress;
 old.props.onOpenChange(false);tree=render();nodes(tree).find(v=>v.props.testID==='channel-second').props.onPress();tree=render();old.props.onOpenChange(false);edit();tree=render();assert.equal(menu(tree).props.title,'Second');assert.equal(menu(tree).props.open,true);assert.deepEqual(calls,[]);
 nodes(menu(tree)).find(v=>v.type==='MenuRow'&&v.props.label==='web.selection.edit').props.onPress();assert.deepEqual(calls,['second']);
});
