// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {act,createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';
import {clonePortableDto} from '../../../packages/client-core/src/clone-portable-dto.ts';
import * as draft from '../../../packages/client-core/src/server-admin/owner-channel-draft.ts';
const nodes=(value:any):any[]=>Array.isArray(value)?value.flatMap(nodes):value?.props?[value,...nodes(value.props.children),...nodes(value.props.footer)]:[];
test('the actual Hours sheet edits a detached rebuilt block without global structuredClone',async()=>{
 const saved=globalThis.structuredClone;delete (globalThis as any).structuredClone;
 const block=Object.freeze({id:'block',days:Object.freeze(['mon']),from:'01:00',to:'02:00'}),value={libraries:['library'],kinds:['movie'],quality:{},logoOnScreen:{},blocks:[block]};let submitted:any,tree:any;
 const {NativeChannelHours}=await screenModule(new URL('../src-next/screens/settings-live/server/channels/ChannelHours.tsx',import.meta.url),{
  react:{...React,default:React},'react-native':{Text:'Text',View:'View'},'../../../../../../../packages/client-core/src':{clonePortableDto},
  '../../../../../../../packages/client-core/src/server-admin/owner-channel-draft':draft,'../../../../../../../packages/client-core/src/server-admin/owner-library-channels':{newChannelOperation:()=> 'new'},
  '../../../../../../../packages/i18n/src':{defaultI18n:{t:(v:string)=>v,locale:'en-US'}},'../../../../kit/controls':{Button:'Button',Select:'Select',Switch:'Switch',TextField:'TextField'},'../../../../kit/overlays':{BottomSheet:'BottomSheet'},'../../../../kit/theme':{useTheme:()=>({colors:{}})},'./ChannelSelection':{NativeChannelSelection:'Selection'},
 });
 const state={presets:[]};
 const group={current:()=>true,source:{subscribe:()=>()=>{},getSnapshot:()=>state,presets:async()=>{}},libraries:[]};
 const host=createFabricHookHost(()=>{tree=NativeChannelHours({group,value,locked:false,onChange:(next:any)=>{submitted=next;}});});
 try{await act(()=>host.mount());await act(()=>nodes(tree).find(v=>v.type==='Button'&&v.props.children.includes('01:00–02:00')).props.onPress());
  const sheet=()=>nodes(tree).find(v=>v.type==='BottomSheet');assert.equal(sheet().props.open,true);
  await act(()=>nodes(sheet()).find(v=>v.type==='TextField'&&v.props.label==='web.ownerChannels.from').props.onChangeText('00:30'));
  await act(()=>nodes(sheet()).find(v=>v.type==='Button'&&v.props.children.includes('action.save')).props.onPress());
  assert.equal(submitted.blocks[0].from,'00:30');assert.equal(block.from,'01:00');assert.notEqual(submitted.blocks[0].days,block.days);
 }finally{await host.unmount();globalThis.structuredClone=saved;}
});
