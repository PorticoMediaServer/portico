import {useState,useSyncExternalStore} from 'react';
import {clonePortableDto} from '../../../../../../../packages/client-core/src';
import type {LibraryChannelBlock,LibraryChannelBuilder} from '../../../../../../../packages/client-core/src/server-admin/owner-library-channels';
import {week,clockMinute,copyBlockPreset,blockOverlap,validSelection} from '../../../../../../../packages/client-core/src/server-admin/owner-channel-draft';
import {newChannelOperation} from '../../../../../../../packages/client-core/src/server-admin/owner-library-channels';
import type {NativeChannelsGroup} from '../../../../screens/settings-live/server/channels/model';

/** A channel's time blocks: the list, one block's sheet (from a preset or by hand, its own titles, its days and times),
 * and a save refused while times overlap or are unreadable. Moved from the old field as it was. */
export function useChannelHours({group,value,locked,onChange}:{group:NativeChannelsGroup;value:LibraryChannelBuilder;locked:boolean;onChange:(value:LibraryChannelBuilder)=>void}){
 const state=useSyncExternalStore(group.source.subscribe,group.source.getSnapshot),[opening,setOpening]=useState<{base:LibraryChannelBuilder;block:LibraryChannelBlock;original?:LibraryChannelBlock}>(),[tried,setTried]=useState(false);
 const held=opening?.base===value?opening:undefined,blocks=value.blocks??[],draft=held?.block;
 const change=(blocks:LibraryChannelBlock[])=>{if(!locked&&group.current())onChange({...value,libraries:[...new Set([...value.libraries,...blocks.flatMap(v=>v.selection?.libraries??[])])],blocks});};
 const edit=(patch:Partial<LibraryChannelBlock>)=>{if(held&&!locked&&group.current())setOpening({...held,block:{...held.block,...patch}});};
 const open=(block?:LibraryChannelBlock)=>{if(locked||!group.current())return;setTried(false);setOpening({base:value,original:block,block:clonePortableDto(block??{id:newChannelOperation(),name:'',days:[...week],from:'18:00',to:'20:00'})});void group.source.presets();};
 const close=()=>{if(!locked)setOpening(undefined);};
 const save=()=>{if(!held||locked||!group.current())return;setTried(true);const next=held.original?blocks.map(v=>v===held.original?held.block:v):[...blocks,held.block];if(!draft?.days.length||clockMinute(draft.from)===undefined||clockMinute(draft.to)===undefined||draft.selection&&!validSelection(draft.selection)||blockOverlap(next))return;change(next);setOpening(undefined);};
 const preset=(id:string)=>{const found=state.presets.find(v=>v.id===id);if(found&&held&&!locked&&draft){const kind=group.libraries.find(v=>v.id===found.library)?.kind??'';setOpening({...held,block:copyBlockPreset(found,kind,draft.id)});}};
 return {state,held,blocks,draft,tried,change,edit,open,close,save,preset};
}
