import {throwSignalAborted as throwIfAborted} from '../abort.ts';
import {abortScope,abortReason} from '../abort.ts';
import type {ReaderDocument,ReaderFixedViewport,ReaderLocation,ReaderView} from '../ports.ts';
import {planFixedLayout, fixedVisualPanes, validReaderCrease} from '../layout.ts';
import {pageGesture,settleFrames} from '../epub/page-motion.ts';

/** Adapter-private projection of the canonical Reading facade, never a store. */
export type FixedPreferences={twoPages:boolean;firstPageAlone:boolean;comicFit:'auto'|'width'|'height';comicDirection:'file'|'ltr'|'rtl'|'vertical';pdfView:'automatic'|'spread'|'width'|'page'};
export type PageSurface=Readonly<{width:number;height:number;mount(target:HTMLElement,scale:number,signal:AbortSignal):Promise<()=>void>;dispose():void}>;
export type FixedPageLoader=(page:number,options:{width:number;signal:AbortSignal})=>Promise<PageSurface>;
type Zoom={kind:'scale';value:number}|{kind:'fit';value:'page'|'width'|'height'|'spread'};

export type FixedPageViewportInput=Readonly<{
  page:number;pageWidth:number;pageHeight:number;scale:number;fittedScale:number;
  /** Measured scroll offset and visible box of the page's own scroll host. */
  scroll:Readonly<{left:number;top:number;width:number;height:number}>;
  /** Reported window the host fills, used for the box when it is not measured. */
  viewport:Readonly<{width:number;height:number}>;
}>;
const fraction=(value:number)=>Math.min(1,Math.max(0,value));
/** What a zoomed fixed page actually shows, in 0..1 page fractions. A page at or
 * below its fitted scale has nothing to report, so the result is absent at fit,
 * before layout, and for any input that cannot describe a real page. */
export function fixedPageViewport(input:FixedPageViewportInput):ReaderFixedViewport|undefined{
  const {page,pageWidth,pageHeight,scale,fittedScale,scroll,viewport}=input;
  const boxWidth=scroll.width>0?scroll.width:viewport.width,boxHeight=scroll.height>0?scroll.height:viewport.height;
  if(!Number.isSafeInteger(page)||page<0||!Number.isFinite(pageWidth)||pageWidth<=0||!Number.isFinite(pageHeight)||pageHeight<=0
    ||!Number.isFinite(scale)||scale<=0||!Number.isFinite(fittedScale)||fittedScale<=0
    ||!Number.isFinite(scroll.left)||!Number.isFinite(scroll.top)||!Number.isFinite(boxWidth)||boxWidth<=0||!Number.isFinite(boxHeight)||boxHeight<=0)return;
  if(scale-fittedScale<=1e-6)return;
  const width=pageWidth*scale,height=pageHeight*scale;
  // A page smaller than its box is centred by the flex host; a larger one starts at zero.
  const offsetX=Math.max(0,(boxWidth-width)/2),offsetY=Math.max(0,(boxHeight-height)/2);
  const x=fraction((scroll.left-offsetX)/width),y=fraction((scroll.top-offsetY)/height);
  const right=fraction((scroll.left+boxWidth-offsetX)/width),bottom=fraction((scroll.top+boxHeight-offsetY)/height);
  return Object.freeze({page,pageWidth,pageHeight,x,y,width:Math.max(0,right-x),height:Math.max(0,bottom-y),scale});
}

