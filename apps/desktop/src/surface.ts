import type {SurfaceBounds} from './bridge.ts';

type Saved = Readonly<{element: HTMLElement; property: string; value: string; priority: string}>;
/** Native-only paint mechanics. Default web paint and every control/portal stay with web.
 * Cut a measured hole in specifically identified BELOW-video paint branches, never the dock
 * owner (which contains controls and the moving surface). Restore every changed property. */
export class NativeHole {
  private readonly video: HTMLVideoElement;
  private readonly send: (bounds: SurfaceBounds) => Promise<void>;
  private readonly active: () => boolean;
  private saved: Saved[] = [];
  private disposed = false;
  private frame = 0;
  private pending = false;
  private epoch = 0;
  private lastBounds = '';
  private lastPaint = '';
  constructor(video: HTMLVideoElement, send: (bounds: SurfaceBounds) => Promise<void>, active: () => boolean) {
    this.video = video; this.send = send; this.active = active;
    this.frame = requestAnimationFrame(() => this.update());
  }
  private set(element: HTMLElement, property: string, value: string) {
    this.saved.push({element, property, value: element.style.getPropertyValue(property), priority: element.style.getPropertyPriority(property)});
    element.style.setProperty(property, value, 'important');
  }
  private restore() {
    for (const {element, property, value, priority} of this.saved.reverse()) {
      if (value) element.style.setProperty(property,value,priority); else element.style.removeProperty(property);
    }
    this.saved = [];
  }
  private update() {
    if (this.disposed) return;
    const surface = this.video.closest<HTMLElement>('[data-media-surface]');
    const rect = this.video.getBoundingClientRect();
    const visible = !!surface && getComputedStyle(surface).visibility !== 'hidden' && rect.width > 0 && rect.height > 0 && this.video.isConnected;
    const bounds = visible ? {x: rect.left,y: rect.top,width: rect.width,height: rect.height} : {x:0,y:0,width:0,height:0};
    const key = JSON.stringify(bounds);
    if (!this.pending && key !== this.lastBounds) {
      this.pending = true;
      const epoch=this.epoch;
      void this.send(bounds).then(() => { if(epoch===this.epoch)this.lastBounds = key; }).catch(() => {}).finally(() => { this.pending = false; });
    }
    const paint = visible && this.active() && surface?.dataset.nativeVideoActive === 'true' ? key : '';
    if (paint !== this.lastPaint) {
      this.restore(); this.lastPaint = paint;
      if (paint) this.paint(bounds);
    }
    this.frame = requestAnimationFrame(() => this.update());
  }
  private paint(hole: SurfaceBounds) {
    const root = document.documentElement;
    const original = getComputedStyle(root);
    // The checked web root has a solid fill. A future backdrop needs its own explicit seam.
    if (original.backgroundImage !== 'none') return;
    const color = original.backgroundColor, w = innerWidth, h = innerHeight;
    const left = Math.max(0,Math.min(w,hole.x)), top = Math.max(0,Math.min(h,hole.y));
    const right = Math.max(left,Math.min(w,hole.x+hole.width)), bottom = Math.max(top,Math.min(h,hole.y+hole.height));
    this.set(root,'background-color','transparent');
    this.set(root,'background-image',Array(4).fill(`linear-gradient(${color},${color})`).join(','));
    this.set(root,'background-repeat','no-repeat'); this.set(root,'background-attachment','fixed');
    this.set(root,'background-size',`${w}px ${top}px, ${w}px ${h-bottom}px, ${left}px ${bottom-top}px, ${w-right}px ${bottom-top}px`);
    this.set(root,'background-position',`0 0, 0 ${bottom}px, 0 ${top}px, ${right}px ${top}px`);
    for (const element of document.querySelectorAll<HTMLElement>('[data-route-content], [data-shell-navigation], [data-native-underlay-branch="dock"]')) {
      const r=element.getBoundingClientRect(), x=left-r.left,y=top-r.top, rgt=right-r.left, btm=bottom-r.top;
      if (r.right<=left||r.left>=right||r.bottom<=top||r.top>=bottom) continue;
      // evenodd path: normal paint outside the video rectangle, transparent only beneath it.
      this.set(element,'clip-path',`path(evenodd, "M0 0 H${r.width} V${r.height} H0 Z M${x} ${y} H${rgt} V${btm} H${x} Z")`);
    }
  }
  invalidate(){++this.epoch;this.lastBounds='';}
  dispose() { if (this.disposed) return; this.disposed=true;++this.epoch; cancelAnimationFrame(this.frame); this.restore(); }
}
