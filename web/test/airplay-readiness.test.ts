import test from 'node:test';
import assert from 'node:assert/strict';
import {mediaAirPlayPort} from '../src/platform/playback/play-on.ts';

const safari='Version/18.0 Safari/605.1.15';
class PickerVideo extends EventTarget {
  disableRemotePlayback=false;
  picks=0;
  webkitShowPlaybackTargetPicker(){this.picks++;}
}

test('the browser AirPlay port uses the current source at the gesture and never borrows stale availability',()=>{
 const video=new PickerVideo();let current=false,throws=false;
 const unbound=mediaAirPlayPort(video as unknown as HTMLVideoElement,safari);assert.equal(unbound.available(),false);unbound.showPicker();assert.equal(video.picks,0);
 const port=mediaAirPlayPort(video as unknown as HTMLVideoElement,safari,{isCurrent:()=>{if(throws)throw new Error('retired');return current;},subscribe:()=>()=>{}});
 assert.equal(port.available(),false);port.showPicker();assert.equal(video.picks,0);
 current=true;assert.equal(port.available(),true);port.showPicker();assert.equal(video.picks,1);
 current=false;port.showPicker();assert.equal(video.picks,1);
 throws=true;assert.equal(port.available(),false);port.showPicker();assert.equal(video.picks,1);
 throws=false;current=true;video.disableRemotePlayback=true;assert.equal(port.available(),false);port.showPicker();assert.equal(video.picks,1);
});

test('AirPlay subscribes to current-source readiness and releases that subscription with its media listeners',t=>{
 const original=Object.getOwnPropertyDescriptor(globalThis,'MutationObserver');let disconnected=0;
 class Observer {constructor(_callback:MutationCallback){}observe(){}disconnect(){disconnected++;}}
 Object.defineProperty(globalThis,'MutationObserver',{configurable:true,value:Observer});
 t.after(()=>{if(original)Object.defineProperty(globalThis,'MutationObserver',original);else Reflect.deleteProperty(globalThis,'MutationObserver');});
 const video=new PickerVideo(),listeners=new Set<()=>void>();let changes=0,current=true;
 const port=mediaAirPlayPort(video as unknown as HTMLVideoElement,safari,{isCurrent:()=>current,subscribe:listener=>{listeners.add(listener);return()=>{listeners.delete(listener);};}});
 const off=port.subscribe(()=>{changes++;});assert.equal(listeners.size,1);
 current=false;for(const listener of listeners)listener();assert.equal(port.available(),false);assert.equal(changes,1);
 video.dispatchEvent(new Event('emptied'));assert.equal(changes,2);
 off();assert.equal(listeners.size,0);assert.equal(disconnected,1);
 video.dispatchEvent(new Event('loadedmetadata'));assert.equal(changes,2);
});