/** Both fixed formats use the common spread planner and their own real page renderer. */
export async function mountFixed<P extends FixedPreferences>(format:'pdf'|'cbz'|'cbr',pageCount:number,fileDirection:'ltr'|'rtl'|'vertical',load:FixedPageLoader,options:Parameters<ReaderDocument<P>['mount']>[0]):Promise<ReaderView<P>>{
  const document=options.container.ownerDocument,host=document.createElement('div');
  const paneStyle=document.createElement('style');paneStyle.textContent='[data-reader-fixed] [data-reader-page]{scrollbar-width:none}[data-reader-fixed] [data-reader-page]::-webkit-scrollbar{display:none}';document.head.append(paneStyle);
  host.tabIndex=0;host.setAttribute('role','document');
  host.dataset.readerFixed=format;Object.assign(host.style,{position:'relative',display:'flex',width:'100%',height:'100%',overflow:'auto',overscrollBehavior:'contain',touchAction:'none'});
  let preferences=options.preferences,viewport=options.viewport,page=0,closed=false,version=0,mutation=0,rendering=0,scrolling=false;
  let zoom:Zoom|undefined,scale=1,fittedScale=1,verticalWidth:number|undefined,location:ReaderLocation|null=null,cleanup:(()=>void)[]=[],pending:AbortController|undefined;
  let foldPanes:Map<number,{clip:HTMLDivElement;scale:number}>|undefined,dragPane:HTMLDivElement|undefined,gesturePage:number|undefined;
  let pageGeometry:{page:number;width:number;height:number;scale:number;fittedScale:number}|undefined,reportedViewport:ReaderFixedViewport|undefined,viewportFrame:number|undefined;
  const scrollState=(at=gesturePage??page)=>{const pane=foldPanes?.get(at);return pane?{left:pane.clip.scrollLeft,top:pane.clip.scrollTop,width:pane.clip.clientWidth,height:pane.clip.clientHeight}:{left:host.scrollLeft,top:host.scrollTop,width:viewport.width,height:viewport.height};};
  const scrollTo=(left:number,top:number,at=gesturePage??page)=>{const pane=foldPanes?.get(at);if(pane){pane.clip.scrollLeft=Math.max(0,left);pane.clip.scrollTop=Math.max(0,top);}else{host.scrollLeft=Math.max(0,left);host.scrollTop=Math.max(0,top);}};
  // The single-page host is the scroll boundary here; it uses the same positive
  // scroll offsets the pan/scroll code above already clamps to.
  const currentViewport=(at=page)=>{const geometry=pageGeometry;
    return geometry&&geometry.page===at?fixedPageViewport({page:at,pageWidth:geometry.width,pageHeight:geometry.height,scale:geometry.scale,
      fittedScale:geometry.fittedScale,scroll:scrollState(at),viewport}):undefined;};
  const sameViewport=(left:ReaderFixedViewport|undefined,right:ReaderFixedViewport|undefined)=>left===right||!!left&&!!right
    &&left.page===right.page&&left.pageWidth===right.pageWidth&&left.pageHeight===right.pageHeight&&left.scale===right.scale
    &&left.x===right.x&&left.y===right.y&&left.width===right.width&&left.height===right.height;
  const locate=(next=currentViewport())=>{reportedViewport=next;
    return location={format,page,progression:pageCount===1?0:page/(pageCount-1),...(next?{viewport:next}:{})};};
  // Pan geometry changes at pointer/scroll rate; one publish per frame at most.
  const scheduleViewport=()=>{if(viewportFrame!==undefined||closed)return;
    viewportFrame=requestAnimationFrame(()=>{viewportFrame=undefined;publishViewport();});};
  const publishViewport=()=>{if(closed)return;const next=currentViewport();
    if(!sameViewport(reportedViewport,next))options.onLocation(locate(next));};
  const surfacesByPage=new Map<number,PageSurface>();
  const lifetime=new AbortController(),scope=abortScope([lifetime.signal,options.signal]),signal=scope.signal;
  const direction=():'ltr'|'rtl'|'vertical'=>{const value:FixedPreferences['comicDirection']=preferences.comicDirection;return format!=='pdf'&&value!=='file'?value:fileDirection;};
  const fit=():'auto'|'page'|'width'|'height'|'spread'=>{
    if(zoom?.kind==='fit')return zoom.value;
    if(format==='pdf'){const value:FixedPreferences['pdfView']=preferences.pdfView;return value==='automatic'?'auto':value;}
    return preferences.comicFit;
  };
  const plan=(at=page)=>{
    const layout=planFixedLayout({format,viewport,page:at,pageCount,twoPages:preferences.twoPages,firstPageAlone:preferences.firstPageAlone,direction:direction(),fit:fit()});
    if(layout.direction==='vertical'&&fit()==='auto')return {...layout,fit:'width' as const};
    return format==='pdf'&&preferences.pdfView==='automatic'&&zoom?.kind!=='fit'?{...layout,fit:layout.spread?'spread' as const:'page' as const}:layout;
  };
  const clean=()=>{for(const fn of cleanup.splice(0))fn();};
  // motion.html 5.1: a tap or key turn slides the outgoing and incoming spreads together (slow 320,
  // out); reduced motion cross-fades them in 160 ms. The outgoing surfaces are released only when the
  // slide ends, and a new turn first finishes the running one. Zoom, resize and continuous scrolling
  // keep the immediate swap. A released swipe instead continues from the finger: both spreads settle
  // on the settle spring at the finger's speed (reduced motion: the same 160 ms cross-fade).
  let turning:{finish:()=>void;defer:(fn:()=>void)=>void}|undefined;
  // Lite (the host's data-motion-tier, a performance tier) takes the reduced path for now, behind its
  // own test so it can diverge. The finger is tracked in every tier.
  const liteMotion=()=>document.documentElement.dataset.motionTier==='lite';
  const reducedMotion=()=>{try{return matchMedia('(prefers-reduced-motion: reduce)').matches||document.documentElement.dataset.reduceMotion==='true'||liteMotion();}catch{return true;}};
  // Moving spreads sit outside the host; it clips them instead of growing scrollbars mid-motion.
  const moving=(on:boolean)=>{if(on)host.style.overflow='hidden';else if(!gesture&&!turning&&!settling)host.style.overflow='auto';};
  const swap=(next:HTMLDivElement,releases:(()=>void)[],turn:number)=>{
    turning?.finish();settled();
    const previous=turn?host.firstElementChild as HTMLElement|null:null;
    const swiped=gesture?.released&&gesture.element===previous?gesture:undefined;forgetGesture();
    if(!previous||typeof next.animate!=='function'||typeof previous.animate!=='function'){clean();cleanup=releases;host.replaceChildren(next);moving(false);return;}
    const outgoing=cleanup.splice(0);cleanup=releases;moving(true);
    previous.style.transform='';
    const from=previous.getBoundingClientRect(),box=host.getBoundingClientRect();
    Object.assign(previous.style,{position:'absolute',left:`${from.left-box.left+host.scrollLeft}px`,top:`${from.top-box.top+host.scrollTop}px`,margin:'0',pointerEvents:'none'});
    host.append(next);
    const reduce=reducedMotion(),width=host.clientWidth||box.width,offset=swiped?.offset??0;
    let leaving:Animation|undefined,arriving:Animation|undefined,done=false,timer:ReturnType<typeof setTimeout>|undefined,armed:number|undefined;
    // W14: the incoming spread sits at its first keyframe and is painted (two frames: raster, then upload of its
    // canvases) before the outgoing one starts to leave, so the slide never shows a blank between them. A swipe
    // already shows the neighbour beside the finger, so it starts at once; a hidden page falls back to the timer.
    const start=()=>{
      if(done||arriving)return;
      if(armed!==undefined)cancelAnimationFrame(armed);clearTimeout(timer);if(!swiped||reduce){next.style.transform='';next.style.opacity='';}
      if(swiped&&!reduce){
        const {values,duration}=settleFrames(offset,-turn*width,swiped.velocity),timing={duration,easing:'linear'};
        leaving=previous.animate(values.map(x=>({transform:`translateX(${x}px)`})),timing);
        arriving=next.animate(values.map(x=>({transform:`translateX(${x+turn*width}px)`})),timing);
      }else{
        const timing={duration:reduce?160:320,easing:reduce?'linear':'cubic-bezier(.2,.8,.2,1)'},held=`translateX(${offset}px)`;
        leaving=previous.animate(reduce?[{transform:held,opacity:1},{transform:held,opacity:0}]:[{transform:held},{transform:`translateX(${-turn*width}px)`}],timing);
        arriving=next.animate(reduce?[{opacity:0},{opacity:1}]:[{transform:`translateX(${offset+turn*width}px)`},{transform:'none'}],timing);
      }
      arriving.onfinish=handle.finish;
    };
    const handle={finish:()=>{if(done)return;done=true;if(armed!==undefined)cancelAnimationFrame(armed);clearTimeout(timer);leaving?.cancel();arriving?.cancel();next.style.transform='';next.style.opacity='';previous.remove();for(const fn of outgoing)fn();if(turning===handle)turning=undefined;moving(false);},defer:(fn:()=>void)=>{if(done)fn();else outgoing.push(fn);}};
    if(swiped&&!reduce)start();
    else{
      if(reduce)next.style.opacity='0';else next.style.transform=`translateX(${offset+turn*width}px)`;
      armed=requestAnimationFrame(()=>{armed=requestAnimationFrame(()=>{armed=undefined;start();});});timer=setTimeout(start,120);
    }
    turning=handle;
  };
  const turnFrom=(at:number,vertical:boolean)=>vertical||at===page||!host.firstElementChild?0:Math.sign(at-page)*(direction()==='rtl'?-1:1);
  type Composed={next:HTMLDivElement;releases:(()=>void)[];created:Map<number,PageSurface>;visualPages:readonly number[];vertical:boolean;
    fold?:{panes:Map<number,{clip:HTMLDivElement;scale:number}>;applied:number;fitted:number};
    flat?:{requested:number;fitted:number;width:number;geometry:typeof pageGeometry}};
  const discard=(built:Composed)=>{for(const release of built.releases.reverse())release();for(const surface of built.created.values())surface.dispose();built.next.remove();};
  /** Builds one spread, unattached. Its surfaces and mounts are released here on failure. */
  async function compose(at:number,operation:AbortSignal):Promise<Composed>{
    const layout=plan(at),next=document.createElement('div'),releases:(()=>void)[]=[],created=new Map<number,PageSurface>();
    // Continuous comics retain only actual adjacent pages. There are no blank
    // estimated-height pages, fabricated content, or whole-archive downloads.
    const vertical=layout.direction==='vertical',visualPages=vertical?Array.from({length:Math.min(pageCount-1,at+2)-Math.max(0,at-1)+1},(_,i)=>Math.max(0,at-1)+i):layout.visualPages;
    try{
      const surfaces:PageSurface[]=[];
      for(const index of visualPages){
        let surface=surfacesByPage.get(index);
        if(!surface){surface=await load(index,{width:Math.max(32,Math.min(2048,Math.ceil(viewport.width/layout.visualPages.length))),signal:operation});if(operation.aborted){surface.dispose();throw abortReason(operation);}created.set(index,surface);}
        throwIfAborted(operation);surfaces.push(surface);
      }
      // A continuous column has one stable page width despite different scan
      // resolutions. Each page retains its actual aspect ratio.
      const fold=!vertical?validReaderCrease(viewport):undefined;
      if(fold){
        // foldable6: physical panes around the reported crease. A cover or a last
        // page sits on its own recto/verso side; each page fits its own pane so
        // unequal aspects never cross the hinge.
        const lone=visualPages.length===1?(preferences.firstPageAlone&&visualPages[0]===0?'cover' as const:visualPages[0]===pageCount-1?'last' as const:'only' as const):undefined;
        const panes=fixedVisualPanes(viewport,visualPages,direction()==='rtl'?'rtl':'ltr',lone);
        Object.assign(next.style,{position:'relative',display:'block',flex:'none',width:`${viewport.width}px`,height:`${viewport.height}px`,minWidth:'0',margin:'0'});
        const panesById=new Map<number,{clip:HTMLDivElement;scale:number}>();
        let applied=0,fittedCurrent=0;
        for(const {page:pageNumber,pane} of panes){
          const index=visualPages.indexOf(pageNumber),surface=surfaces[index];
          const fitAll=Math.min(pane.width/surface.width,pane.height/surface.height);
          const fitted=Math.min(fitAll,layout.fit==='width'?pane.width/surface.width:layout.fit==='height'?pane.height/surface.height:fitAll);
          const renderScale=zoom?.kind==='scale'?zoom.value:fitted;
          if(!Number.isFinite(renderScale)||renderScale<=0)throw new Error('page_invalid');
          const w=surface.width*renderScale,h=surface.height*renderScale,clip=document.createElement('div'),content=document.createElement('div'),target=document.createElement('div');
          clip.dataset.readerPage=String(pageNumber);
          // The pane clips at the hinge, and scrolls the page inside itself so a
          // zoomed page can be panned to every edge without crossing the crease.
          Object.assign(clip.style,{position:'absolute',left:`${pane.x}px`,top:'0px',width:`${pane.width}px`,height:`${pane.height}px`,overflow:'auto',scrollbarWidth:'none'});
          const cw=Math.max(pane.width,w),ch=Math.max(pane.height,h);
          Object.assign(content.style,{position:'relative',width:`${cw}px`,height:`${ch}px`});
          Object.assign(target.style,{position:'absolute',left:`${(cw-w)/2}px`,top:`${(ch-h)/2}px`,width:`${w}px`,height:`${h}px`});
          content.append(target);clip.append(content);next.append(clip);panesById.set(pageNumber,{clip,scale:renderScale});
          releases.push(await surface.mount(target,renderScale,operation));throwIfAborted(operation);
          if(pageNumber===at){applied=renderScale;fittedCurrent=fitted;}
        }
        if(!(applied>0))throw new Error('page_invalid');
        return {next,releases,created,visualPages,vertical,fold:{panes:panesById,applied,fitted:fittedCurrent}};
      }
      const width=vertical?(verticalWidth??surfaces[visualPages.indexOf(at)].width):surfaces.reduce((sum,s)=>sum+s.width,0),height=vertical?surfaces.reduce((sum,s)=>sum+s.height*width/s.width,0):Math.max(...surfaces.map(s=>s.height));
      const currentSurface=surfaces[visualPages.indexOf(at)],fitHeight=vertical?currentSurface.height*width/currentSurface.width:height;
      const fitted=layout.fit==='width'?viewport.width/width:layout.fit==='height'?viewport.height/fitHeight:Math.min(viewport.width/width,viewport.height/fitHeight);
      const requested=zoom?.kind==='scale'?zoom.value:fitted;
      if(!Number.isFinite(requested)||requested<=0||!Number.isFinite(width)||!Number.isFinite(height))throw new Error('page_invalid');
      Object.assign(next.style,{position:'relative',display:'flex',flexDirection:vertical?'column':'row',flex:'none',alignItems:'center',width:`${width*requested}px`,height:`${height*requested}px`,minWidth:'0',margin:'auto'});
      for(let i=0;i<surfaces.length;i++){
        const surface=surfaces[i],renderScale=vertical?requested*width/surface.width:requested,target=document.createElement('div');target.dataset.readerPage=String(visualPages[i]);
        Object.assign(target.style,{position:'relative',flex:'none',width:`${surface.width*renderScale}px`,height:`${surface.height*renderScale}px`,overflow:'hidden'});next.append(target);
        releases.push(await surface.mount(target,renderScale,operation));throwIfAborted(operation);
      }
      return {next,releases,created,visualPages,vertical,flat:{requested,fitted,width,geometry:!vertical&&surfaces.length===1?{page:at,width:surfaces[0].width,height:surfaces[0].height,scale:requested,fittedScale:fitted}:undefined}};
    }catch(error){for(const release of releases.reverse())release();for(const surface of created.values())surface.dispose();throw error;}
  }
  async function render(at:number,taskSignal:AbortSignal,anchorOffset?:number):Promise<void>{
    if(closed)throw new Error('reader_closed');throwIfAborted(taskSignal);
    if(!Number.isInteger(at)||at<0||at>=pageCount)throw new Error('page_invalid');
    pending?.abort();const controller=new AbortController();pending=controller;const current=++version;
    if(gesture?.released){gesture.claimed=true;clearTimeout(gesture.timer);}
    // The spread already showing beside a swipe is the one this turn lands on; anything else is stale.
    const shown=peek?.at===at?peek:undefined;if(shown){shown.taken=true;peek=undefined;}else dropPeek();
    const task=abortScope([signal,controller.signal,taskSignal]),operation=task.signal;
    rendering++;let built:Composed|undefined,swapped=false;
    try{
      if(shown){const stop=()=>shown.controller.abort();operation.addEventListener('abort',stop,{once:true});
        try{built=await shown.ready;}finally{operation.removeEventListener('abort',stop);shown.scope.dispose();}
        if(built&&shown.restore)Object.assign(built.next.style,shown.restore);built?.next.removeAttribute('aria-hidden');
        throwIfAborted(operation);
      }
      built??=await compose(at,operation);
      if(closed||current!==version)throw new DOMException('','AbortError');
      const {next,releases,created,visualPages,vertical}=built;
      if(built.fold){swap(next,releases,turnFrom(at,false));swapped=true;foldPanes=built.fold.panes;page=at;scale=built.fold.applied;fittedScale=built.fold.fitted;verticalWidth=undefined;pageGeometry=undefined;}
      else{const flat=built.flat!;swap(next,releases,turnFrom(at,vertical));swapped=true;foldPanes=undefined;page=at;scale=flat.requested;fittedScale=flat.fitted;if(vertical)verticalWidth=flat.width;pageGeometry=flat.geometry;}
      for(const [index,surface]of created)surfacesByPage.set(index,surface);
      for(const [index,surface]of surfacesByPage)if(!visualPages.includes(index)){const owner=turning;surfacesByPage.delete(index);if(owner)owner.defer(()=>surface.dispose());else surface.dispose();}
      if(foldPanes){for(const {clip} of foldPanes.values()){clip.scrollLeft=0;clip.scrollTop=0;}}
      else{host.scrollLeft=direction()==='rtl'?host.scrollWidth-host.clientWidth:0;host.scrollTop=vertical?(next.querySelector<HTMLElement>(`[data-reader-page="${at}"]`)!.offsetTop+(anchorOffset??0)):0;}
      location=locate();options.onLocation(location);
    }catch(error){if(built&&!swapped)discard(built);throw error;}finally{rendering--;task.dispose();if(!rendering&&gesture?.released)settleBack();}
  }
  // motion.html 5.1 swipe: at fit, in paged layout, a horizontal touch drag moves the spread with the
  // finger (also under reduced motion: finger tracking stays) while the neighbouring spread is built
  // beside it. Past a quarter of the width or a flick it asks the chrome for the turn, and swap() settles
  // from the release; otherwise, at a book edge, or when nothing turns, it springs back.
  let gesture:{id:number;element:HTMLElement;x:number;base:number;offset:number;velocity:number;samples:{t:number;x:number}[];released:boolean;claimed:boolean;timer?:ReturnType<typeof setTimeout>}|undefined;
  let peek:{at:number;side:1|-1;controller:AbortController;scope:ReturnType<typeof abortScope>;ready:Promise<Composed|undefined>;built?:Composed;restore?:Partial<CSSStyleDeclaration>;taken:boolean}|undefined;
  let settling:{finish:()=>void}|undefined;
  const settled=()=>{const running=settling;settling=undefined;running?.finish();};
  const forgetGesture=()=>{const held=gesture;gesture=undefined;if(!held)return;clearTimeout(held.timer);held.element.style.transform='';};
  const discardPeek=(held:NonNullable<typeof peek>)=>{if(held.built)discard(held.built);held.controller.abort();held.scope.dispose();};
  const dropPeek=()=>{const held=peek;peek=undefined;if(held)discardPeek(held);};
  const neighbour=(side:1|-1)=>((side===1)!==(direction()==='rtl')?plan().nextPage:plan().previousPage)??undefined;
  const paint=()=>{const held=gesture;if(!held)return;held.element.style.transform=`translateX(${held.offset}px)`;
    if(peek?.built&&!peek.taken)peek.built.next.style.transform=`translateX(${held.offset+peek.side*host.clientWidth}px)`;};
  const showPeek=(at:number,side:1|-1)=>{
    const controller=new AbortController(),scope=abortScope([signal,controller.signal]);
    const entry:NonNullable<typeof peek>={at,side,controller,scope,taken:false,ready:Promise.resolve(undefined)};
    entry.ready=compose(at,scope.signal).then(built=>{
      if(entry.taken)return built;
      if(peek!==entry||closed){discard(built);return undefined;}
      const next=built.next,{position,left,top,margin,pointerEvents,transform}=next.style;entry.restore={position,left,top,margin,pointerEvents,transform};
      const w=parseFloat(next.style.width)||0,h=parseFloat(next.style.height)||0;
      Object.assign(next.style,{position:'absolute',left:`${host.scrollLeft+Math.max(0,(host.clientWidth-w)/2)}px`,top:`${host.scrollTop+Math.max(0,(host.clientHeight-h)/2)}px`,margin:'0',pointerEvents:'none'});
      next.setAttribute('aria-hidden','true');entry.built=built;host.append(next);paint();return built;
    },()=>undefined);
    peek=entry;
  };
  const settleBack=()=>{
    const held=gesture;gesture=undefined;if(!held){dropPeek();return;}clearTimeout(held.timer);settled();
    // The neighbour returns beside the page rather than vanishing; it is released when both are home.
    const beside=peek?.built&&!peek.taken?peek:undefined;if(!beside)dropPeek();else peek=undefined;
    const element=held.element,width=host.clientWidth;
    if(!element.isConnected||!held.offset||typeof element.animate!=='function'){element.style.transform='';if(beside)discardPeek(beside);moving(false);return;}
    const reduce=reducedMotion(),frames=reduce?{values:[held.offset,0],duration:160}:settleFrames(held.offset,0,held.velocity),timing={duration:frames.duration,easing:'linear'};
    element.style.transform='';
    const animations=[element.animate(frames.values.map(x=>({transform:`translateX(${x}px)`})),timing)];
    if(beside){const next=beside.built!.next;next.style.transform=`translateX(${beside.side*width}px)`;animations.push(next.animate(frames.values.map(x=>({transform:`translateX(${x+beside.side*width}px)`})),timing));}
    const run={finish:()=>{for(const animation of animations)animation.cancel();if(beside)discardPeek(beside);if(settling===run)settling=undefined;moving(false);}};
    settling=run;animations[0].onfinish=run.finish;
  };
  const currentShift=(element:Element)=>{try{const value=getComputedStyle(element).transform;return value&&value!=='none'?new DOMMatrixReadOnly(value).m41:0;}catch{return 0;}};
  const startSwipe=(event:PointerEvent,origin:number)=>{
    turning?.finish();const element=host.firstElementChild as HTMLElement|null;if(!element)return false;
    const base=settling?currentShift(element):0;settled();
    gesture={id:event.pointerId,element,x:origin,base,offset:base,velocity:0,samples:[],released:false,claimed:false};moving(true);return true;
  };
  const trackSwipe=(event:PointerEvent)=>{
    const held=gesture!;let offset=held.base+event.clientX-held.x;const side:1|-1=offset<0?1:-1,at=neighbour(side);
    // A book edge resists instead of revealing nothing.
    if(at===undefined)offset=held.base+(event.clientX-held.x)/3;
    held.offset=offset;held.samples.push({t:event.timeStamp,x:event.clientX});while(held.samples.length>2&&event.timeStamp-held.samples[0].t>100)held.samples.shift();
    if(peek&&(peek.side!==side||peek.at!==at))dropPeek();
    if(!peek&&at!==undefined)showPeek(at,side);
    paint();
  };
  const releaseSwipe=(event:PointerEvent)=>{
    const held=gesture!;held.released=true;
    const first=held.samples[0],span=first?event.timeStamp-first.t:0;held.velocity=first&&span>0?(event.clientX-first.x)/span*1000:0;
    const rtl=direction()==='rtl',intent=event.type==='pointerup'&&view.canTurnAtFit!()?pageGesture(host.clientWidth,held.offset,held.velocity,rtl):0;
    const target=(intent===1?plan().nextPage:intent===-1?plan().previousPage:undefined)??undefined;
    if(target===undefined){settleBack();return;}
    // Nothing claims the turn (an open panel, turns disabled): spring back rather than hold.
    held.timer=setTimeout(()=>{if(gesture===held&&!held.claimed)settleBack();},250);
    options.onSurfaceInput?.({kind:'swipe',direction:(intent===1)!==rtl?'left':'right'});
  };
  const pointers=new Map<number,{x:number;y:number}>();let pinch:number|undefined,drag:{x:number;y:number;left:number;top:number;mode?:'pan'|'swipe'}|undefined,tap:{id:number;x:number;y:number;multiple:boolean;moved:boolean}|undefined;
  let pendingTap:{timer:ReturnType<typeof setTimeout>;x:number;y:number;time:number}|undefined;
  const clearTap=()=>{if(pendingTap){clearTimeout(pendingTap.timer);pendingTap=undefined;}};
  const gestureZoom=(next:Zoom)=>{void view.setZoom!(next,signal).then(()=>{if(!closed)options.onSurfaceInput?.({kind:'activity'});}).catch(()=>{});};
  const distance=()=>{const a=[...pointers.values()];return a.length===2?Math.hypot(a[1].x-a[0].x,a[1].y-a[0].y):undefined;};
  const onDown=(event:PointerEvent)=>{
    // A completed centre tap owns the chrome toggle. Do not wake it on
    // contact first, which would invert the user's hidden/shown intention.
    if(!tap)tap={id:event.pointerId,x:event.clientX,y:event.clientY,multiple:false,moved:false};else{tap.multiple=true;clearTap();options.onSurfaceInput?.({kind:'activity'});}
    // PDF text selection must retain browser handles and native selection semantics.
    if(event.pointerType!=='touch'&&(event.target as Element)?.closest?.('[data-reader-text] span'))return;
    pointers.set(event.pointerId,{x:event.clientX,y:event.clientY});try{host.setPointerCapture?.(event.pointerId);}catch{/* synthetic pointers */}
    if(pointers.size===1){const hit=(event.target as Element|null)?.closest?.('[data-reader-page]');const clip=hit instanceof HTMLElement?foldPanes?.get(Number(hit.dataset.readerPage))?.clip:undefined;const pane=clip&&clip===hit?clip:undefined;dragPane=pane;gesturePage=pane?Number(pane.dataset.readerPage):undefined;const at=pane?{left:pane.scrollLeft,top:pane.scrollTop}:scrollState();drag={x:event.clientX,y:event.clientY,left:at.left,top:at.top};}else{if(gesture&&!gesture.released)settleBack();pinch=distance();drag=undefined;dragPane=undefined;}
  };
  const onMove=(event:PointerEvent)=>{
    if(!pointers.has(event.pointerId))return;
    if(tap?.id===event.pointerId&&Math.hypot(event.clientX-tap.x,event.clientY-tap.y)>=8)tap.moved=true;
    if(pointers.size>1||tap?.moved)options.onSurfaceInput?.({kind:'activity'});
    pointers.set(event.pointerId,{x:event.clientX,y:event.clientY});const nextDistance=distance();
    if(pinch&&nextDistance){const next=Math.max(.05,Math.min(8,scale*nextDistance/pinch));pinch=nextDistance;gestureZoom({kind:'scale',value:next});}
    else if(drag&&tap?.moved){
      if(!drag.mode){const dx=event.clientX-drag.x,dy=event.clientY-drag.y,scroller=dragPane??host;
        drag.mode=event.pointerType!=='mouse'&&Math.abs(dx)>Math.abs(dy)&&direction()!=='vertical'&&view.canTurnAtFit!()&&scroller.scrollWidth-scroller.clientWidth<=1&&startSwipe(event,drag.x)?'swipe':'pan';}
      if(drag.mode==='swipe'){if(gesture?.id===event.pointerId&&!gesture.released)trackSwipe(event);return;}
      if(dragPane){dragPane.scrollLeft=Math.max(0,drag.left+drag.x-event.clientX);dragPane.scrollTop=Math.max(0,drag.top+drag.y-event.clientY);}else scrollTo(drag.left+drag.x-event.clientX,drag.top+drag.y-event.clientY);scheduleViewport();}
  };
  const onUp=(event:PointerEvent)=>{if(gesture&&!gesture.released&&gesture.id===event.pointerId)releaseSwipe(event);dragPane=undefined;
    pointers.delete(event.pointerId);pinch=undefined;drag=undefined;if(!pointers.size)gesturePage=undefined;
    if(tap?.id===event.pointerId){
      const candidate=tap;tap=undefined;
      if(!candidate.multiple&&!candidate.moved&&event.type==='pointerup'&&Math.hypot(event.clientX-candidate.x,event.clientY-candidate.y)<8&&document.getSelection()?.isCollapsed!==false){
        const rect=host.getBoundingClientRect();if(rect.width<=0)return;
        if(pendingTap&&event.timeStamp-pendingTap.time<=300&&Math.hypot(event.clientX-pendingTap.x,event.clientY-pendingTap.y)<24){clearTap();onDouble();}
        else{
          clearTap();const fraction=Math.max(0,Math.min(1,(event.clientX-rect.left)/rect.width)),atVersion=version;
          const timer=setTimeout(()=>{pendingTap=undefined;if(!closed&&version===atVersion)options.onSurfaceInput?.({kind:'tap',fraction});},300);
          pendingTap={timer,x:event.clientX,y:event.clientY,time:event.timeStamp};
        }
      }
    }
  };
  const onKey=(event:KeyboardEvent)=>{
    const target=event.target as HTMLElement;
    options.onSurfaceInput?.({kind:'key',key:event.key,shift:event.shiftKey,alt:event.altKey,ctrl:event.ctrlKey,meta:event.metaKey,editable:!!target.closest?.('input,textarea,select,[contenteditable=true]')||document.getSelection()?.isCollapsed===false,preventDefault:()=>event.preventDefault()});
  };
  const onWheel=(event:WheelEvent)=>{if(!event.ctrlKey)return;event.preventDefault();gestureZoom({kind:'scale',value:Math.max(.05,Math.min(8,scale*Math.exp(-event.deltaY*.002)))});};
  const onDouble=()=>{gestureZoom(zoom?.kind==='scale'?{kind:'fit',value:'page'}:{kind:'scale',value:Math.min(8,scale*2)});};
  const view:ReaderView<P>={
    async navigate(target,taskSignal){
      mutation++;clearTap();
      let at=page;
      if(target.kind==='next')at=plan().nextPage??page;
      else if(target.kind==='previous')at=plan().previousPage??page;
      else if(target.kind==='progression'){if(!Number.isFinite(target.value)||target.value<0||target.value>1)throw new Error('page_invalid');at=Math.round(target.value*(pageCount-1));}
      else if(target.kind==='location'){if(target.location.format==='epub'||target.location.format!==format)throw new Error('page_invalid');at=target.location.page;}
      else throw new Error('page_invalid');
      await render(at,taskSignal);
    },
    async applyPreferences(next,taskSignal){
      throwIfAborted(taskSignal);const own=++mutation,previous=preferences,before=plan(),held=scrollState();preferences=next;
      const after=plan(),samePages=before.visualPages.join(',')===after.visualPages.join(',');
      if(samePages&&before.fit===after.fit&&before.direction===after.direction)return;
      try{await render(page,taskSignal);if(samePages&&zoom?.kind==='scale'){scrollTo(held.left,held.top,page);publishViewport();}}
      catch(error){if(own===mutation)preferences=previous;throw error;}
    },
    async resize(next,taskSignal){const own=++mutation,previous=viewport;viewport=next;try{await render(page,taskSignal);}catch(error){if(own===mutation)viewport=previous;throw error;}},
    getLocation(){return location;},
    async setZoom(next,taskSignal){
      if(next.kind==='scale'&&(!Number.isFinite(next.value)||next.value<.05||next.value>8)||next.kind==='fit'&&!['page','width','height','spread'].includes(next.value))throw new Error('zoom_invalid');
      const own=++mutation,previous=zoom,anchor=gesturePage??page,held=scrollState(anchor),x=(held.left+held.width/2)/scale,y=(held.top+held.height/2)/scale;zoom=next;
      try{await render(page,taskSignal);scrollTo(x*scale-held.width/2,y*scale-held.height/2,anchor);publishViewport();}catch(error){if(own===mutation)zoom=previous;throw error;}
    },
    getZoom(){return {scale,...zoom?.kind==='scale'?{}:{fit:plan().fit}};},
    canTurnAtFit(){return zoom?.kind!=='scale'||Math.abs(scale-fittedScale)<=1e-6;},
    async pan(position,taskSignal){throwIfAborted(taskSignal);if(closed||!Number.isFinite(position.x)||!Number.isFinite(position.y))throw new Error('pan_invalid');scrollTo(position.x,position.y,page);publishViewport();},
    dispose(){if(closed)return;closed=true;version++;turning?.finish();settled();dropPeek();if(gesture)clearTimeout(gesture.timer);gesture=undefined;clearTap();if(viewportFrame!==undefined){cancelAnimationFrame(viewportFrame);viewportFrame=undefined;}pending?.abort();lifetime.abort();scope.dispose();clean();for(const surface of surfacesByPage.values())surface.dispose();surfacesByPage.clear();paneStyle.remove();host.remove();}
  };
  const onScroll=()=>{
    clearTap();if(closed||rendering||scrolling)return;options.onSurfaceInput?.({kind:'activity'});scheduleViewport();
    if(direction()!=='vertical')return;
    const bounds=host.getBoundingClientRect();let greatest=0,visible:HTMLElement|undefined;
    for(const element of host.querySelectorAll<HTMLElement>('[data-reader-page]')){
      const rect=element.getBoundingClientRect(),intersection=Math.max(0,Math.min(bounds.bottom,rect.bottom)-Math.max(bounds.top,rect.top));
      if(intersection>greatest){greatest=intersection;visible=element;}
    }
    if(!visible)return;const at=Number(visible.dataset.readerPage);if(at===page)return;
    const offset=host.scrollTop-visible.offsetTop;page=at;location=locate();options.onLocation(location);
    mutation++;scrolling=true;
    void render(at,signal,offset).then(()=>{if(!closed)options.onSurfaceInput?.({kind:'activity'});}).catch(()=>{}).finally(()=>{scrolling=false;});
  };
  host.addEventListener('pointerdown',onDown,{signal});host.addEventListener('pointermove',onMove,{signal});host.addEventListener('pointerup',onUp,{signal});host.addEventListener('pointercancel',onUp,{signal});host.addEventListener('wheel',onWheel,{passive:false,signal});
  host.addEventListener('keydown',onKey,{signal});host.addEventListener('focusin',()=>options.onSurfaceInput?.({kind:'activity'}),{signal});
  host.addEventListener('scroll',onScroll,{passive:true,signal});
  signal.addEventListener('abort',()=>{void view.dispose();},{once:true});
  try{await render(0,signal);throwIfAborted(signal);options.container.append(host);return view;}catch(error){await view.dispose();throw error;}
}
