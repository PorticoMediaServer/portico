import type {SavedDownloadsPort,SavedDownloadsSnapshot,SavedDownloadsError,SavedLocalDownload} from '../../../web/src/screens/saved/downloads-port.ts';
import type {DesktopDownloadStore,DownloadCursor,LocalDownload,LocalSelection} from './downloads.ts';

/** All three operations are provided by the native folder owner, never a web path. */
export type DesktopDownloadsFolder=Readonly<{
  read(signal:AbortSignal):Promise<Readonly<{displayPath:string}>>;
  choose(signal:AbortSignal):Promise<void>;
  open(signal:AbortSignal):Promise<void>;
}>;
export type DesktopSavedPlayback=Readonly<{
  play(selection:Extract<LocalSelection,{kind:'entry'}>,signal:AbortSignal):Promise<void>;
  /** Present only with the genuine complete ordered local queue consumer. */
  playGroup?:(selection:Extract<LocalSelection,{kind:'group'}>,signal:AbortSignal)=>Promise<void>;
}>;
const closed:SavedDownloadsSnapshot=Object.freeze({available:false,phase:'loading',items:Object.freeze([])});
function row(value:LocalDownload):SavedLocalDownload {
  return Object.freeze({id:value.id,revision:value.revision,preparationId:value.preparationId,itemId:value.itemId,
    quality:value.quality,title:value.title,state:value.state,held:value.held,bytes:value.bytes,bytesDone:value.bytesDone,
    percent:value.bytes>0?100*value.bytesDone/value.bytes:null,offlineAccess:value.offlineAccess,onServer:value.title.onServer});
}
function failure(error:unknown,fallback:SavedDownloadsError):SavedDownloadsError {
  return error instanceof Error&&error.message==='downloads_conflict'?'conflict':fallback;
}

/** Projects the one native store into Saved. It observes native bytes only: no
 * server preparation subscription, transfer, auth, file URL or second local queue.
 * The captured parent owns the shared store/coordinator and closes them together. */
