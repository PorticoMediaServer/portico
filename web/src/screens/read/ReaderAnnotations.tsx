import {useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import {randomId} from '@core/random-id.ts';
import {ReadingAnnotationService, type ReadingAnnotationState} from '@core/reading-annotation-service.ts';
import type {ReadingAnnotation, ReadingAnnotationContent, ReadingAnnotationPort} from '@core/reading-annotations.ts';
import {readingLocationFromReader, readerLocationFromReading, type ReadingController, type ReaderSnapshot, type ReaderSelection} from '@portico/reader-core';
import type {ReadingPreferences} from '@portico/client-core';
import type {ReaderMountAuthority} from './mount';
import {Button} from '../../kit/controls';
import {Dialog} from '../../kit/overlays/Dialog';
import styles from './annotations.module.css';
const t = defaultI18n.t;
const blank: ReadingAnnotationState = Object.freeze({entries: [], nextCursor: '', loading: false, saving: false});
const noSubscribe = () => () => {}, noSnapshot = () => blank;

export function useReaderAnnotations(port: ReadingAnnotationPort | undefined, controller: ReadingController, authority: ReaderMountAuthority, active: boolean, ready: boolean) {
  const [owned, setOwned] = useState<Readonly<{port: ReadingAnnotationPort; service: ReadingAnnotationService}>>();
  if (owned && (owned.port !== port || !active)) owned.service.dispose();
  useLayoutEffect(() => {
    if (!port || port.manifest.canAnnotate !== true || !active) return;
    const service = new ReadingAnnotationService(port, () => {
      const state = controller.getSnapshot();
      return authority.isCurrent() && state.sourceId === port.manifest.sourceId && state.sourceRevision === port.manifest.revision && state.status === 'ready';
    });
    setOwned({port, service});
    return () => service.dispose();
  }, [port, controller, authority, active]);
  const service = owned && owned.port === port && active ? owned.service : undefined;
  const state = useSyncExternalStore(service?.subscribe ?? noSubscribe, service?.getSnapshot ?? noSnapshot, noSnapshot);
  useEffect(() => {if (service && ready) void service.load();}, [service, ready]);
  return {service, state};
}
const colours = ['yellow','green','blue','pink'] as const;
const colourLabels = {yellow:'next.reader.annotationYellow',green:'next.reader.annotationGreen',blue:'next.reader.annotationBlue',pink:'next.reader.annotationPink'} as const;
function selectedContent(selection: ReaderSelection, colour: typeof colours[number]): ReadingAnnotationContent | undefined {
  if (!selection.text.trim() || new TextEncoder().encode(selection.text).length > 8192 || selection.location.format === 'cbz'
    || selection.location.format === 'pdf' && (!selection.pageRects?.length || selection.rects.length > 128)) return;
  return Object.freeze({id: randomId(), kind:'highlight', location:readingLocationFromReader(selection.location), text:selection.text, note:'', colour,
    ...(selection.location.format === 'pdf' ? {rects:selection.pageRects} : {})});
}
type AnnotationLease = {owner: object; life: AbortController; retired: boolean};
function retire(lease: AnnotationLease | undefined) {if (lease) {lease.retired=true;lease.life.abort();}}
export function useAnnotationLease(owner: object | undefined) {
  const [lease,setLease]=useState<AnnotationLease>();
  if (lease && lease.owner!==owner) retire(lease);
  useLayoutEffect(()=>{
    if (!owner) return;
    const next={owner,life:new AbortController(),retired:false};setLease(next);
    return ()=>retire(next);
  },[owner]);
  return lease&&lease.owner===owner&&!lease.retired?lease:undefined;
}
const live = (lease: AnnotationLease | undefined) => !!lease&&!lease.retired&&!lease.life.signal.aborted;
type NoteOpening = Readonly<{id:string; owner:ReadingAnnotationService; life:AbortController; content:ReadingAnnotationContent; revision:number; member?:ReadingAnnotation}>;
function useNoteOpening() {
  const [opening,setOpening]=useState<NoteOpening>();
  const current=useRef<NoteOpening | undefined>(undefined);
  const open=(next:NoteOpening)=>{current.current?.life.abort();current.current=next;setOpening(next);};
  const close=(original:NoteOpening)=>{original.life.abort();if(current.current===original){current.current=undefined;setOpening(undefined);}};
  return {opening,open,close};
}
function AnnotationNoteEditor({opening,service,state,signal,onClose}: {opening:NoteOpening;service:ReadingAnnotationService;state:ReadingAnnotationState;signal:AbortSignal;onClose:()=>void}) {
  const lease=useAnnotationLease(opening);
  type Draft={note:string;life:AbortController;retired:boolean;claimed:boolean};
  const [draft,setDraft]=useState<Draft>(()=>({note:opening.content.note,life:new AbortController(),retired:false,claimed:false}));
  const offered=useRef(draft);
  const owns=()=>live(lease)&&!opening.life.signal.aborted&&!signal.aborted&&!draft.retired&&offered.current===draft&&service.current()
    &&(!opening.member||service.getSnapshot().entries.includes(opening.member));
  const close=()=>{if (!live(lease)||offered.current!==draft) return;draft.retired=true;draft.life.abort();retire(lease);onClose();};
  
  return <Dialog open={live(lease)&&!opening.life.signal.aborted&&!signal.aborted} onOpenChange={open=>{if(!open)close();}} title={t('next.reader.annotationEdit')} size="sm"
    footer={<><Button onClick={close}>{t('action.cancel')}</Button><Button disabled={state.loading||state.saving||state.revision!==opening.revision||draft.claimed} onClick={()=>{
      if(!lease||!owns()||draft.claimed)return;
      draft.claimed=true;
      const callerSignal=AbortSignal.any([signal,opening.life.signal,lease.life.signal,draft.life.signal]);
      void service.write({...opening.content,note:draft.note},{revision:opening.revision,signal:callerSignal,isCurrent:owns}).then(saved=>{if(saved&&live(lease)&&offered.current===draft)close();});
    }}>{t('next.reader.annotationSave')}</Button></>}>
    <blockquote>{opening.content.text}</blockquote><label>{t('next.reader.note')}<textarea value={draft.note} maxLength={4096} disabled={state.saving} onChange={event=>{
      if(!owns()||draft.claimed)return;
      draft.retired=true;draft.life.abort();
      const next={note:event.currentTarget.value,life:new AbortController(),retired:false,claimed:false};offered.current=next;setDraft(next);
    }}/></label>
    {!!state.error&&<><p role="alert">{t('next.reader.annotationFailed')}</p><p>{t('next.reader.annotationReopen')}</p><Button disabled={state.loading||state.saving} onClick={()=>{if(live(lease)&&!signal.aborted&&!opening.life.signal.aborted)void service.load();}}>{t('next.reader.annotationReload')}</Button></>}
  </Dialog>;
}
export function ReaderAnnotationActions({service, snapshot, controller, onSearch, reportError}: {
  service?: ReadingAnnotationService; snapshot: ReaderSnapshot<ReadingPreferences>; controller: ReadingController; onSearch?: (text: string) => void; reportError: (error: unknown) => void;
}) {
  const lease=useAnnotationLease(service);
  const {opening,open,close}=useNoteOpening();
  const [colour,setColour]=useState<typeof colours[number]>('yellow');
  const state=useSyncExternalStore(service?.subscribe??noSubscribe,service?.getSnapshot??noSnapshot,noSnapshot);
  const selection=snapshot.selection;
  const current=()=>live(lease)&&!!service?.current()&&controller.getSnapshot().sourceId===snapshot.sourceId&&controller.getSnapshot().sourceRevision===snapshot.sourceRevision;
  if(!service?.current())return null;
  const busy=state.loading||state.saving||state.revision===undefined;
  const selectionSupported=!!selection?.text.trim()&&new TextEncoder().encode(selection.text).length<=8192&&selection.location.format!=='cbz'&&(selection.location.format!=='pdf'||!!selection.pageRects?.length&&selection.rects.length<=128);
  const capture=()=>current()&&controller.getSnapshot().selection===selection&&selection?selectedContent(selection,colour):undefined;
  const ownsSelection=()=>current()&&controller.getSnapshot().selection===selection;
  return <>
    {selection&&<div className={styles.selection} role="toolbar" aria-label={t('next.reader.selectionActions')}>
      {!selectionSupported&&<span role="status">{t('next.reader.annotationUnsupportedSelection')}</span>}
      {colours.map(value=><button key={value} type="button" data-colour={value} aria-label={t(colourLabels[value])} aria-pressed={value===colour} onPointerDown={event=>event.preventDefault()} onMouseDown={event=>event.preventDefault()} onClick={()=>{if(current())setColour(value);}}/>)}
      <Button disabled={busy||!selectionSupported} onPointerDown={event=>event.preventDefault()} onMouseDown={event=>event.preventDefault()} onClick={()=>{const content=capture();if(content&&lease)void service.write(content,{revision:state.revision,signal:lease.life.signal,isCurrent:ownsSelection});}}>{t('next.reader.addHighlight')}</Button>
      <Button disabled={busy||!selectionSupported} onPointerDown={event=>event.preventDefault()} onMouseDown={event=>event.preventDefault()} onClick={()=>{const content=capture();if(content&&state.revision!==undefined)open({id:randomId(),owner:service,life:new AbortController(),content,revision:state.revision});}}>{t('next.reader.addNote')}</Button>
      {navigator.clipboard&&<Button onPointerDown={event=>event.preventDefault()} onMouseDown={event=>event.preventDefault()} onClick={()=>{if(ownsSelection())void navigator.clipboard.writeText(selection.text).catch(reportError);}}>{t('next.reader.copySelection')}</Button>}
      {onSearch&&selection.text.length<=1024&&<Button onPointerDown={event=>event.preventDefault()} onMouseDown={event=>event.preventDefault()} onClick={()=>{if(ownsSelection())onSearch(selection.text);}}>{t('next.reader.searchSelection')}</Button>}
    </div>}
    {opening?.owner===service&&lease&&<AnnotationNoteEditor key={opening.id} opening={opening} service={service} state={state} signal={lease.life.signal} onClose={()=>close(opening)}/>}
  </>;
}
export function ReaderAnnotationList({kind,service,state,controller,reportError,signal}: {kind:'bookmark'|'highlight';service:ReadingAnnotationService;state:ReadingAnnotationState;controller:ReadingController;reportError:(error:unknown)=>void;signal:AbortSignal}) {
  const lease=useAnnotationLease(signal);
  const {opening,open,close}=useNoteOpening();
  const entries=state.entries.filter(entry=>entry.kind===kind),busy=state.loading||state.saving||state.revision===undefined;
  const owns=(entry:ReadingAnnotation)=>live(lease)&&!signal.aborted&&service.current()&&service.getSnapshot().entries.includes(entry);
  return <div className={styles.list}>
    {!!state.error&&<p role="alert">{t('next.reader.annotationFailed')}</p>}
    {!!state.error&&<Button disabled={state.loading||state.saving} onClick={()=>{if(live(lease)&&!signal.aborted)void service.load();}}>{t('next.reader.annotationReload')}</Button>}
    {state.loading&&<p role="status">{t('status.loading')}</p>}
    {!state.loading&&!state.error&&state.revision!==undefined&&!entries.length&&<p>{t('next.reader.annotationEmpty')}</p>}
    {entries.map(entry=><article key={entry.id} data-colour={entry.colour}>
      <button type="button" className={styles.anchor} onClick={()=>{if(owns(entry)&&lease)void controller.navigate({kind:'location',location:readerLocationFromReading(entry.location)},AbortSignal.any([signal,lease.life.signal])).catch(error=>{if(owns(entry))reportError(error);});}}>
        {entry.text||(entry.location.format==='epub'?entry.location.section||t('next.reader.bookmarks'):typeof entry.location.pageIndex==='number'?t('next.reader.pageLabel',{page:defaultI18n.number(entry.location.pageIndex+1)}):t('next.reader.bookmarks'))}
      </button>
      {entry.note&&<p>{entry.note}</p>}<time dateTime={new Date(entry.updatedAt).toISOString()}>{defaultI18n.dateTime(entry.updatedAt)}</time>
      <Button disabled={busy} onClick={()=>{if(owns(entry)&&state.revision!==undefined)open({id:randomId(),owner:service,life:new AbortController(),content:entry,member:entry,revision:state.revision});}}>{t('next.reader.annotationEdit')}</Button>
      <Button disabled={busy} onClick={()=>{if(owns(entry)&&lease)void service.write({id:entry.id,remove:true},{revision:state.revision,signal:AbortSignal.any([signal,lease.life.signal]),isCurrent:()=>owns(entry)});}}>{t('next.reader.annotationDelete')}</Button>
    </article>)}
    {state.nextCursor&&<Button disabled={busy} onClick={()=>{if(live(lease)&&!signal.aborted)void service.load(true);}}>{t('next.reader.annotationMore')}</Button>}
    {opening?.owner===service&&lease&&<AnnotationNoteEditor key={opening.id} opening={opening} service={service} state={state} signal={AbortSignal.any([signal,lease.life.signal])} onClose={()=>close(opening)}/>}
  </div>;
}
