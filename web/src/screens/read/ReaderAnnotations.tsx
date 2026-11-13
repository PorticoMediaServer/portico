import {useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import {randomId} from '@core/random-id.ts';
import {ReadingAnnotationService, type ReadingAnnotationState} from '@core/reading-annotation-service.ts';
import type {ReadingAnnotation, ReadingAnnotationContent, ReadingAnnotationPort} from '@core/reading-annotations.ts';
import {readingLocationFromReader, readerLocationFromReading, type ReadingController, type ReaderLocation, type ReaderSnapshot, type ReaderSelection, type ReaderTocItem} from '@portico/reader-core';
import type {ReadingPreferences} from '@portico/client-core';
import type {ReaderMountAuthority} from './mount';
import {Button} from '../../kit/controls';
import {Dialog} from '../../kit/overlays/Dialog';
import {Menu, MenuItem} from '../../kit/overlays';
import {ReaderAnnotations, ReaderBookmarks, ReaderPanelEmpty, ReaderPanelSection} from '../../kit/reader/ReaderPanel';
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
/** The annotations once nothing is loading or saving, or undefined when the book closes first. */
function settled(service: ReadingAnnotationService, signal: AbortSignal): Promise<ReadingAnnotationState | undefined> {
  return new Promise(resolve => {
    const check = () => {
      const state = service.getSnapshot();
      if (signal.aborted || !service.current()) {off(); resolve(undefined);}
      else if (!state.loading && !state.saving && state.revision !== undefined) {off(); resolve(state);}
    };
    const off = service.subscribe(check); signal.addEventListener('abort', check, {once: true}); check();
  });
}
/** The search reads at most this many matches; words found this often or more are not placed. */
const placeLimit = 200;
type Remembered = Record<string, ReadingAnnotation['location'] | null>;
const remembered = (key: string): Remembered => {try {const value = JSON.parse(globalThis.localStorage?.getItem(key) ?? '{}'); return value && typeof value === 'object' ? value : {};} catch {return {};}};
const remember = (key: string, value: Remembered) => {try {globalThis.localStorage?.setItem(key, JSON.stringify(value));} catch { /* without storage the next open searches again */ }};
/**
 * 10-reading rule 27: a highlight from an earlier copy of this book is looked for by its words in
 * this copy. It is placed only when the search returned every match (fewer than 200), at the one
 * nearest where it was. While the copy it was made on is still kept (another version), it is drawn
 * here for this open only, never saved, so it stays where it was in that copy. When that copy is
 * gone (its file replaced or removed), it is moved here by saving it on this copy. One not placed
 * stays listed under "From an earlier copy of this book". What each search found is remembered in
 * this browser per copy and highlight, so a book is searched once per copy, not on every open.
 * The search reads up to 1,024 characters, so a longer highlight stays listed.
 */
export function usePlaceEarlierCopies(service: ReadingAnnotationService | undefined, controller: ReadingController, ready: boolean, copy: string): ReadonlyMap<string, ReadingAnnotation['location']> {
  const [placed, setPlaced] = useState<ReadonlyMap<string, ReadingAnnotation['location']>>(new Map());
  useEffect(() => {
    if (!service || !ready) return;
    const life = new AbortController(), key = 'portico.reader.earlierCopy.v1:' + copy, memory = remembered(key), shown = new Map<string, ReadingAnnotation['location']>(), tried = new Set<string>();
    setPlaced(new Map());
    void (async () => {
      for (;;) {
        const state = await settled(service, life.signal);
        const next = state?.entries.find(entry => entry.earlierCopy && entry.kind === 'highlight' && entry.colour && entry.location.format === 'epub'
          && entry.text.trim() && entry.text.length <= 1024 && !tried.has(entry.id));
        if (!state || !next) return;
        tried.add(next.id);
        const memoryKey = next.id + '@' + next.updatedAt;
        let found = memory[memoryKey];
        if (found === undefined) {
          let best: ReaderLocation | undefined, count = 0;
          for await (const hit of controller.search(next.text, {signal: life.signal, limit: placeLimit})) {
            count++;
            if (hit.location?.format === 'epub' && (!best || Math.abs(hit.location.progression - next.location.progression) < Math.abs(best.progression - next.location.progression))) best = hit.location;
          }
          if (life.signal.aborted) return;
          found = best && count < placeLimit ? readingLocationFromReader(best) : null;
          memory[memoryKey] = found; remember(key, memory);
        }
        if (!found) continue;
        if (next.earlierCopyGone) await service.write({id: next.id, kind: 'highlight', location: found, text: next.text, note: next.note, colour: next.colour},
          {revision: state.revision, signal: life.signal, isCurrent: () => !life.signal.aborted});
        else {shown.set(next.id, found); setPlaced(new Map(shown));}
      }
    })().catch(() => { /* a search or save that fails leaves the highlight listed from the earlier copy */ });
    return () => life.abort();
  }, [service, controller, ready, copy]);
  return placed;
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
/** Where a text-less bookmark is: its chapter's title from the book's contents, else its page, else
 * how far through the book it is. An EPUB's saved section is a package path (OEBPS/chapter3.xhtml),
 * never shown (SRV-A2). */
/** The chapter a mark is in, when the contents name it. */
function bookmarkChapter(location:ReadingAnnotation['location'],toc:readonly ReaderTocItem[]):string|undefined {
  if(location.format!=='epub')return typeof location.pageIndex==='number'?t('next.reader.pageLabel',{page:defaultI18n.number(location.pageIndex+1)}):undefined;
  const find=(items:readonly ReaderTocItem[]):string|undefined=>{for(const item of items){if(item.location.format==='epub'&&location.section!==undefined&&item.location.section===location.section)return item.label;const inner=item.children&&find(item.children);if(inner)return inner;}return undefined;};
  return find(toc);
}
export function bookmarkPlace(location:ReadingAnnotation['location'],toc:readonly ReaderTocItem[]):string {
  if(location.format==='epub'){
    const find=(items:readonly ReaderTocItem[]):string|undefined=>{for(const item of items){if(item.location.format==='epub'&&location.section!==undefined&&item.location.section===location.section)return item.label;const inner=item.children&&find(item.children);if(inner)return inner;}return undefined;};
    const title=find(toc);if(title)return title;
  }else if(typeof location.pageIndex==='number')return t('next.reader.pageLabel',{page:defaultI18n.number(location.pageIndex+1)});
  return Number.isFinite(location.progression)?defaultI18n.percent(location.progression):t('next.reader.bookmarks');
}
export function ReaderAnnotationList({kind,service,state,controller,reportError,signal,placed}: {kind:'bookmark'|'highlight';service:ReadingAnnotationService;state:ReadingAnnotationState;controller:ReadingController;reportError:(error:unknown)=>void;signal:AbortSignal;
  /** Earlier-copy highlights found in this copy for this open: listed with the others, not editable here. */ placed?:ReadonlyMap<string,ReadingAnnotation['location']>}) {
  const lease=useAnnotationLease(signal);
  const {opening,open,close}=useNoteOpening();
  const toc=useSyncExternalStore(controller.subscribe,()=>controller.getSnapshot().toc,()=>controller.getSnapshot().toc);
  const all=state.entries.filter(entry=>entry.kind===kind),entries=all.filter(entry=>!entry.earlierCopy||placed?.has(entry.id)),earlier=all.filter(entry=>entry.earlierCopy&&!placed?.has(entry.id)),busy=state.loading||state.saving||state.revision===undefined;
  const owns=(entry:ReadingAnnotation)=>live(lease)&&!signal.aborted&&service.current()&&service.getSnapshot().entries.includes(entry);
  const [menu,setMenu]=useState<Readonly<{entry:ReadingAnnotation;anchor:HTMLButtonElement}>>();
  const go=(entry:ReadingAnnotation)=>{if(owns(entry)&&lease)void controller.navigate({kind:'location',location:readerLocationFromReading(placed?.get(entry.id)??entry.location)},AbortSignal.any([signal,lease.life.signal])).catch(error=>{if(owns(entry))reportError(error);});};
  const more=(entry:ReadingAnnotation)=>({onMore:(anchor:HTMLButtonElement)=>{if(owns(entry))setMenu({entry,anchor});},moreLabel:t('next.controls.more')});
  const when=(entry:ReadingAnnotation)=>defaultI18n.date(entry.updatedAt,'medium');
  const highlight=(entry:ReadingAnnotation,navigable:boolean)=>({id:entry.id,quote:entry.text||defaultI18n.percent(entry.location.progression),note:entry.note||undefined,
    chapter:bookmarkChapter(entry.location,toc),date:when(entry),colour:entry.colour||'yellow',...(navigable?{onNavigate:()=>go(entry)}:{}),...more(entry)} as const);
  const remove=(entry:ReadingAnnotation)=>{setMenu(undefined);if(owns(entry)&&lease)void service.write({id:entry.id,remove:true},{revision:state.revision,signal:AbortSignal.any([signal,lease.life.signal]),isCurrent:()=>owns(entry)});};
  return <div className={styles.list}>
    {!!state.error&&<ReaderPanelEmpty role="alert">{t('next.reader.annotationFailed')}</ReaderPanelEmpty>}
    {!!state.error&&<Button disabled={state.loading||state.saving} onClick={()=>{if(live(lease)&&!signal.aborted)void service.load();}}>{t('next.reader.annotationReload')}</Button>}
    {state.loading&&<ReaderPanelEmpty role="status">{t('status.loading')}</ReaderPanelEmpty>}
    {!state.loading&&!state.error&&state.revision!==undefined&&!all.length&&<ReaderPanelEmpty>{t('next.reader.annotationEmpty')}</ReaderPanelEmpty>}
    {kind==='bookmark'?<ReaderBookmarks entries={entries.map(entry=>({id:entry.id,place:bookmarkPlace(entry.location,toc),excerpt:entry.note||undefined,date:when(entry),onNavigate:()=>go(entry),...more(entry)}))}/>
      :<ReaderAnnotations entries={entries.map(entry=>highlight(entry,true))}/>}
    {/* 10-reading rule 27: one from an earlier copy whose words are not found in this one is kept here, with its words and note, and can only be removed. */}
    {earlier.length>0&&<ReaderPanelSection>{t('next.reader.earlierCopy')}</ReaderPanelSection>}
    {earlier.length>0&&<ReaderAnnotations entries={earlier.map(entry=>highlight(entry,false))}/>}
    {state.nextCursor&&<Button disabled={busy} onClick={()=>{if(live(lease)&&!signal.aborted)void service.load(true);}}>{t('next.reader.annotationMore')}</Button>}
    {menu&&<Menu open onOpenChange={open=>{if(!open)setMenu(undefined);}} anchorRef={{current:menu.anchor}} returnFocusRef={{current:menu.anchor}} title={menu.entry.text||bookmarkPlace(menu.entry.location,toc)}>
      {!menu.entry.earlierCopy&&<MenuItem icon="i-note2" disabled={busy} onSelect={()=>{const entry=menu.entry;setMenu(undefined);if(owns(entry)&&state.revision!==undefined)open({id:randomId(),owner:service,life:new AbortController(),content:entry,member:entry,revision:state.revision});}}>{t('next.reader.annotationEdit')}</MenuItem>}
      <MenuItem icon="i-trash" disabled={busy} onSelect={()=>remove(menu.entry)}>{t('next.reader.annotationDelete')}</MenuItem>
    </Menu>}
    {opening?.owner===service&&lease&&<AnnotationNoteEditor key={opening.id} opening={opening} service={service} state={state} signal={AbortSignal.any([signal,lease.life.signal])} onClose={()=>close(opening)}/>}
  </div>;
}
