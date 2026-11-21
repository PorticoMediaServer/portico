// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {act,createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';
const nodes=(value:any):any[]=>Array.isArray(value)?value.flatMap(nodes):value?.props?[value,...nodes(value.props.children)]:[];
test('pinned titles read only the revealed prefix, Show more resolves the next twenty, and retirement stops label work',async()=>{
 const calls:string[]=[],ids=Array.from({length:45},(_,i)=>`item-${i}`);let active=true,tree:any;
 const lookup={api:{},current:()=>active,report:assert.fail},group={lookup,libraries:[{id:'library',name:'Movies',kind:'movie'}]};
 const {NativeChannelPins}=await screenModule(new URL('../src-next/kit/settings/channels/ChannelPins.tsx',import.meta.url),{
  react:{...React,default:React},'react-native':{Text:'Text',View:'View'},
  '../../../../../../packages/client-core/src/server-admin/owner-library-channels':{readChannelTitle:async(_api:any,id:string)=>{calls.push(id);return {id,title:`Title ${id}`,kind:'movie'};},findChannelTitles:assert.fail},
  '../../../../../../packages/i18n/src':{defaultI18n:{t:(v:string)=>v}},'../../controls':{Button:'Button',Chip:'Chip',Select:'Select',TextField:'TextField'},'../../theme':{useTheme:()=>({colors:{},space:{},type:{rowLabel:{},rowHelp:{}}})},'./source':{terminal:()=>false},
 });
 const host=createFabricHookHost(()=>{tree=NativeChannelPins({group,libraryIds:['library'],ids,locked:false,label:'Always',onChange:assert.fail});});
 try{await act(()=>host.mount());assert.deepEqual(calls.sort(),ids.slice(0,20).sort());assert.equal(nodes(tree).filter(v=>v.type==='Chip').length,20);
  await act(()=>nodes(tree).find(v=>v.type==='Button'&&v.props.children.includes('action.showMore')).props.onPress());assert.equal(nodes(tree).filter(v=>v.type==='Chip').length,40);assert.equal(calls.length,40);
  active=false;await act(()=>nodes(tree).find(v=>v.type==='Button'&&v.props.children.includes('action.showMore')).props.onPress());assert.equal(calls.length,40);
 }finally{await host.unmount();}
});
