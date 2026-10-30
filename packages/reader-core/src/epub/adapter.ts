/// <reference path="./foliate.d.ts" />
import {EPUB} from 'foliate-js/epub.js';
import './vendor/paginator.js';
import {fromRange, joinIndir} from 'foliate-js/epubcfi.js';
import {ReaderError, throwIfAborted} from '../errors.ts';
import {watchReaderMotion} from '../motion-policy.ts';
import {abortScope} from '../abort.ts';
import type {ReaderAdapter, ReaderDocument, ReaderLocation, ReaderMetadata, ReaderMount,
  ReaderNavigation, ReaderIndexedSearchHit, ReaderSearchOptions, ReaderSearchResult, ReaderTocItem, ReaderView, ReaderViewport} from '../ports.ts';
import {openEpubArchive, safeArchivePath} from './archive.ts';
import {resourceAssets} from './resources.ts';
import {rejectXmlEntities, secureMarkup, secureCss} from './security.ts';
import {epubDisplay, type EpubDisplayPreferences} from './display.ts';
import {validateEpubRaster} from './media.ts';
import {epubFontKey} from './font-obfuscation.ts';
import {metadataLanguage, metadataText} from './metadata.ts';
import {isWholeWordPhrase} from '../search-boundary.ts';
import {indexedNormalize, indexedRange, indexedRawText, searchWellFormed, indexedPageLimit, indexedOverallLimit, indexedTextLimit, indexedTimeLimit, indexedSectionBudget} from './indexed-text.ts';

type Section = {id: string; size: number; cfi: string; linear?: string; createDocument(): Promise<Document>; resolveHref(href:string):string};
type Navigation = {index: number; anchor?: number | ((doc: Document) => Node | Range | number | null)};
type Toc = {href: string; label: string; subitems?: Toc[]};
type Book = {
  init(): Promise<Book>; landmarks?: {type: string[]; href: string}[]; sections: Section[]; toc?: Toc[]; metadata?: Record<string, unknown>; dir?: string;
  rendition?: {layout?: string}; transformTarget: EventTarget;
  resolveHref(href: string): Navigation | null; resolveCFI(cfi: string): Navigation; destroy(): void;
};
type Relocate = {index: number; range: Range; fraction: number; size?: number};
interface Paginator extends HTMLElement {
  open(book: Book): void; goTo(target: Navigation): Promise<void>; next(distance?:number,signal?:AbortSignal): Promise<void>; prev(distance?:number,signal?:AbortSignal): Promise<void>; setReducedMotion(reduced:boolean):void;
  setStyles(styles: string): void; render(): void; destroy(): void;
  getContents(): {doc: Document; index: number}[];
}
export type EpubAdapterOptions = Readonly<{fonts: Readonly<{literata: Blob; manrope: Blob}>}>;
function cfiAt(book: Book, index: number, range?: Range): string {
  return range ? joinIndir(book.sections[index].cfi, fromRange(range)) : book.sections[index].cfi;
}
function progression(book: Book, index: number, fraction: number): number {
  const sizes = book.sections.map(s=>s.linear==='no'?0:s.size), total = sizes.reduce((a,b)=>a+b,0);
  return total ? Math.max(0,Math.min(1,(sizes.slice(0,index).reduce((a,b)=>a+b,0)+(sizes[index]??0)*fraction)/total)) : 0;
}
function chapterAt(book:Book,index:number,visible:Range,tocBySection:Map<number,{label:string;cfi:string}[]>):string|undefined {
  let nearest:Range|undefined,label:string|undefined;
  for(const item of tocBySection.get(index)??[]) {
    const target=book.resolveCFI(item.cfi);const anchor=typeof target?.anchor==='function'?target.anchor(visible.startContainer.ownerDocument!):null;
    if(!anchor)continue;
    const range=visible.startContainer.ownerDocument!.createRange();
    if((anchor as Range).startContainer)range.setStart((anchor as Range).startContainer,(anchor as Range).startOffset);
    else if((anchor as Node).nodeType)range.selectNodeContents(anchor as Node);else continue;
    range.collapse(true);
    if(range.compareBoundaryPoints(0,visible)<=0&&(!nearest||range.compareBoundaryPoints(0,nearest)>0)){nearest=range;label=item.label;}
  }
  return label;
}

async function cancellable<T>(promise: Promise<T>, signal: AbortSignal): Promise<T> {
  throwIfAborted(signal);
  let abort!: () => void, timer: ReturnType<typeof setTimeout>;
  const stopped = new Promise<never>((_,reject)=>{
    abort=()=>reject(new ReaderError('aborted')); signal.addEventListener('abort',abort,{once:true});
    timer=setTimeout(()=>reject(new ReaderError('render_failed')),15000);
  });
  try {return await Promise.race([promise,stopped]);}
  finally {clearTimeout(timer!);signal.removeEventListener('abort',abort);}
}
/** Direct engine modules only: no stock reader, URL opener, media overlay or
 * ambient network client is imported. Online assets and local ZIPs share this. */
