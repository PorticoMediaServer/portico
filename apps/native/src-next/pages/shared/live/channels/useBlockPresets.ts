import {useState,useSyncExternalStore} from 'react';
import {validConditions,clockMinute} from '../../../../../../../packages/client-core/src/server-admin/owner-channel-draft';
import type {LibraryChannelBlockPreset,LibraryChannelBlockPresetSave} from '../../../../../../../packages/client-core/src/server-admin/owner-library-channels';
import type {NativeChannelsGroup} from '../../../../screens/settings-live/server/channels/model';

/** The block presets sheet: the server's presets, one preset's editor (its name, library, rules, order, length, start and
 * days) checked before it is saved, and one guarded delete. Moved from the old sheet as it was. */
export function useBlockPresets(group:NativeChannelsGroup){
 const {source}=group,state=useSyncExternalStore(source.subscribe,source.getSnapshot),[open,setOpen]=useState(false),[remove,setRemove]=useState<LibraryChannelBlockPreset>(),[tried,setTried]=useState(false),editor=state.presetEditor,draft=editor?.draft;
 const locked=!!(state.busy||state.uncertain||state.pendingPreset||state.pendingCreate),edit=(patch:LibraryChannelBlockPresetSave)=>{if(editor)source.presetEdit(editor,{...editor.draft,...patch});};
 const valid=!!draft?.name.trim()&&!!draft.library&&validConditions(draft.recipe.clauses)&&draft.recipe.minutes>=5&&draft.recipe.minutes<=1440&&Number.isInteger(draft.recipe.minutes)&&(!draft.recipe.startsAt||clockMinute(draft.recipe.startsAt)!==undefined);
 return {source,state,open,remove,setRemove,tried,editor,draft,locked,edit,valid,
  show:()=>{setOpen(true);void source.presets();},
  openChange:(v:boolean)=>{if(!state.busy){setOpen(v);if(!v&&editor)source.presetClose(editor);}},
  openPreset:(row?:LibraryChannelBlockPreset)=>{setTried(false);source.presetOpen(row);},
  save:()=>{if(!editor)return;setTried(true);if(valid)void source.presetSave(editor);},
  confirmRemove:()=>{if(remove)void source.presetRemove(remove).then(()=>{if(group.current())setRemove(undefined);});}};
}
