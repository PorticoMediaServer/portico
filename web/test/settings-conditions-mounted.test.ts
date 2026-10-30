import test from 'node:test';
import assert from 'node:assert/strict';
import {componentModule} from './helpers/component-harness.mjs';
import {createRenderer,tree,all} from './helpers/interaction-harness.mjs';
import {createSources} from '../src/screens/settings/server/channels/channel-sources.ts';
import {channelConditionNode} from '@core/server-admin/channel-conditions.ts';

const config=(year=2000)=>({protocolVersion:'1.0',id:'one',name:'A',description:'',enabled:true,position:0,timezone:'UTC',seed:'s',defaultRuleId:'rule',viewerAccess:'owner-only',logoItemId:'',templateId:'',quality:{mode:'automatic',maxBitrate:8000000,maxHeight:1080,allowLossy:true,allowHdrToSdr:true},overlay:{enabled:false,corner:'br',sizePercent:10,insetPercent:5,treatment:'none'},rules:[{id:'rule',name:'Rule',query:{libraryIds:['lib'],kinds:['movie'],recentDays:0,limit:10,order:'title',filter:{variant:'predicate',field:'year',operator:'at-least',value:year}},mode:'sequential',episodeMode:'none',exhaustion:'loop',deduplicationWindow:0,maxConsecutive:1}],blocks:[]});
const channel=(year=2000,revision=1)=>({config:config(year),revision,state:'ok',healthCode:'',generation:'g1',generatedThrough:'',candidateCount:0,unresolvedDurationCount:0,replacementBoundary:''});
const i18n={defaultI18n:{t:(key:string)=>key,has:()=>true}};
async function fixture(t:import('node:test').TestContext){
 const h=createRenderer();let wire=channel(),fieldsCreated=0;const writes:any[]=[];
 const sources=createSources({serverId:'server',isCurrent:()=>true,reportError:error=>{throw error;},uploadLogo:async()=>{},api:{async request<T>(_path:string,method?:string,body?:any){if(method==='POST'){writes.push(body);wire={...channel(2000,body.expectedRevision+1),config:body.config};return {protocolVersion:'1.0',serverId:'server',channel:wire} as T;}return {protocolVersion:'1.0',serverId:'server',channels:[wire]} as T;}}});
 await sources.reader.load();sources.editor.edit(sources.reader.getSnapshot().channels![0]);
 class Fields {constructor(){fieldsCreated++;} getSnapshot=()=>({phase:'ready',fields:[{libraryId:'lib',pivot:'movies',field:{id:'year',type:'number',operators:['at-least','between']}}]});subscribe=()=>()=>{};select=async()=>{};dispose=()=>{};}
 const conditions=await componentModule(new URL('../src/screens/settings/server/channels/ChannelConditions.tsx',import.meta.url),{react:h.react,'@i18n':i18n,
  '@core/server-admin/channel-catalogue.ts':{channelFieldMessage:()=> 'year',channelFieldLabel:()=> 'year',channelValueLabel:(value:unknown)=>String(value)},
  '../../../../kit/controls':{Button:'Button',RichSelect:'RichSelect',TextField:'TextField'},'./channel-fields-source':{ChannelFieldsSource:Fields},'./channels.module.css':{default:{}}});
 const editor=await componentModule(new URL('../src/screens/settings/server/channels/ChannelEditor.tsx',import.meta.url),{react:h.react,'@i18n':i18n,
  '../../../../kit/controls':{Button:'Button',RichSelect:'RichSelect',Switch:'Switch',TextField:'TextField'},'../../../../kit/overlays':{Dialog:'Dialog'},'../../../../kit/MetadataFacts':{MetadataFacts:'MetadataFacts'},
  './ChannelConditions':conditions,'./ChannelPins':{ChannelPins:'ChannelPins'},'./ChannelHours':{ChannelHours:'ChannelHours'},'./channels.module.css':{default:{}}});
 const props={source:sources.editor,api:{},reportError:()=>{},presets:sources.presets,libraries:[{id:'lib',kind:'movie',name:'Library'}],isCurrent:()=>true,close:()=>{},creating:false,openPresets:()=>{}};
 const render=()=>tree(h.render(()=>h.react.createElement(editor.ChannelEditor,props)));
 t.after(()=>{h.dispose();sources.dispose();});render();render();
 return {sources,writes,render,fieldsCreated:()=>fieldsCreated,remote:(year:number)=>{wire=channel(year,2);},
  year:()=>all(render(),'TextField').find((node:any)=>node.props['aria-label']==='year')!,
  operator:()=>all(render(),'RichSelect').find((node:any)=>node.props['aria-label']==='year')!,
  rename:()=>all(render(),'TextField').find((node:any)=>node.props.label==='builder.name')!};
}

test('mounted ConditionsBody adopts remote Y at rev2, keeps owner/siblings, rejects X callbacks and saves the Y-based edit',async t=>{
 const f=await fixture(t),s=f.sources,editor=s.editor,presets=s.presets,logos=s.logos;
 f.rename().props.onChange({target:{value:'local name'}});f.render();const old=f.year().props.onChange;assert.equal(f.year().props.value,'2000');f.remote(2010);
 assert.deepEqual(await s.refreshQuiet(),{kind:'adopted'});assert.equal(s.editor.getSnapshot().config?.name,'local name');f.render();f.render();assert.equal(f.year().props.value,'2010');const before=s.editor.getSnapshot();
 old({target:{value:'1990'}});assert.equal(s.editor.getSnapshot(),before,'old rendered X callbacks cannot borrow rev2');
 f.year().props.onChange({target:{value:'2011'}});assert.equal((s.editor.getSnapshot().config?.rules[0].query.filter as any).value,2011);f.render();
 assert.equal(f.fieldsCreated(),1);assert.equal(s.editor,editor);assert.equal(s.presets,presets);assert.equal(s.logos,logos);
 await s.editor.save();assert.equal(f.writes[0].expectedRevision,2);assert.equal(f.writes[0].config.rules[0].query.filter.value,2011);assert.equal(f.writes[0].config.name,'local name');
});

test('incomplete mounted condition draft stays local, gates quiet adoption and keeps its old callback spent',async t=>{
 const f=await fixture(t),s=f.sources;const old=f.year().props.onChange;f.operator().props.onValueChange('between');f.render();assert.equal(f.year().props.value,'');const before=s.editor.getSnapshot();f.remote(2010);
 assert.deepEqual(await s.refreshQuiet(),{kind:'deferred'});assert.equal(s.editor.getSnapshot(),before);f.render();assert.equal(f.year().props.value,'');old({target:{value:'1990'}});assert.equal(s.editor.getSnapshot(),before);assert.equal(f.fieldsCreated(),1);
});

test('conflicting accepted local conditions retain the complete original editor receipt and mounted tree',async t=>{
 const f=await fixture(t),s=f.sources;f.year().props.onChange({target:{value:'2005'}});f.render();const before=s.editor.getSnapshot();f.remote(2010);
 assert.deepEqual(await s.refreshQuiet(),{kind:'conflict'});assert.equal(s.editor.getSnapshot(),before);f.render();assert.equal(f.year().props.value,'2005');await s.editor.save();assert.equal(f.writes[0].expectedRevision,1);assert.equal(f.writes[0].config.rules[0].query.filter.value,2005);
});
