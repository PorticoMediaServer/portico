import {throwSignalAborted as throwIfAborted} from '../abort.ts';
import {abortScope} from '../abort.ts';
import type {ReaderAdapter,ReaderDocument,ReaderLocation} from '../ports.ts';
import {mountFixed,type FixedPreferences} from './fixed-view.ts';
import {openPdf,pdfText,pdfTextStream,PdfInputError,type PdfEngine,type PdfOptions} from './engine.ts';
import {renderPdfStructure} from './struct-tree.ts';

// Necessary text-layer geometry, adapted from Mozilla PDF.js6.4.299,
// Copyright Mozilla Foundation, Apache-2.0. No viewer chrome is included.
const textCss=`[data-reader-text]{color-scheme:only light;position:absolute;inset:0;overflow:clip;line-height:1;letter-spacing:normal;word-spacing:normal;text-size-adjust:none;forced-color-adjust:none;transform-origin:0 0;pointer-events:none;--min-font-size:1;--text-scale-factor:calc(var(--total-scale-factor)*var(--min-font-size));--min-font-size-inv:calc(1/var(--min-font-size))}
[data-reader-text] :is(span,br){color:transparent;position:absolute;white-space:pre;cursor:text;transform-origin:0 0;pointer-events:auto;user-select:text;-webkit-user-select:text}
[data-reader-text]>:not(.markedContent),[data-reader-text] .markedContent span:not(.markedContent){z-index:1;--font-height:0;font-size:calc(var(--text-scale-factor)*var(--font-height));--scale-x:1;--rotate:0deg;transform:rotate(var(--rotate)) scaleX(var(--scale-x)) scale(var(--min-font-size-inv))}
[data-reader-text] .markedContent{display:contents}[data-reader-text] span[role=img]{user-select:none;cursor:default}[data-reader-text] ::selection{background:var(--reader-selection,Highlight);color:transparent}
[data-reader-structure]{position:absolute;inset:0;pointer-events:none;contain:strict}`;
const position=(page:number,count:number):ReaderLocation=>({format:'pdf',page,progression:count===1?0:page/(count-1)});
type ReaderTocEntry=ReaderDocument<object>['toc'][number];
async function outline(engine:PdfEngine,signal:AbortSignal):Promise<readonly ReaderTocEntry[]>{
  const raw=await engine.run(engine.document.getOutline(),signal);if(!raw)return [];
  let count=0;
  const visit=async(items:typeof raw,depth:number):Promise<ReaderTocEntry[]>=>{
    if(depth>16)throw new PdfInputError('pdf_limit');const out:ReaderTocEntry[]=[];
    for(const item of items){
      throwIfAborted(signal);const entryNumber=++count;if(count>2048)throw new PdfInputError('pdf_limit');
      // External actions/links are never followed or mounted.
      let dest=item.dest;if(typeof dest==='string')dest=await engine.run(engine.document.getDestination(dest),signal);
      const first=Array.isArray(dest)?dest[0]:undefined;let index:number|undefined;
      if(typeof first==='number'&&Number.isInteger(first))index=first;
      else if(first&&typeof first==='object'&&Number.isInteger(first.num)&&Number.isInteger(first.gen))index=await engine.run(engine.document.getPageIndex(first),signal);
      const children=item.items?.length?await visit(item.items,depth+1):[];
      if(index!==undefined&&index>=0&&index<engine.document.numPages&&item.title&&item.title.length<=2048)out.push({id:`pdf-outline-${entryNumber}`,label:item.title,location:position(index,engine.document.numPages),...children.length?{children}:{}});
      else out.push(...children);
    }return out;
  };return visit(raw,0);
}
async function preview(engine:PdfEngine,index:number,width:number,signal:AbortSignal){
  if(!Number.isInteger(width)||width<32||width>2048)throw new PdfInputError('page_invalid');
  const page=await engine.page(index,signal),original=page.getViewport({scale:1}),scale=Math.min(1,width/original.width),viewport=page.getViewport({scale});
  if(viewport.width*viewport.height>16_000_000)throw new PdfInputError('pdf_limit');
  if(typeof document==='undefined')throw new Error('preview_canvas_required');
  const canvas=document.createElement('canvas');canvas.width=Math.ceil(viewport.width);canvas.height=Math.ceil(viewport.height);
  const render=page.render({canvas,viewport,annotationMode:engine.library.AnnotationMode.DISABLE});const abort=()=>render.cancel();signal.addEventListener('abort',abort,{once:true});
  try{
    await engine.run(render.promise,signal);
    const blob=await new Promise<Blob>((resolve,reject)=>canvas.toBlob(value=>value?resolve(value):reject(new PdfInputError('pdf_invalid')),'image/png'));
    throwIfAborted(signal);if(blob.size>16*1024*1024)throw new PdfInputError('pdf_limit');return {mime:'image/png' as const,bytes:new Uint8Array(await blob.arrayBuffer()),width:canvas.width,height:canvas.height};
  }finally{signal.removeEventListener('abort',abort);render.cancel();canvas.width=0;canvas.height=0;page.cleanup();}
}
export function createPdfAdapter<P extends FixedPreferences>(options:PdfOptions={}):ReaderAdapter<P>{
  return {format:'pdf',async open({source,signal}):Promise<ReaderDocument<P>>{
    const engine=await openPdf(source,signal,options);let closed=false;const views=new Set<Awaited<ReturnType<ReaderDocument<P>['mount']>>>();
    try{
      const info=await engine.run(engine.document.getMetadata(),signal),toc=await outline(engine,signal);
      const labels=await engine.run(engine.document.getPageLabels(),signal);
      const viewer=await engine.run(engine.document.getViewerPreferences(),signal),order=viewer?.get('Direction');
      const direction=order==='R2L'?'rtl' as const:order==='L2R'?'ltr' as const:undefined;
      const fields=info.info as Record<string,unknown>;
      const title=typeof fields.Title==='string'?fields.Title.slice(0,2048):undefined,author=typeof fields.Author==='string'?fields.Author.slice(0,2048):undefined;
      const language=typeof fields.Language==='string'?fields.Language.slice(0,256):undefined;
      const publication:ReaderDocument<P>={id:source.id,revision:source.revision,format:'pdf',metadata:{...title?{title}:{},...author?{author}:{},...language?{language}:{},...direction?{direction}:{},pageCount:engine.document.numPages},toc,
        async mount(mountOptions){
          if(closed)throw new PdfInputError('closed');
          const owner=mountOptions.container.ownerDocument,style=owner.createElement('style');style.textContent=textCss;mountOptions.container.append(style);
          const life=new AbortController(),scope=abortScope([mountOptions.signal,engine.signal,life.signal]),combined=scope.signal;
          let fixed:Awaited<ReturnType<ReaderDocument<P>['mount']>>;
          try{fixed=await mountFixed('pdf',engine.document.numPages,direction??'ltr',async(index,{signal:pageSignal})=>{
            const page=await engine.page(index,pageSignal),original=page.getViewport({scale:1});
            if(original.width<=0||original.height<=0||original.width>32768||original.height>32768)throw new PdfInputError('pdf_limit');
            return {width:original.width,height:original.height,
              async mount(target,scale,renderSignal){
                throwIfAborted(renderSignal);const viewport=page.getViewport({scale}),canvas=owner.createElement('canvas');
                const dpr=Math.min(owner.defaultView?.devicePixelRatio??1,2,8192/viewport.width,8192/viewport.height,Math.sqrt(16_000_000/(viewport.width*viewport.height)));
                canvas.width=Math.max(1,Math.ceil(viewport.width*dpr));canvas.height=Math.max(1,Math.ceil(viewport.height*dpr));Object.assign(canvas.style,{display:'block',width:'100%',height:'100%'});canvas.setAttribute('aria-hidden','true');target.append(canvas);
                const layer=owner.createElement('div');layer.dataset.readerText=String(index);layer.style.setProperty('--total-scale-factor',String(viewport.scale));target.append(layer);
                const text=new engine.library.TextLayer({textContentSource:pdfTextStream(page,renderSignal),container:layer,viewport});
                const render=page.render({canvas,viewport,...dpr!==1?{transform:[dpr,0,0,dpr,0,0]}:{},annotationMode:engine.library.AnnotationMode.DISABLE});
                const abort=()=>{render.cancel();text.cancel();};renderSignal.addEventListener('abort',abort,{once:true});
                let structure:HTMLElement|null=null;
                try{const [,,tree]=await engine.run(Promise.all([render.promise,text.render(),page.getStructTree()]),renderSignal);
                  throwIfAborted(renderSignal);structure=renderPdfStructure(tree,layer);if(structure)target.insertBefore(structure,layer);
                  return ()=>{renderSignal.removeEventListener('abort',abort);abort();canvas.width=0;canvas.height=0;canvas.remove();layer.remove();structure?.remove();};}
                catch(error){renderSignal.removeEventListener('abort',abort);abort();canvas.width=0;canvas.height=0;canvas.remove();layer.remove();structure?.remove();throw error;}
              },dispose(){page.cleanup();}
            };
          },{...mountOptions,signal:combined,onLocation(location){mountOptions.onLocation({...location,...location.format==='pdf'&&labels?.[location.page]?{pageLabel:labels[location.page]}:{}});}});}catch(error){life.abort();scope.dispose();style.remove();throw error;}
          const onSelection=()=>{
            const selection=owner.getSelection();if(!selection||selection.isCollapsed||!selection.rangeCount){mountOptions.onSelection(null);return;}
            const element=selection.anchorNode?.parentElement?.closest<HTMLElement>('[data-reader-text]');
            if(!element||!mountOptions.container.contains(element)||!element.contains(selection.focusNode)){mountOptions.onSelection(null);return;}
            const page=Number(element.dataset.readerText),text=selection.toString();if(!text||text.length>65536){mountOptions.onSelection(null);return;}
            const range=selection.getRangeAt(0),raw=range.getClientRects();
            // A quote is anchored only when its entire range has supported geometry.
            // Never persist a sampled prefix as the anchor for the complete quote.
            const supported=selection.rangeCount===1&&element.contains(range.startContainer)&&element.contains(range.endContainer)&&raw.length>0&&raw.length<=128;
            const rects=supported?Array.from(raw,r=>({x:r.x,y:r.y,width:r.width,height:r.height})):[];
            const bounds=element.getBoundingClientRect();
            const pageRects=Number.isFinite(bounds.x)&&Number.isFinite(bounds.y)&&Number.isFinite(bounds.width)&&Number.isFinite(bounds.height)&&bounds.width>0&&bounds.height>0?rects.map(r=>{
              if(![r.x,r.y,r.width,r.height].every(Number.isFinite)||r.width<=0||r.height<=0)return undefined;
              const x=Math.max(0,Math.min(1,(r.x-bounds.x)/bounds.width)),y=Math.max(0,Math.min(1,(r.y-bounds.y)/bounds.height));
              const right=Math.max(0,Math.min(1,(r.x+r.width-bounds.x)/bounds.width)),bottom=Math.max(0,Math.min(1,(r.y+r.height-bounds.y)/bounds.height));
              const width=right-x,height=bottom-y;
              return width>0&&height>0&&x+width<=1&&y+height<=1?{x,y,width,height}:undefined;
            }):[];
            const complete=pageRects.length>0&&pageRects.every((rect):rect is NonNullable<typeof rect>=>rect!==undefined);
            mountOptions.onSelection?.({text,location:position(page,engine.document.numPages),rects,...complete?{pageRects}: {}});
          };owner.addEventListener('selectionchange',onSelection,{signal:combined});
          const view={...fixed,dispose(){life.abort();scope.dispose();style.remove();views.delete(view);return fixed.dispose();}};views.add(view);return view;
        },
        async *search(query,searchOptions){
          if(closed)throw new PdfInputError('closed');const term=query.trim(),foldedTerm=term.toLowerCase();if(!term||term.length>256)throw new PdfInputError('pdf_limit');
          if(searchOptions.limit!==undefined&&(!Number.isInteger(searchOptions.limit)||searchOptions.limit<1||searchOptions.limit>1000))throw new PdfInputError('pdf_limit');
          const limit=searchOptions.limit??100;let found=0;
          for(let index=0;index<engine.document.numPages&&found<limit;index++){
            const page=await engine.page(index,searchOptions.signal);
            try{const text=await pdfText(engine,page,searchOptions.signal),folded=text.toLowerCase(),canFold=folded.length===text.length&&foldedTerm.length===term.length;
              const haystack=canFold?folded:text,needle=canFold?foldedTerm:term;let from=0,offset:number;
              while(found<limit&&(offset=haystack.indexOf(needle,from))>=0){throwIfAborted(searchOptions.signal);const excerptStart=Math.max(0,offset-100);yield {id:`pdf-${index}-${offset}`,excerpt:text.slice(excerptStart,Math.min(text.length,offset+needle.length+100)),match:{start:offset-excerptStart,length:needle.length},location:position(index,engine.document.numPages)};found++;from=offset+Math.max(1,needle.length);}
            }finally{page.cleanup();}
          }
        },page(index,pageOptions){return preview(engine,index,pageOptions.width,pageOptions.signal);},
        async dispose(){if(closed)return;closed=true;for(const view of [...views])await view.dispose();views.clear();await engine.dispose();}
      };return publication;
    }catch(error){await engine.dispose();throw error;}
  }};
}
