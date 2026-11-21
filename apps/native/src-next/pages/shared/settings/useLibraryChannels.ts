import {useEffect, useLayoutEffect, useMemo, useState, useSyncExternalStore} from 'react';
import type {LibraryChannel, LibraryChannelTemplate} from '../../../../../../packages/contracts/src/next/api';
import {channelMonograms} from '../../../../../../packages/client-core/src/presentation/kit-model/channels';
import {createNativeChannels, type NativeChannelsGroup} from '../../../screens/settings-live/server/channels/model';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {NativeShellRuntime} from '../../../shell/runtime';

export function useLibraryChannelGroup(ports: Pick<NativeSettingsLivePorts, 'context' | 'requestId'>, runtime: NativeShellRuntime){
 const context=ports.context,[group,setGroup]=useState<NativeChannelsGroup>();
 useLayoutEffect(()=>{const next=createNativeChannels(context,runtime.reportError);setGroup(next);return next.dispose;},[context,runtime.reportError]);
 return group?.context === context ? group : undefined;
}
export function useChannelRows({group,runtime,initialChannelId}:{group:NativeChannelsGroup;runtime:NativeShellRuntime;initialChannelId?:string}){
 const {source}=group,state=useSyncExternalStore(source.subscribe,source.getSnapshot),[menu,setMenu]=useState<{row:LibraryChannel;token:object}>(),[templates,setTemplates]=useState<object>(),[remove,setRemove]=useState<{row:LibraryChannel;token:object}>();
 const openings=useMemo(()=>({active:group.current(),menu:undefined as typeof menu,templates:undefined as object|undefined,remove:undefined as typeof remove}),[group]);
 useLayoutEffect(()=>{openings.active=group.current();return()=>{openings.active=false;openings.menu=undefined;openings.templates=undefined;openings.remove=undefined;};},[group,openings]);
 useLayoutEffect(() => {
  if (openings.menu && !state.channels.includes(openings.menu.row)) openings.menu = undefined;
  if (openings.remove && !state.channels.includes(openings.remove.row)) openings.remove = undefined;
 }, [openings, state.channels]);
 const closeMenu=()=>{if(openings.active&&openings.menu===menu){openings.menu=undefined;setMenu(undefined);}};
 const closeTemplates=()=>{if(openings.active&&openings.templates===templates){openings.templates=undefined;setTemplates(undefined);}};
 const closeRemove=()=>{if(openings.active&&openings.remove===remove){openings.remove=undefined;setRemove(undefined);}};
 useEffect(()=>{void source.load().then(()=>{if(initialChannelId&&group.current())void source.openId(initialChannelId);});},[group,source,initialChannelId]);
 const blocked=!!(state.loading||state.busy||state.uncertain||state.editor||state.presetEditor||state.pendingCreate||state.pendingPreset),held=menu&&openings.menu===menu&&state.channels.includes(menu.row)&&group.current()?menu:undefined,removal=remove&&openings.remove===remove&&state.channels.includes(remove.row)&&group.current()?remove:undefined;
 const marks=channelMonograms({kind:'complete-authorized',channels:state.channels.map(row=>({sourceId:row.id,id:row.id,name:row.name}))});
 const open=(template?:LibraryChannelTemplate)=>{if(openings.active&&templates&&openings.templates===templates&&!blocked&&group.current()&&(!template||state.choices?.items.includes(template)&&template.available)){source.blank(template);closeTemplates();}};
 const action=(run:(row:LibraryChannel)=>void)=>{if(openings.active&&held&&openings.menu===held&&!blocked&&group.current()&&source.getSnapshot().channels.includes(held.row)){closeMenu();run(held.row);}};
 return {source, state, menu, setMenu, templates, setTemplates, remove, setRemove, openings, closeMenu, closeTemplates,
  closeRemove, blocked, held, removal, marks, open, action};
}
