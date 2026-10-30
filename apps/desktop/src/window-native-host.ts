import type {ReactNode} from 'react';
import {createDesktopWindowController} from './window-controller.ts';
import {nativeWindowBridge,type ChromeUpdate,type WindowAction,type WindowBridge,type WindowChromeHostState,type WindowSnapshot} from './window.ts';
/** The mounted caption controls (caption-controls.tsx), injected by main so this host
 * module loads without React or CSS. */
export type CaptionControlsFactory=(ports:Readonly<{action:(action:WindowAction)=>Promise<void>;geometry:(rects:ChromeUpdate['captions'])=>void}>)=>Readonly<{
  render:ReactNode;update(native:WindowSnapshot,state:WindowChromeHostState):void;dispose():void;
}>;
/** Headless binding of existing shell markers to the actual native window.
 * Windows controls report mounted geometry; Mac/Linux retain native controls. */
export function createDesktopNativeWindowHost(options:Readonly<{
  onEnvironment:(value:{os:WindowSnapshot['os'];fullscreen:boolean})=>void;
  captions?:CaptionControlsFactory;bridge?:WindowBridge;prepareToClose?:()=>Promise<void>;page?:Document;
}>){
  const page=options.page??document,report=()=>console.warn('window_unavailable');
  let captions:ReturnType<CaptionControlsFactory>|undefined;
  const controller=createDesktopWindowController({bridge:options.bridge??nativeWindowBridge,onEnvironment:options.onEnvironment,
    onSnapshot(native,state){captions?.update(native,state);},onError:report,prepareToClose:options.prepareToClose});
  captions=options.captions?.({action:value=>controller.action(value),geometry:value=>controller.geometry(value)});
  const interactive='a,button,input,textarea,select,summary,[contenteditable]:not([contenteditable=false]),[role=button],[role=link],[data-window-no-drag]';
  const region=(event:MouseEvent)=>{
    const native=controller.native();
    if(!native||native.os==='linux'||native.fullscreen||controller.state().fullscreen||event.button!==0)return false;
    for(const target of event.composedPath()){
      if(!(target instanceof Element))continue;
      if(target.matches(interactive))return false;
      if(target.hasAttribute('data-window-drag-region'))return true;
    }
    return false;
  };
  let macDouble:Readonly<{x:number;y:number}>|undefined,closing:Promise<void>|undefined;
  const down=(event:MouseEvent)=>{
    if(!region(event)||event.detail<1||event.detail>2)return;
    event.preventDefault();event.stopImmediatePropagation();
    if(controller.native()?.os==='macos'&&event.detail===2){macDouble={x:event.clientX,y:event.clientY};return;}
    void controller.action(event.detail===2?'toggleMaximize':'drag');
  };
  const up=(event:MouseEvent)=>{
    const start=macDouble;macDouble=undefined;
    if(start&&event.detail===2&&event.clientX===start.x&&event.clientY===start.y&&region(event))void controller.action('toggleMaximize');
  };
  page.addEventListener('mousedown',down,true);page.addEventListener('mouseup',up,true);
  const timer=setInterval(()=>{void controller.read();},200);void controller.read();
  return Object.freeze({render:captions?.render,update:(state:WindowChromeHostState)=>controller.update(state),dispose(){
    if(closing)return closing;captions?.dispose();clearInterval(timer);page.removeEventListener('mousedown',down,true);page.removeEventListener('mouseup',up,true);closing=controller.dispose();return closing;
  }});
}
