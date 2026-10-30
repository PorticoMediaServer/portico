// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';
import {clonePortableDto} from '../../../packages/client-core/src/clone-portable-dto.ts';
const nodes=(value:any):any[]=>Array.isArray(value)?value.flatMap(nodes):value?.props?[value,...nodes(value.props.children),...nodes(value.props.footer)]:[];
const button=(tree:any,label:string)=>nodes(tree).find(node=>node.type==='Button'&&node.props.children.includes(label));
async function withoutGlobal(run:()=>Promise<void>){const descriptor=Object.getOwnPropertyDescriptor(globalThis,'structuredClone');delete(globalThis as {structuredClone?:unknown}).structuredClone;try{await run();}finally{if(descriptor)Object.defineProperty(globalThis,'structuredClone',descriptor);}}
const theme={colors:{text:'white',textSecondary:'grey',raised:'black',record:'red',line:'grey'}};
const common={react:{...React,default:React},'react-native':{Text:'Text',View:'View'},
 '../../../../../../../packages/client-core/src':{clonePortableDto},'../../../../../../../packages/i18n/src':{defaultI18n:{t:(key:string)=>key,locale:'en-US'}},
 '../../../../kit/controls':{Button:'Button',Select:'Select',Switch:'Switch',TextField:'TextField'},'../../../../kit/overlays':{BottomSheet:'BottomSheet'},
 '../../../../kit/shell/visual':{shellType:()=>({})},'../../../../kit/theme':{useTheme:()=>theme}};

test('actual Hours editor opens and edits a detached original block without global structuredClone',()=>withoutGlobal(async()=>{
 const {NativeChannelHours}=await screenModule(new URL('../src-next/screens/settings-live/server/channels/ChannelHours.tsx',import.meta.url),{...common,'./model':{nativeChannelOperationId:()=> 'original-operation'}});
 const block=Object.freeze({id:'b1',weekdays:Object.freeze([1]),startMinute:60,endMinute:120,priority:0,ruleId:'r1',fallbackRuleId:'r1',anchor:'channel-cursor',overrun:'finish'});
 const config={defaultRuleId:'r1',rules:[{id:'r1',name:'Original'}],blocks:[block]};let submitted:any,tree:any;
 const presetState={items:[],busy:false},presets={subscribe:()=>()=>{},getSnapshot:()=>presetState};
 const host=createFabricHookHost(()=>{tree=NativeChannelHours({config,presets,locked:false,isCurrent:()=>true,onChange:(update:any)=>{submitted=update(config);return true;},onEditing:()=>{},t:(key:string)=>key});});
 try{
  await act(()=>host.mount());await act(()=>nodes(tree).find(node=>node.type==='Button'&&node.props.onPress).props.onPress());
  const sheet=nodes(tree).find(node=>node.type==='BottomSheet');assert(sheet.props.open);
  const start=nodes(sheet).find(node=>node.type==='TextField'&&node.props.value==='01:00');assert(start);
  await act(()=>start.props.onChangeText('02:00'));await act(()=>button(tree,'action.done').props.onPress());
  assert(submitted);assert.equal(submitted.blocks[0].startMinute,120);assert.equal(block.startMinute,60);
  assert.notEqual(submitted.blocks[0],block);assert.notEqual(submitted.blocks[0].weekdays,block.weekdays);assert.deepEqual(block.weekdays,[1]);
 }finally{await host.unmount();}
}));

test('actual preset editor clones nested definition without a global and refuses an old form save after replacement',()=>withoutGlobal(async()=>{
 class Fields{constructor(..._args:unknown[]){}select=async()=>{};dispose=()=>{};}
 const {NativeBlockPresets}=await screenModule(new URL('../src-next/screens/settings-live/server/channels/BlockPresets.tsx',import.meta.url),{...common,
 '../../../../../../../packages/client-core/src/server-admin/channel-fields-source':{ChannelFieldsSource:Fields},
 '../../../../../../../packages/client-core/src/server-admin/channel-catalogue':{channelFieldLabel:()=>'',channelFieldMessage:()=>''}});
 const body=Object.freeze({libraryId:'library',description:'Original',order:'sequential',durationMinutes:60,startMinute:-1,days:Object.freeze(['monday']),criteria:Object.freeze([{field:'genre',operator:'includes',values:Object.freeze(['Drama'])}])});
 const record={id:'preset',name:'Original',revision:3,body};const state={items:[record],loaded:true,busy:false,reconcile:false};let saves=0,sent:any,tree:any;
 const source={subscribe:()=>()=>{},getSnapshot:()=>state,load:async()=>{},save:async(original:any,_name:string,value:any)=>{assert.equal(original,record);saves++;sent=value;return true;}};
 const group={presets:source,current:()=>true,api:{},libraries:[{id:'library',name:'Movies'}],failure:assert.fail};
 const host=createFabricHookHost(()=>{tree=NativeBlockPresets({group,isCurrent:()=>true,onEditing:()=>{}});});
 try{
  await act(()=>host.mount());await act(()=>button(tree,'web.selection.edit').props.onPress());
  const draft=nodes(tree).find(node=>node.props.draft)?.props.draft;assert(draft);assert.notEqual(draft.body,body);assert.notEqual(draft.body.criteria[0].values,body.criteria[0].values);
  await act(()=>nodes(tree).find(node=>node.type==='TextField'&&node.props.label==='builder.description').props.onChangeText('Changed'));
  const oldSave=button(tree,'builder.preset.save').props.onPress;
  await act(()=>button(tree,'action.cancel').props.onPress());await act(()=>button(tree,'web.selection.edit').props.onPress());
  await act(()=>oldSave());assert.equal(saves,0,'old original form cannot save or close a successor');
  await act(()=>button(tree,'builder.preset.save').props.onPress());assert.equal(saves,1);assert.equal(sent.description,'Original');assert.equal(body.description,'Original');assert.notEqual(sent.criteria[0].values,body.criteria[0].values);
 }finally{await host.unmount();}
}));