export function createBrowserEpubAdapter<P extends EpubDisplayPreferences>(options: EpubAdapterOptions): ReaderAdapter<P> {
  return {format:'epub',async open({source,signal:parent}) {
    const id=source.id,revision=source.revision,size=source.size,resources=source.resources,contents=source.contents;
    const indexedSearch=source.indexedSearch,indexedSpine=source.spine;
    const spine=indexedSpine===undefined?undefined:Object.freeze([...indexedSpine]);
    const currentSource=()=>source.id===id&&source.revision===revision&&source.size===size&&source.resources===resources&&source.contents===contents&&source.indexedSearch===indexedSearch&&source.spine===indexedSpine;
    const unchanged=()=>{if(!currentSource())throw new ReaderError('invalid_source');};
    const lifetime=new AbortController(),scope=abortScope([parent,lifetime.signal]),signal=scope.signal;
    let assets:Awaited<ReturnType<typeof openEpubArchive>>;
    try{throwIfAborted(signal);assets=source.resources?resourceAssets(source):await openEpubArchive(source,signal);}
    catch(error){scope.dispose();throw error;}
    let book: Book | undefined, mounted=false, disposed=false, view: ReaderView<P>|undefined;
    const byPath=new Map(assets.entries.map(e=>[e.path,e]));
    const decoder=new TextDecoder('utf-8',{fatal:true});
    // Both loader entry points see the same inert publication text. In
    // particular, an ordinary remote anchor must be removed before Foliate
    // parses or rewrites the resource, rather than only at the final Blob.
    function secureText(path:string,mime:string,raw:string):string {
      if(mime==='text/html'||mime==='application/xhtml+xml'||/\.x?html?$/i.test(path)||/<(?:\w+:)?html[\s>]/i.test(raw))
        return secureMarkup(raw,'application/xhtml+xml');
      if(mime==='image/svg+xml'||/\.svg$/i.test(path))return secureMarkup(raw,'image/svg+xml');
      if(mime==='text/css'||/\.css$/i.test(path))return secureCss(raw);
      return rejectXmlEntities(raw);
    }
    async function loadText(path:string,callerSignal:AbortSignal=signal):Promise<string|null> {
      const task=abortScope([signal,callerSignal]),active=task.signal;
      try {throwIfAborted(active);unchanged();
      const entry=byPath.get(safeArchivePath(path));if(!entry)return null;
      const text=decoder.decode(await assets.read(path,active));throwIfAborted(active);unchanged();
      const mime='mime' in entry ? entry.mime : '';
      return secureText(path,mime??'',text);
      }finally{task.dispose();}
    }
    async function loadBlob(path:string):Promise<Blob|null> {
      throwIfAborted(signal);unchanged();const entry=byPath.get(safeArchivePath(path));if(!entry)return null;
      let bytes=await assets.read(path,signal);throwIfAborted(signal);unchanged();
      const mime='mime' in entry && typeof entry.mime==='string'?entry.mime:'';
      if(['text/html','application/xhtml+xml','image/svg+xml','text/css'].includes(mime)||/\.(?:x?html?|svg|css)$/i.test(path))
        bytes=new TextEncoder().encode(secureText(path,mime,decoder.decode(bytes)));
      return new Blob([bytes as Uint8Array<ArrayBuffer>],{type:mime});
    }
    const close=async()=>{if(disposed)return;disposed=true;lifetime.abort();scope.dispose();try{await view?.dispose();}finally{book?.destroy();await assets.dispose();}};
    try {
      unchanged();
      const encryption=await loadText('META-INF/encryption.xml');
      if(encryption) {
        const xml=new DOMParser().parseFromString(encryption,'application/xml');
        if(xml.querySelector('parsererror'))throw new ReaderError('invalid_book');
        for(const method of Array.from(xml.getElementsByTagNameNS('*','EncryptionMethod'))) {
          if(!['http://www.idpf.org/2008/embedding','http://ns.adobe.com/pdf/enc#RC'].includes(method.getAttribute('Algorithm')??''))throw new ReaderError('protected');
        }
      }
      book=await (new EPUB({loadText,loadBlob,getSize:(path:string)=>byPath.get(path)?.size??0,sha1:epubFontKey}) as Book).init();
      throwIfAborted(signal);
      if(!book.sections.length||book.sections.length>2048)throw new ReaderError('invalid_book');
      // Both the pre-loader and post-rewrite transformation are necessary:
      // external resources never enter the loader; CSP precedes iframe parsing.
      book.transformTarget.addEventListener('load',event=>{
        const detail=(event as CustomEvent<{isScript:boolean;allow:boolean}>).detail;if(detail.isScript)detail.allow=false;
      });
      book.transformTarget.addEventListener('data',event=>{
        const detail=(event as CustomEvent<{name?:string;data:string|Blob|Promise<string|Blob>;type:string}>).detail;
        if(detail.type==='text/css')detail.data=Promise.resolve(detail.data).then(async data=>{throwIfAborted(signal);return secureCss(typeof data==='string'?data:await data.text(),true);});
        if(['application/xhtml+xml','text/html','image/svg+xml'].includes(detail.type)) {
          detail.data=Promise.resolve(detail.data).then(async data=>{throwIfAborted(signal);return secureMarkup(typeof data==='string'?data:await data.text(),detail.type as 'application/xhtml+xml',true);});
        } else if(detail.type!=='text/css') {
          detail.data=Promise.resolve(detail.data).then(async data=>{
            throwIfAborted(signal);
            // Inspect media after standard font deobfuscation, before the
            // engine creates a URL/browser decoder can consume it. Obfuscated
            // font bytes can otherwise have a coincidental image signature.
            if(data instanceof Blob) {
              const bytes=new Uint8Array(await data.arrayBuffer());throwIfAborted(signal);
              const observed=validateEpubRaster(bytes,detail.name??'',detail.type);
              if(observed)detail.type=observed.mime;
            }
            return data;
          });
        }
      });
      const b=book;
      const metadata:ReaderMetadata=Object.freeze({title:metadataText(b.metadata?.title),author:metadataText(b.metadata?.author),
        language:metadataLanguage(b.metadata?.language),...(b.dir==='rtl'||b.dir==='ltr'?{direction:b.dir}:{})});
      const tocBySection=new Map<number,{label:string;cfi:string}[]>();
      async function mapToc(items:readonly Toc[],active:AbortSignal):Promise<readonly ReaderTocItem[]> {
        const result:ReaderTocItem[]=[];
        for(const item of items) {
          throwIfAborted(active);unchanged();const target=b.resolveHref(item.href);if(!target||!b.sections[target.index])continue;
          const doc=await b.sections[target.index].createDocument();
          throwIfAborted(active);unchanged();
          const node=typeof target.anchor==='function'?target.anchor(doc):null;
          const range=doc.createRange();if(node instanceof Range)range.setStart(node.startContainer,node.startOffset);
          else if(node instanceof Node)range.selectNodeContents(node);else range.selectNodeContents(doc.body??doc.documentElement);
          range.collapse(true);
          const location:ReaderLocation={format:'epub',cfi:cfiAt(b,target.index,range),section:b.sections[target.index].id,
            progression:progression(b,target.index,(()=>{
              const prefix=doc.createRange();prefix.selectNodeContents(doc.body??doc.documentElement);prefix.setEnd(range.startContainer,range.startOffset);
              return prefix.toString().length/Math.max(1,(doc.body??doc.documentElement).textContent?.length??1);
            })()),chapter:item.label};
          const list=tocBySection.get(target.index)??[];list.push({label:item.label,cfi:location.cfi});tocBySection.set(target.index,list);
          result.push(Object.freeze({id:item.href,label:item.label,location:Object.freeze(location),...(item.subitems?{children:await mapToc(item.subitems,active)}:{})}));
        }return Object.freeze(result);
      }
      let toc:readonly ReaderTocItem[]=Object.freeze([]),tocTask:Promise<readonly ReaderTocItem[]>|undefined;
      const document:ReaderDocument<P>={id,revision,format:'epub',metadata,get toc(){return toc;},
        async loadToc(callerSignal) {
          throwIfAborted(callerSignal);throwIfAborted(signal);unchanged();
          if(!tocTask)tocTask=(async()=>{
            const task=abortScope([signal,callerSignal]);
            try {const resolved=await mapToc(b.toc??[],task.signal);throwIfAborted(task.signal);unchanged();toc=resolved;return toc;}
            finally{task.dispose();}
          })();
          const result=await tocTask;throwIfAborted(callerSignal);throwIfAborted(signal);unchanged();return result;
        },
        async resolveContents(index,callerSignal) {
          const task=abortScope([signal,callerSignal]);
          try {
            throwIfAborted(task.signal);unchanged();
            if(!Number.isSafeInteger(index)||index<0||index>511||!contents||contents.length>512)throw new ReaderError('invalid_location');
            const entry=contents[index];
            if(!entry||typeof entry.section!=='string'||safeArchivePath(entry.section)!==entry.section||
              entry.fragment!==undefined&&(typeof entry.fragment!=='string'||entry.fragment.length>1024||/[\p{Cc}]/u.test(entry.fragment)))throw new ReaderError('invalid_location');
            const {section,fragment}=entry;
            const target=b.resolveHref(section);
            if(!target||b.sections[target.index]?.id!==section)throw new ReaderError('invalid_location');
            // Reuse the engine's admitted source loader with the actual caller
            // signal; Section.createDocument only has the book-wide signal.
            const text=await loadText(section,task.signal);
            throwIfAborted(task.signal);unchanged();
            if(contents[index]!==entry||entry.section!==section||entry.fragment!==fragment)throw new ReaderError('invalid_source');
            if(text===null)throw new ReaderError('invalid_location');
            const doc=new DOMParser().parseFromString(text,'application/xhtml+xml');
            if(doc.querySelector('parsererror'))throw new ReaderError('invalid_book');
            // Manifest fragments are already decoded by the producer. The
            // pinned engine keeps href hashes encoded, so use the decoded DOM
            // identifier rather than encoding it into another URI here.
            const node=fragment?(doc.getElementById(fragment)??doc.querySelector(`[name="${CSS.escape(fragment)}"]`)):
              typeof target.anchor==='function'?target.anchor(doc):null;
            if(fragment&&!(node instanceof Node)&&!(node instanceof Range))throw new ReaderError('invalid_location');
            const range=doc.createRange();
            if(node instanceof Range)range.setStart(node.startContainer,node.startOffset);
            else if(node instanceof Node)range.selectNodeContents(node);
            else range.selectNodeContents(doc.body??doc.documentElement);
            range.collapse(true);
            const prefix=doc.createRange();prefix.selectNodeContents(doc.body??doc.documentElement);prefix.setEnd(range.startContainer,range.startOffset);
            const location:ReaderLocation={format:'epub',cfi:cfiAt(b,target.index,range),section,
              progression:progression(b,target.index,prefix.toString().length/Math.max(1,(doc.body??doc.documentElement).textContent?.length??1))};
            throwIfAborted(task.signal);unchanged();return Object.freeze(location);
          }finally{task.dispose();}
        },
        async mount(mount) {
          throwIfAborted(signal);throwIfAborted(mount.signal);unchanged();if(mounted)throw new ReaderError('invalid_book');mounted=true;
          view=await mountEpub(b,{...mount,onLocation(value){if(currentSource())mount.onLocation(value);},
            onSelection(value){if(currentSource())mount.onSelection(value);},onSurfaceInput(value){if(currentSource())mount.onSurfaceInput?.(value);}},options,tocBySection,signal,unchanged);return view;
        },
        async *search(query:string,search:ReaderSearchOptions):AsyncIterable<ReaderSearchResult> {
          if(!query.trim()||query.length>1024||search.limit<1||search.limit>500)throw new ReaderError('limit_exceeded');
          const task=abortScope([signal,search.signal]),searchSignal=task.signal;let count=0;
          try {
          if(indexedSearch&&spine&&spine.length>0){
            let cursor:string|undefined, first=true, emitted=0, fallback=false, indexRevision:number|undefined, pages=0;
            const seenCursors=new Set<string>(), seenHits=new Set<string>();
            const sectionCache=new Map<number,{doc:Document;raw:NonNullable<ReturnType<typeof indexedRawText>>}>();let cachedBytes=0;
            const deadline=Date.now()+indexedTimeLimit;
            while(emitted<search.limit){
              throwIfAborted(searchSignal);unchanged();
              if(cursor!==undefined&&Date.now()>deadline)throw new ReaderError('limit_exceeded');
              const requested=Math.min(indexedPageLimit,search.limit-emitted);
              const page=await indexedSearch(query,{signal:searchSignal,limit:requested,...(cursor===undefined?{}:{cursor})});
              throwIfAborted(searchSignal);unchanged();
              if(page.sourceId!==id||page.sourceRevision!==revision)throw new ReaderError('invalid_source');
              if(!Number.isSafeInteger(page.indexRevision)||page.indexRevision<0)throw new ReaderError('invalid_book');
              if(indexRevision===undefined)indexRevision=page.indexRevision;else if(page.indexRevision!==indexRevision)throw new ReaderError('invalid_source');
              if(!Array.isArray(page.hits)||page.hits.length>requested)throw new ReaderError('invalid_book');
              const status=page.coverage?.status;
              if(typeof status!=='string'||!['indexed','pending','unsupported','evicted','excluded'].includes(status))throw new ReaderError('invalid_book');
              if(status!=='indexed'){
                if(first&&emitted===0&&page.hits.length===0&&page.next===undefined){fallback=true;break;}
                throw new ReaderError('invalid_book');
              }
              const groups=new Map<number,{path:string;hits:ReaderIndexedSearchHit[]}>();
              for(const hit of page.hits){
                if(!Number.isSafeInteger(hit.sectionIndex)||hit.sectionIndex<0||hit.sectionIndex>=spine.length)throw new ReaderError('invalid_book');
                const path=spine[hit.sectionIndex]!;
                if(hit.sectionPath!==path||b.sections[hit.sectionIndex]?.id!==path)throw new ReaderError('invalid_book');
                if(!searchWellFormed(hit.excerpt)||hit.excerpt.length>8192||!Number.isSafeInteger(hit.utf16Offset)||hit.utf16Offset<0||hit.utf16Offset>indexedTextLimit
                  ||!Number.isSafeInteger(hit.matchStart)||hit.matchStart<0||!Number.isSafeInteger(hit.matchLength)||hit.matchLength<1
                  ||hit.matchStart+hit.matchLength>hit.excerpt.length)throw new ReaderError('invalid_book');
                const key=`${hit.sectionIndex}\u0000${hit.utf16Offset}`;
                if(seenHits.has(key))throw new ReaderError('invalid_book');
                seenHits.add(key);
                const group=groups.get(hit.sectionIndex)??{path,hits:[]};group.hits.push(hit);groups.set(hit.sectionIndex,group);
              }
              const cooperative=async()=>{await new Promise<void>(resolve=>setTimeout(resolve,0));throwIfAborted(searchSignal);};
              for(const group of groups.values()){
                throwIfAborted(searchSignal);unchanged();
                await cooperative();
                throwIfAborted(searchSignal);unchanged();
                const sectionIndex=group.hits[0]!.sectionIndex;
                // CPU work gets its own budget after asset IO; ordinary network
                // latency must not consume the section's mapping allowance.
                let sectionDeadline=Math.min(deadline,Date.now()+indexedSectionBudget);
                let section=sectionCache.get(sectionIndex);
                if(!section&&Date.now()<=deadline){
                  const textSource=await loadText(group.path,searchSignal);throwIfAborted(searchSignal);unchanged();
                  sectionDeadline=Math.min(deadline,Date.now()+indexedSectionBudget);
                  let doc:Document|null=null, raw:ReturnType<typeof indexedRawText>=null;
                  if(textSource&&Date.now()<=sectionDeadline){doc=new DOMParser().parseFromString(textSource,'application/xhtml+xml');if(doc.querySelector('parsererror'))doc=null;}
                  if(doc){try{raw=indexedRawText(doc,searchSignal,sectionDeadline);}catch(error){if(!(error instanceof ReaderError)||error.code==='aborted'||error.code==='closed')throw error;raw=null;}}
                  if(doc&&raw){section={doc,raw};
                    // Bounded per-query cache: at most 8 sections / 16 MiB of raw
                    // text. Other sections are still parsed once for their page.
                    if(sectionCache.size<8&&cachedBytes+raw.raw.length<=(16<<20)){sectionCache.set(sectionIndex,section);cachedBytes+=raw.raw.length;}
                  }
                }
                const doc=section?.doc??null, raw=section?.raw??null;
                let normalized:ReturnType<typeof indexedNormalize>=null;
                if(raw){try{normalized=indexedNormalize(raw.raw,[...new Set(group.hits.map(hit=>hit.utf16Offset))],[...new Set(group.hits.map(hit=>hit.utf16Offset+hit.matchLength))],searchSignal,sectionDeadline);}catch(error){if(!(error instanceof ReaderError)||error.code==='aborted'||error.code==='closed')throw error;normalized=null;}}
                for(const hit of group.hits){
                  throwIfAborted(searchSignal);unchanged();
                  const base={id:`indexed:${hit.sectionIndex}:${hit.utf16Offset}`,excerpt:hit.excerpt,match:Object.freeze({start:hit.matchStart,length:hit.matchLength})};
                  let result:ReaderSearchResult=Object.freeze(base);
                  if(doc&&raw&&normalized){
                    try{
                      const range=indexedRange(doc,raw,normalized,hit);
                      if(range){
                        const cfi=cfiAt(b,hit.sectionIndex,range);
                        const chapter=chapterAt(b,hit.sectionIndex,range,tocBySection);
                        const fraction=normalized.text.length?Math.max(0,Math.min(1,hit.utf16Offset/normalized.text.length)):0;
                        const bookProgression=progression(b,hit.sectionIndex,fraction);
                        result=Object.freeze({...base,...(chapter===undefined?{}:{chapter}),location:Object.freeze({format:'epub' as const,cfi,section:group.path,progression:bookProgression,...(chapter===undefined?{}:{chapter})})});
                      }
                    }catch(error){if(error instanceof ReaderError&&(error.code==='aborted'||error.code==='closed'))throw error;}
                  }
                  throwIfAborted(searchSignal);unchanged();
                  yield result;emitted++;
                  if(emitted>=search.limit)return;
                }
              }
              if(emitted>=search.limit)return;
              if(page.next===undefined)return;
              if(typeof page.next!=='string'||!/^[A-Za-z0-9_-]{1,512}$/.test(page.next)||seenCursors.has(page.next))throw new ReaderError('invalid_book');
              seenCursors.add(page.next);cursor=page.next;first=false;
              if(++pages>indexedOverallLimit)throw new ReaderError('limit_exceeded');
            }
            if(!fallback)return;
          }
          for(let index=0;index<b.sections.length;index++) {
            throwIfAborted(searchSignal);unchanged();
            // Search has its own read signal, so cancelling a query cancels its
            // asset request without closing the mounted publication.
            const textSource=await loadText(b.sections[index].id,searchSignal);throwIfAborted(searchSignal);
            if(!textSource)continue;
            const doc=new DOMParser().parseFromString(textSource,'application/xhtml+xml');
            if(doc.querySelector('parsererror'))throw new ReaderError('invalid_book');
            const walker=doc.createTreeWalker(doc.body??doc.documentElement,NodeFilter.SHOW_TEXT,{acceptNode(node){
              return node.parentElement?.closest('style,script,head')?NodeFilter.FILTER_REJECT:NodeFilter.FILTER_ACCEPT;
            }});
            const nodes:Text[]=[],offsets:number[]=[];let text='';
            for(let node=walker.nextNode();node;node=walker.nextNode()) {nodes.push(node as Text);offsets.push(text.length);text+=node.textContent??'';if(text.length>8*1024*1024)throw new ReaderError('limit_exceeded');}
            // Locale-neutral lowercase preserves UTF-16 offsets for common text;
            // search compares slices to avoid length-changing case mappings.
            const folded=text.toLowerCase(),foldedQuery=query.toLowerCase(),useFolded=folded.length===text.length&&foldedQuery.length===query.length;
            const haystack=useFolded?folded:text,needle=useFolded?foldedQuery:query;
            for(let at=haystack.indexOf(needle);at>=0;at=haystack.indexOf(needle,at+Math.max(1,needle.length))) {
              throwIfAborted(searchSignal);unchanged();
              // Whole-word/phrase filtering happens before the result cap: the
              // limit counts real matches, never a capped substring list. The
              // span is in original-text UTF-16 offsets, so case folding that
              // changes length is never mapped through a guessed offset.
              const matchEnd=at+needle.length;
              if(!isWholeWordPhrase(text,at,matchEnd))continue;
              let start=offsets.length-1,end=start;
              while(start>0&&offsets[start]>at)start--;end=start;
              while(end+1<offsets.length&&offsets[end+1]<matchEnd)end++;
              const range=doc.createRange();range.setStart(nodes[start],at-offsets[start]);range.setEnd(nodes[end],matchEnd-offsets[end]);
              const cfi=cfiAt(b,index,range),chapter=chapterAt(b,index,range,tocBySection);
              const excerptStart=Math.max(0,at-60);
              yield {id:cfi,excerpt:text.slice(excerptStart,Math.min(text.length,matchEnd+60)),match:{start:at-excerptStart,length:needle.length},
                location:{format:'epub',cfi,section:b.sections[index].id,progression:progression(b,index,text.length?at/text.length:0),chapter},chapter};
              if(++count>=search.limit)return;
            }
          }
          }finally{task.dispose();}
        },dispose:close};
      unchanged();return document;
    } catch(error){await close();throw error;}
  }};
}
async function mountEpub<P extends EpubDisplayPreferences>(book:Book,mount:ReaderMount<P>,options:EpubAdapterOptions,tocBySection:Map<number,{label:string;cfi:string}[]>,parent:AbortSignal,unchanged:()=>void):Promise<ReaderView<P>> {
  const abort=new AbortController(),scope=abortScope([parent,mount.signal,abort.signal]),signal=scope.signal;
  const renderer=document.createElement('foliate-paginator') as Paginator;
  renderer.setAttribute('portico-motion','');
  const surface=document.createElement('div');surface.style.cssText='position:relative;width:100%;height:100%;overflow:hidden;';
  renderer.style.cssText='display:block;position:absolute;left:0;right:0;width:100%;';
  const urls={literata:URL.createObjectURL(options.fonts.literata),manrope:URL.createObjectURL(options.fonts.manrope)};
  let location:ReaderLocation|null=null,relocated:Relocate|undefined,preferences=mount.preferences,viewport=mount.viewport,dead=false;
  let motionWatch: ReturnType<typeof watchReaderMotion> | undefined;
  let cleanupDoc:()=>void=()=>{};
  let clearSearchMark:()=>void=()=>{};
  function current(){throwIfAborted(signal);if(dead)throw new ReaderError('closed');unchanged();}
  function apply() {
    const display=epubDisplay(preferences,viewport,urls);surface.style.background=display.background;
    renderer.style.top=`${display.top}px`;renderer.style.height=`${Math.max(1,viewport.height-display.top-display.bottom)}px`;
    renderer.setAttribute('flow',display.flow);renderer.setAttribute('margin','0px');
    renderer.setAttribute('max-block-size',`${Math.max(1,viewport.height-display.top-display.bottom)}px`);
    renderer.setAttribute('gap',`${display.gap}%`);renderer.setAttribute('max-column-count',String(display.columns));
    renderer.setAttribute('max-inline-size',`${Math.max(100,display.maxInlineSize)}px`);
    renderer.setStyles(display.css);renderer.render();
  }
  const relocate=(event:Event)=>{
    if(dead||signal.aborted)return;const d=(event as CustomEvent<Relocate>).detail;
    if(!book.sections[d.index]||!Number.isFinite(d.fraction))return;
    relocated=d;
    location=Object.freeze({format:'epub',cfi:cfiAt(book,d.index,d.range),section:book.sections[d.index].id,
      progression:progression(book,d.index,d.fraction),chapter:chapterAt(book,d.index,d.range,tocBySection),
      ...((tocBySection.get(d.index)?.length??0)===1&&d.size&&Number.isFinite(d.size)&&d.size>0?{pagesLeftInSection:Math.max(0,Math.ceil((1-d.fraction)/d.size)-1)}:{})});
    mount.onLocation(location);
  };
  const load=(event:Event)=>{
    if(dead||signal.aborted)return;clearSearchMark();cleanupDoc();const {doc,index}=(event as CustomEvent<{doc:Document;index:number}>).detail;
    const iframe=doc.defaultView?.frameElement;
    if(mount.metadata?.title)iframe?.setAttribute('title',mount.metadata.title);
    doc.documentElement.lang ||= metadataLanguage(book.metadata?.language)??'';
    const listenerAbort=new AbortController(),opts={signal:listenerAbort.signal};cleanupDoc=()=>listenerAbort.abort();
    const selection=()=>{
      if(dead||signal.aborted)return;const selected=doc.defaultView?.getSelection();
      if(!selected||selected.isCollapsed||!selected.rangeCount){mount.onSelection(null);return;}
      const range=selected.getRangeAt(0);if(!range.toString().trim())return;
      const frame=doc.defaultView?.frameElement?.getBoundingClientRect(),host=mount.container.getBoundingClientRect();
      const rects=Array.from(range.getClientRects(),r=>({x:r.x+(frame?.x??0)-host.x,y:r.y+(frame?.y??0)-host.y,width:r.width,height:r.height}));
      mount.onSelection({text:range.toString().slice(0,65536),location:{format:'epub',cfi:cfiAt(book,index,range),section:book.sections[index].id,progression:location?.progression??0},rects});
    };
    doc.addEventListener('selectionchange',selection,opts);
    let down:{id:number;x:number;y:number;at:number}|null=null;
    doc.addEventListener('pointerdown',event=>{
      const target=event.target as Element|null;
      if(!event.isPrimary||target?.nodeType===1&&target.closest('a[href],button,input,textarea,select,[contenteditable="true"]')){down=null;return;}
      if(event.button===0)down={id:event.pointerId,x:event.clientX,y:event.clientY,at:performance.now()};
    },opts);
    doc.addEventListener('pointercancel',()=>{down=null;},opts);
    doc.addEventListener('pointerup',event=>{
      const start=down;down=null;
      if(!start||start.id!==event.pointerId||doc.defaultView?.getSelection()?.toString())return;
      const dx=event.clientX-start.x,dy=event.clientY-start.y,elapsed=performance.now()-start.at;
      // One physical gesture emits one intent. A preceding activity wake would
      // turn a centre tap's toggle into an unconditional hide.
      // Touch dragging is already handled and committed by this paginator. A
      // second host swipe intent would advance twice. Mouse/keyboard retain
      // their original intent path.
      if(preferences.layout==='pages'&&event.pointerType==='touch'&&Math.hypot(dx,dy)>10)return;
      if(preferences.layout==='pages'&&elapsed<=600&&Math.abs(dx)>=48&&Math.abs(dx)>Math.abs(dy)*1.5) {
        mount.onSurfaceInput?.({kind:'swipe',direction:dx<0?'left':'right'});return;
      }
      if(Math.hypot(dx,dy)>10||elapsed>400)return;
      mount.onSurfaceInput?.({kind:'tap',fraction:Math.max(0,Math.min(1,event.clientX/(doc.defaultView?.innerWidth??viewport.width)))});
    },opts);
    doc.addEventListener('pointermove',event=>{if(event.pointerType==='mouse')mount.onSurfaceInput?.({kind:'activity'});},opts);
    doc.addEventListener('keydown',event=>mount.onSurfaceInput?.({kind:'key',key:event.key,shift:event.shiftKey,alt:event.altKey,ctrl:event.ctrlKey,meta:event.metaKey,
      editable:(event.target as Element|null)?.nodeType===1&&!!(event.target as Element).closest('input,textarea,[contenteditable="true"]'),preventDefault:()=>event.preventDefault()}),opts);
    doc.addEventListener('click',event=>{
      const link=(event.target as Element|null)?.closest('a[href]');if(!link)return;event.preventDefault();
      const href=link.getAttribute('href');if(!href||/^[a-z][a-z\d+.-]*:|^\//i.test(href))return;
      const target=book.resolveHref(book.sections[index].resolveHref(href));
      if(target){clearSearchMark();void cancellable(renderer.goTo(target),signal).catch(()=>{});}
    },opts);
  };
  const turned=()=>{if(!dead&&!signal.aborted){try{unchanged();if(!dead&&!signal.aborted)mount.onSurfaceInput?.({kind:'turned'});}catch{/* Original publication retired. */}}};
  renderer.addEventListener('relocate',relocate);renderer.addEventListener('load',load);renderer.addEventListener('portico-turn',turned);
  const dispose=()=>{if(dead)return;dead=true;abort.abort();scope.dispose();motionWatch?.dispose();clearSearchMark();cleanupDoc();renderer.removeEventListener('relocate',relocate);renderer.removeEventListener('load',load);renderer.removeEventListener('portico-turn',turned);
    try{renderer.destroy();}finally{surface.remove();URL.revokeObjectURL(urls.literata);URL.revokeObjectURL(urls.manrope);}};
  signal.addEventListener('abort',dispose,{once:true});
  let system: MediaQueryList | undefined;try{system=renderer.ownerDocument.defaultView?.matchMedia('(prefers-reduced-motion: reduce)');}catch{/* Unknown system policy reduces. */}
  motionWatch=watchReaderMotion(mount.motion,system,value=>{if(!dead&&!signal.aborted)renderer.setReducedMotion(value);});
  if(dead||signal.aborted){motionWatch.dispose();throw new ReaderError('closed');}
  renderer.setReducedMotion(motionWatch.reduced());
  const view:ReaderView<P>={
    async navigate(target:ReaderNavigation,callerSignal) {
      current();const task=abortScope([signal,callerSignal]),active=task.signal;
      try {
      throwIfAborted(active);clearSearchMark();throwIfAborted(active);
      if(target.kind==='next')await cancellable(renderer.next(undefined,active),active);
      else if(target.kind==='previous')await cancellable(renderer.prev(undefined,active),active);
      else if(target.kind==='location') {
        if(target.location.format!=='epub')throw new ReaderError('invalid_location');
        const resolved=book.resolveCFI(target.location.cfi);if(!resolved||!book.sections[resolved.index])throw new ReaderError('invalid_location');
        await cancellable(renderer.goTo(resolved),active);
      } else if(target.kind==='progression') {
        if(!Number.isFinite(target.value)||target.value<0||target.value>1)throw new ReaderError('invalid_location');
        const sizes=book.sections.map(s=>s.linear==='no'?0:s.size),total=sizes.reduce((a,b)=>a+b,0);let remaining=total*target.value,index=0;
        while(index<sizes.length-1&&remaining>=sizes[index])remaining-=sizes[index++];
        await cancellable(renderer.goTo({index,anchor:sizes[index]?remaining/sizes[index]:0}),active);
      }throwIfAborted(active);current();
      }finally{task.dispose();}
    },
    async applyPreferences(next,callerSignal){current();throwIfAborted(callerSignal);preferences=next;apply();await new Promise<void>(r=>requestAnimationFrame(()=>r()));current();},
    async resize(next,callerSignal){current();throwIfAborted(callerSignal);viewport=next;apply();await new Promise<void>(r=>requestAnimationFrame(()=>r()));current();},
    async showSearchResult(target,callerSignal) {
      current();throwIfAborted(callerSignal);clearSearchMark();
      if(!target)return;
      if(target.format!=='epub')throw new ReaderError('invalid_location');
      const resolved=book.resolveCFI(target.cfi);
      if(!resolved||typeof resolved.anchor!=='function')throw new ReaderError('invalid_location');
      const content=renderer.getContents().find(content=>content.index===resolved.index);
      if(!content)throw new ReaderError('invalid_location');
      const range=resolved.anchor(content.doc);
      if(!range||typeof range!=='object'||!('startContainer' in range)||!('endContainer' in range)||!range.toString())throw new ReaderError('invalid_location');
      const match=range as Range;
      if(match.startContainer.ownerDocument!==content.doc||match.endContainer.ownerDocument!==content.doc)throw new ReaderError('invalid_location');
      // The range comes from the same checked book CFI in its mounted document.
      // CSS Highlights do not create a DOM selection or annotation record.
      const support=content.doc.defaultView as unknown as {CSS?:{highlights?:{set(name:string,value:unknown):void;delete(name:string):boolean}};Highlight?:new(...ranges:Range[])=>unknown}|null;
      if(!support?.CSS?.highlights||!support.Highlight)return;
      const registry=support.CSS.highlights,name='portico-reader-search';
      const style=content.doc.createElement('style');style.textContent='::highlight(portico-reader-search){background:rgba(64,159,212,.48);color:inherit;}';
      (content.doc.head??content.doc.documentElement).append(style);
      try{registry.set(name,new support.Highlight(match));}catch(error){style.remove();throw error;}
      const clear=()=>{registry.delete(name);style.remove();callerSignal.removeEventListener('abort',clear);clearSearchMark=()=>{};};
      clearSearchMark=clear;callerSignal.addEventListener('abort',clear,{once:true});
      if(callerSignal.aborted){clear();throwIfAborted(callerSignal);}
      current();
    },
    getLocation(){
      if(!location||!relocated)return location;
      // Outline completion can enrich the current measured location without
      // inventing a turn or retaining a range from a different page.
      return Object.freeze({...location,chapter:chapterAt(book,relocated.index,relocated.range,tocBySection),
        ...((tocBySection.get(relocated.index)?.length??0)===1&&relocated.size&&Number.isFinite(relocated.size)&&relocated.size>0?
          {pagesLeftInSection:Math.max(0,Math.ceil((1-relocated.fraction)/relocated.size)-1)}:{})});
    },dispose
  };
  try {
    current();renderer.open(book);surface.append(renderer);mount.container.append(surface);apply();
    const body=book.landmarks?.find(mark=>mark.type.includes('bodymatter')||mark.type.includes('text'));
    const target=body?book.resolveHref(body.href):null;
    const index=Math.max(0,book.sections.findIndex(s=>s.linear!=='no'));
    await cancellable(renderer.goTo(target??{index}),signal);current();return view;
  }
  catch(error){dispose();throw error;}
}
