import {useEffect,useState,useSyncExternalStore} from 'react';
import {validBuilder} from '../../../../../../../packages/client-core/src/server-admin/owner-channel-draft';
import type {NativeChannelsGroup} from '../../../../screens/settings-live/server/channels/model';
import type {Editor} from '../../../../screens/settings-live/server/channels/source';

/** One channel's editor: its draft, a preview asked for 600 ms after a valid change, Save refused until the name, time
 * zone and rules are valid, and the keep-my-edits and refresh answers to a channel changed elsewhere. Moved from the
 * old editor as it was. */
export function useChannelEditor({group,editor}:{group:NativeChannelsGroup;editor:Editor}){
 const {source}=group,state=useSyncExternalStore(source.subscribe,source.getSnapshot),[advanced,setAdvanced]=useState(false),[tried,setTried]=useState(false),[previewOpen,setPreviewOpen]=useState<typeof state.preview>(),draft=editor.draft,builder=draft.builder;
 const locked=!!(state.busy||state.uncertain||state.pendingCreate||state.pendingPreset),current=()=>group.current()&&source.getSnapshot().editor===editor;
 const change=(patch:Partial<typeof draft>)=>{if(current())source.edit(editor,value=>({...value,...patch}));};
 let zoneValid=false;try{if(draft.timeZone){new Intl.DateTimeFormat(undefined,{timeZone:draft.timeZone});zoneValid=true;}}catch{/* Keep invalid text editable. */}
 const valid=!!draft.name.trim()&&zoneValid&&validBuilder(builder),preview=state.previewFor===draft?state.preview:undefined;
 useEffect(()=>{if(!valid||locked||editor.conflict)return;const timer=setTimeout(()=>{if(group.current()&&source.getSnapshot().editor===editor)void source.preview(editor);},600);return()=>clearTimeout(timer);},[source,editor,valid,locked,group]);
 const close=()=>{if(current()&&!state.busy){if(previewOpen)setPreviewOpen(undefined);else source.close(editor);}};
 return {source,state,advanced,setAdvanced,tried,previewOpen,setPreviewOpen,draft,builder,locked,current,change,zoneValid,valid,preview,close,
  save:()=>{if(!current())return;setTried(true);if(valid)void source.save(editor);},
  acceptLocal:()=>{if(current())source.acceptLocal(editor);},
  reload:()=>{if(current())void source.reloadEditor(editor);},
  showPreview:()=>{if(current()&&preview)setPreviewOpen(preview);}};
}
