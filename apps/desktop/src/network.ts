import {invoke} from '@tauri-apps/api/core';

export type DesktopNetworkCost='unknown'|'metered'|'unmetered';
export type NetworkCostBridge=Readonly<{read():Promise<unknown>}>;
export const nativeNetworkCostBridge:NetworkCostBridge=Object.freeze({read:()=>invoke<unknown>('desktop_network_cost')});
type Timers=Readonly<{set(callback:()=>void,ms:number):unknown;clear(handle:unknown):void}>;
const defaultTimers:Timers={set:(callback,ms)=>setTimeout(callback,ms),clear:handle=>clearTimeout(handle as ReturnType<typeof setTimeout>)};

/** A device observation only. It owns no preparation list, transfer or auth scope.
 * Expired/failed/late observations never grant an automatic unmetered transfer. */
export class DesktopNetworkMonitor {
  private active=false;
  private disposed=false;
  private epoch=0;
  private pending=false;
  private value:DesktopNetworkCost='unknown';
  private observedAt=-Infinity;
  private timer:unknown;
  private listeners=new Set<()=>void>();
  private readonly bridge:NetworkCostBridge;
  private readonly clock:()=>number;
  private readonly timers:Timers;
  constructor(bridge:NetworkCostBridge=nativeNetworkCostBridge,clock:()=>number=()=>performance.now(),timers:Timers=defaultTimers){this.bridge=bridge;this.clock=clock;this.timers=timers;}
  readonly current=():DesktopNetworkCost=>{const age=this.clock()-this.observedAt;return this.active&&age>=0&&age<=10000?this.value:'unknown';};
  readonly subscribe=(listener:()=>void)=>{this.listeners.add(listener);return()=>{this.listeners.delete(listener);};};
  start(){if(this.active||this.disposed)return;this.active=true;++this.epoch;void this.refresh();}
  async refresh():Promise<void>{
    if(!this.active||this.pending)return;
    this.pending=true;const epoch=this.epoch;
    try{
      const result=await this.bridge.read();
      if(!this.active||epoch!==this.epoch)return;
      const before=this.current();
      this.value=result==='metered'||result==='unmetered'?result:'unknown';
      this.observedAt=this.clock();
      if(before!==this.current())for(const listener of [...this.listeners])listener();
    }catch{
      if(this.active&&epoch===this.epoch){const before=this.current();this.value='unknown';this.observedAt=-Infinity;if(before!=='unknown')for(const listener of [...this.listeners])listener();}
    }finally{
      this.pending=false;
      if(this.active){if(this.timer!==undefined)this.timers.clear(this.timer);this.timer=this.timers.set(()=>{this.timer=undefined;void this.refresh();},5000);}
    }
  }
  dispose(){this.disposed=true;this.active=false;++this.epoch;this.value='unknown';this.observedAt=-Infinity;if(this.timer!==undefined)this.timers.clear(this.timer);this.timer=undefined;this.listeners.clear();}
}