export function createDesktopSavedDownloadsPort({store,folder,playback,signal,wake}:Readonly<{
  store:DesktopDownloadStore;folder:DesktopDownloadsFolder;playback:DesktopSavedPlayback;signal:AbortSignal;wake:()=>void;
}>):SavedDownloadsPort {
  const lifetime=new AbortController(),listeners=new Set<()=>void>();
  let disposed=false,readRevision=0,loadedPages=1,cursor:DownloadCursor|null=null;
  let refreshTimer:ReturnType<typeof setTimeout>|undefined;
  let snapshot:SavedDownloadsSnapshot=Object.freeze({...closed,available:true});
  const current=()=>{if(disposed||signal.aborted)return false;try{store.assertCurrent();return true;}catch{return false;}};
  const active=(input:AbortSignal)=>{const combined=AbortSignal.any([input,signal,lifetime.signal]);combined.throwIfAborted();store.assertCurrent(combined);if(disposed)throw new DOMException('Aborted','AbortError');return combined;};
  const emit=()=>{if(current())for(const listener of [...listeners])listener();};
  const publish=(next:SavedDownloadsSnapshot)=>{snapshot=Object.freeze(next);emit();};
  async function read(input:AbortSignal,more:boolean):Promise<void>{
    const combined=active(input),after=more?cursor:null;
    if(more&&!after)return;
    const revision=++readRevision;
    try{
      const storage=store.capacity(combined).catch(()=>{active(combined);return undefined;});
      const [page,groups,measuredStorage,rules,path]=await Promise.all([store.list(after,combined),store.groups(combined),storage,store.rules(combined),folder.read(combined)]);
      active(combined);if(revision!==readRevision)return;
      if(typeof path.displayPath!=='string'||!path.displayPath||path.displayPath.length>32768||/[\x00-\x1f\x7f]/.test(path.displayPath))throw new Error('downloads_unavailable');
      const items=new Map((more?snapshot.items:[]).map(item=>[item.id,item]));
      for(const item of page.items)items.set(item.id,row(item));
      let next=page.nextCursor,pages=1;
      if(!more)while(next&&pages<loadedPages){
        const following=await store.list(next,combined);active(combined);if(revision!==readRevision)return;
        for(const item of following.items)items.set(item.id,row(item));next=following.nextCursor;++pages;
      }
      cursor=next;loadedPages=more?loadedPages+1:pages;
      publish({available:true,phase:'ready',items:Object.freeze([...items.values()]),groups,...(measuredStorage?{storage:measuredStorage}:{}),
        rules:Object.freeze({revision:rules.revision,value:rules.rules}),folder:Object.freeze({displayPath:path.displayPath}),
        ...(cursor?{nextCursor:JSON.stringify(cursor)}:{})});
    }catch(error){
      if(combined.aborted||!current())throw new DOMException('Aborted','AbortError');
      if(revision!==readRevision)return;
      if(revision===readRevision)publish({...snapshot,available:true,phase:'error',error:failure(error,'read')});
      throw new Error(failure(error,'read'));
    }
  }
  async function write(input:AbortSignal,effect:(signal:AbortSignal)=>Promise<unknown>,kind:SavedDownloadsError='write'){
    const combined=active(input);
    try{await effect(combined);active(combined);wake();await read(combined,false);}
    catch(error){if(combined.aborted||!current())throw new DOMException('Aborted','AbortError');throw new Error(failure(error,kind));}
  }
  const off=store.subscribe(()=>{
    if(!current()||!listeners.size||refreshTimer!==undefined)return;
    refreshTimer=setTimeout(()=>{refreshTimer=undefined;void read(lifetime.signal,false).catch(()=>{});},100);
  });
  const dispose=()=>{if(disposed)return;disposed=true;++readRevision;lifetime.abort();off();if(refreshTimer!==undefined)clearTimeout(refreshTimer);listeners.clear();snapshot=closed;cursor=null;signal.removeEventListener('abort',dispose);};
  signal.addEventListener('abort',dispose,{once:true});if(signal.aborted)dispose();
  const playGroup:SavedDownloadsPort['playGroup']=playback.playGroup?async(selector,expectedRevision,input)=>{
    const combined=active(input);
    const group=snapshot.groups?.find(group=>group.kind===selector.kind&&group.parentId===selector.parentId&&group.groupId===(selector.groupId??'')&&group.revision===expectedRevision);
    if(!group||group.state!=='ready'||group.held!==false||group.offlineAccess!=='valid')throw new Error('play');
    try{await playback.playGroup!({kind:'group',selector,expectedRevision},combined);active(combined);}
    catch(error){if(combined.aborted||!current())throw new DOMException('Aborted','AbortError');throw new Error('play');}
  }:undefined;
  const port:SavedDownloadsPort={
    scope:store.scope,
    getSnapshot:()=>current()?snapshot:closed,
    subscribe(listener){if(!current())return()=>{};listeners.add(listener);return()=>{listeners.delete(listener);};},
    refresh:input=>read(input,false),loadMore:input=>read(input,true),
    action:(id,expectedRevision,action,input)=>write(input,async combined=>{
      const entry={id,revision:expectedRevision};
      if(action==='pause'||action==='resume')await store.hold(entry,action==='pause',combined);
      else if(action==='retry')await store.retry(entry,combined);
      else if(action==='remove')await store.remove(entry,combined);
      else throw new Error('invalid_command');
    }),
    async play(id,input){
      const combined=active(input),entry=snapshot.items.find(item=>item.id===id);
      if(!entry||entry.state!=='ready'||entry.held||entry.offlineAccess!=='valid')throw new Error('play');
      try{await playback.play({kind:'entry',entryId:id,expectedRevision:entry.revision},combined);active(combined);}
      catch(error){if(combined.aborted||!current())throw new DOMException('Aborted','AbortError');throw new Error('play');}
    },
    getArtwork:(id,input)=>store.getArtwork(id,active(input)),
    actionGroup:(selector,expectedRevision,action,input)=>write(input,combined=>store.actionGroup(selector,expectedRevision,action,combined)),
    ...(playGroup?{playGroup}:{}),
    async setRules(expectedRevision,value,input){
      const combined=active(input);
      try{const saved=await store.setRules(expectedRevision,value,combined);active(combined);
        publish({...snapshot,rules:Object.freeze({revision:saved.revision,value:saved.rules})});wake();
        // A failed subsequent read cannot turn a committed CAS ACK into a write rejection.
        await read(combined,false).catch(()=>{});active(combined);return Object.freeze({revision:saved.revision});}
      catch(error){if(combined.aborted||!current())throw new DOMException('Aborted','AbortError');throw new Error(failure(error,'write'));}
    },
    chooseFolder:input=>write(input,combined=>folder.choose(combined),'folder'),
    async openFolder(input){const combined=active(input);try{await folder.open(combined);active(combined);}catch(error){if(combined.aborted||!current())throw new DOMException('Aborted','AbortError');throw new Error('folder');}},
    previewOtherProfiles:input=>store.previewOtherProfiles(active(input)),
    removeOtherProfiles:(confirmation,input)=>write(input,combined=>store.removeOtherProfiles(confirmation,combined)),
    dispose,
  };
  return Object.freeze(port);
}
