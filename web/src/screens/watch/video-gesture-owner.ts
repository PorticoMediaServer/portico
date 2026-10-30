import {springTiming, springs} from '@design';
import {PlayerMorph} from '../../kit/dock/motion';
import {videoGestureFrame, videoGestureGeometry, videoGesturePosition, videoGestureRelease, type VideoFrame, type VideoGestureGeometry} from '../../kit/dock/video-gesture-geometry';
import type {VideoPointer, VideoSurfacePaint} from '../../kit/dock/video-gesture-port';

type Nodes = Readonly<{surface: VideoSurfacePaint; dock: HTMLElement; full: HTMLElement; screen: HTMLElement; bar: HTMLElement; copies: readonly HTMLElement[]}>;
type Input = Readonly<{
  current: () => boolean; nodes: () => Nodes | undefined; reduced: () => boolean;
  state: (preview: boolean, active: boolean) => void; expanded: () => boolean; commit: (expanded: boolean) => boolean;
}>;
type Drag = {id: number; pointer: HTMLElement; start: 0 | 1; x: number; y: number; lastY: number; stamp: number; vy: number;
  dy: number; originPosition: number; parents: readonly (HTMLElement | null)[]; nodes: Nodes; geometry?: VideoGestureGeometry; origin?: VideoFrame; position: number; moved: boolean};
const keys = ['left', 'top', 'width', 'height'] as const;
let sequence = 0;
const frame = (value: DOMRect): VideoFrame => ({left: value.left, top: value.top, width: value.width, height: value.height});
const clamp = (n: number) => Math.max(0, Math.min(1, n));

/** One original pointer and original local-video lifetime owns paint through settle.
 * No new media/session is mounted here. Missing slots leave the discrete control alone. */
export class VideoGestureOwner {
  private live = true;
  private refusedPointer?: number;
  private drag?: Drag;
  private token = `video-gesture-${++sequence}`;
  private epoch = 0;
  private jobs: Animation[] = [];
  private progress?: PlayerMorph;
  private geometry?: VideoGestureGeometry;
  private settling?: {drag: Drag; target: 0 | 1; deadline: number};
  private paintPosition?: number;
  private quiet = false;
  private painted?: Nodes;
  private committedTarget?: 0 | 1;
  private saved?: {screen: string; opacity: string; background: string; bar: string; inert: boolean; copies: readonly {node: HTMLElement; opacity: string; base: number}[]};
  constructor(private input: Input) {}
  private current(nodes: Nodes) {
    const now = this.input.nodes(), active = document.activeElement;
    if (active instanceof Element && active.closest('[role="dialog"],[role="menu"],[aria-modal="true"]')) return false;
    return this.live && this.input.current() && now?.surface === nodes.surface && now.dock === nodes.dock && now.full === nodes.full
      && now.screen === nodes.screen && now.bar === nodes.bar && now.copies.length === nodes.copies.length && now.copies.every((node, i) => node === nodes.copies[i])
      && [nodes.surface.element, nodes.dock, nodes.full, nodes.screen, nodes.bar, ...nodes.copies].every(node => node.isConnected)
      && nodes.screen.contains(nodes.full) && nodes.bar.contains(nodes.dock);
  }
  private leaseCurrent(g: Drag) {
    const n = g.nodes;
    if (!this.current(n) || !g.pointer.isConnected || this.input.expanded() !== (g.start === 0)) return false;
    const parents = [n.surface.element.parentElement, n.dock.parentElement, n.full.parentElement, n.screen.parentElement, n.bar.parentElement, g.pointer.parentElement];
    if (parents.some((parent, i) => parent !== g.parents[i])) return false;
    if (g.geometry) {
      const dock = n.dock.getBoundingClientRect(), full = n.full.getBoundingClientRect();
      if (keys.some(key => Math.abs(dock[key] - g.geometry!.dock[key]) > .1)
        || Math.abs(full.width - g.geometry.full.width) > .1 || Math.abs(full.height - g.geometry.full.height) > .1) return false;
    }
    return true;
  }
  observe() {
    const g = this.drag ?? this.settling?.drag;
    if (g && !this.leaseCurrent(g)) {this.cancel();return;}
    this.refresh();
  }
  begin = (event: VideoPointer, pointer: HTMLElement, start: 0 | 1) => {
    if (!this.live || !this.input.current() || this.input.expanded() !== (start === 0) || event.button !== 0 || event.pointerType === 'mouse' || !pointer.isConnected) return false;
    const active = document.activeElement;
    if (active instanceof Element && active.closest('input,textarea,[contenteditable="true"],[role="dialog"],[role="menu"]')) return false;
    const nodes = this.input.nodes(); if (!nodes || !this.current(nodes)) return false;
    // A second finger never adopts the first pointer's lease.
    if (this.drag) return false;
    this.refusedPointer = undefined;
    this.drag = {id: event.pointerId, pointer, start, x: event.clientX, y: event.clientY, lastY: event.clientY,
      stamp: event.timeStamp, vy: 0, dy: 0, originPosition: start, parents: [nodes.surface.element.parentElement, nodes.dock.parentElement, nodes.full.parentElement, nodes.screen.parentElement, nodes.bar.parentElement, pointer.parentElement], nodes, position: start, moved: false};
    if (this.painted === nodes || this.painted?.surface === nodes.surface) {
      const g = this.drag, geometry = this.geometry;
      if (geometry && nodes.surface.element.dataset.videoGesture === this.token) {
        const sample = this.progress?.sample(performance.now()), position = clamp(sample?.position ?? this.paintPosition ?? start);
        const actual = frame(nodes.surface.element.getBoundingClientRect());
        const opacity = getComputedStyle(nodes.screen).opacity, barOpacity = getComputedStyle(nodes.bar).opacity;
        nodes.surface.freeze();this.stopJobs();this.settling = undefined;
        g.geometry = geometry;this.geometry = geometry;g.origin = actual;g.originPosition = position;g.position = position;g.moved = true;
        g.vy = (sample?.velocity ?? 0) * geometry.travel;
        nodes.screen.style.opacity = opacity;nodes.bar.style.opacity = barOpacity;
        if (!this.input.reduced()) nodes.screen.style.transform = `translateY(${position * geometry.travel}px)`;
      }
    }
    return true;
  };
  move = (event: VideoPointer) => {
    const g = this.drag;
    if (!g || g.id !== event.pointerId || event.timeStamp < g.stamp) return false;
    if (!this.leaseCurrent(g)) {this.cancel();return false;}
    const dx = event.clientX - g.x, dy = event.clientY - g.y;
    if (!g.moved) {
      if (Math.hypot(dx, dy) < 6) return false;
      if (Math.abs(dx) >= Math.abs(dy) || (g.start === 0 ? dy < 0 : dy > 0)) {this.drag = undefined;return false;}
      g.moved = true;this.input.state(true, true);
    }
    const dt = event.timeStamp - g.stamp;
    if (dt > 0) g.vy = (event.clientY - g.lastY) * 1000 / dt;
    g.dy = dy;g.lastY = event.clientY;g.stamp = event.timeStamp;
    this.refresh();return true;
  };
  /** Called after the original preview layout commits, never polled. */
  refresh = () => {
    const g = this.drag;if (!g?.moved) return;
    if (!this.leaseCurrent(g)) {this.cancel();return;}
    if (!g.geometry) {
      const geometry = videoGestureGeometry(frame(g.nodes.dock.getBoundingClientRect()), frame(g.nodes.full.getBoundingClientRect()));
      if (!geometry) return;
      g.geometry = geometry;this.geometry = geometry;
      const painted = frame(g.nodes.surface.element.getBoundingClientRect());
      this.stopJobs();g.nodes.surface.freeze();g.origin = painted;
      if (!this.saved) this.saved = {screen: g.nodes.screen.style.transform, opacity: g.nodes.screen.style.opacity,
        background: g.nodes.screen.style.background, bar: g.nodes.bar.style.opacity, inert: g.nodes.screen.inert, copies: g.nodes.copies.map(node => ({node, opacity: node.style.opacity, base: Number(getComputedStyle(node).opacity)}))};
      this.painted = g.nodes;g.nodes.surface.element.dataset.videoGesture = this.token;
      g.nodes.screen.inert = g.start === 1;
    }
    const position = videoGesturePosition(g.start, g.dy + (g.originPosition - g.start) * g.geometry.travel, g.geometry);if (position === undefined) return;
    g.position = position;
    const base = videoGestureFrame(g.geometry, g.originPosition)!, target = videoGestureFrame(g.geometry, position)!;
    const actual = Object.fromEntries(keys.map(key => [key, g.origin![key] + target[key] - base[key]])) as VideoFrame;
    this.paint(g.nodes, actual, position, g.geometry.travel);
  };
  private paint(nodes: Nodes, value: VideoFrame, position: number, travel: number) {
    const element = nodes.surface.element;
    if (element.dataset.videoGesture !== this.token) return;
    if (!this.input.reduced()) {element.style.width = `${value.width}px`;element.style.height = `${value.height}px`;
      element.style.transform = `translate(${value.left}px, ${value.top}px)`;}
    this.paintPosition = position;
    element.style.visibility = 'visible';
    if (!this.input.reduced()) nodes.screen.style.transform = `translateY(${position * travel}px)`;
    nodes.screen.style.background = 'var(--color-ink)';
    nodes.screen.style.opacity = String(1 - clamp(position / .8));
    nodes.bar.style.opacity = String(clamp((position - .55) / .45));
    this.saved?.copies.forEach(copy => {copy.node.style.opacity = String(copy.base * (1 - clamp(position / .45)));});
  }
  release = (event: VideoPointer, cancelled = false) => {
    const g = this.drag;if (!g || g.id !== event.pointerId) return this.refusedPointer === event.pointerId;
    if (event.timeStamp < g.stamp) return false;
    this.drag = undefined;
    if (!g.moved) return false;
    if (!this.leaseCurrent(g)) {this.refusedPointer = g.id;this.reset();return true;}
    if (!g.geometry || !g.origin) {this.reset();return false;}
    const velocity = event.timeStamp - g.stamp > 80 ? 0 : g.vy;
    const decision = cancelled ? {target: g.start, velocity: velocity / g.geometry.travel} : videoGestureRelease(g.start, g.position, velocity, g.geometry);
    if (!decision) {this.reset();return false;}
    this.settle(g, decision.target, decision.velocity);return true;
  };
  private settle(g: Drag, target: 0 | 1, velocity: number, limit?: number) {
    const nodes = g.nodes, geometry = g.geometry!, now = performance.now();
    const origin = frame(nodes.surface.element.getBoundingClientRect()), destination = videoGestureFrame(geometry, target)!;
    const reduced = this.input.reduced(), duration = Math.min(limit ?? Infinity, reduced ? 160 : springTiming(springs.player).durationMs);
    const deadline = this.settling?.drag === g ? this.settling.deadline : now + duration;
    this.settling = {drag: g, target, deadline};
    if (reduced) {this.settleQuiet(g, target, destination, duration);return;}
    this.stopJobs();const epoch = this.epoch;
    const physics = Object.fromEntries(keys.map(key => {
      const spring = new PlayerMorph(origin[key]);spring.track(origin[key], (geometry.dock[key] - geometry.full[key]) * velocity, now);spring.retarget(destination[key], now);
      return [key, spring];
    })) as Record<keyof VideoFrame, PlayerMorph>;
    const progress = new PlayerMorph(g.position);this.progress = progress;progress.track(g.position, velocity, now);progress.retarget(target, now);
    const count = 49;
    const positions = Array.from({length: count}, (_, i) => i === count - 1 ? target : progress.sample(now + duration * i / (count - 1)).position);
    const surfaceFrames = positions.map((_, i): Keyframe => {
      const time = now + duration * i / (count - 1);
      const value = Object.fromEntries(keys.map(key => [key, i === count - 1 ? destination[key] : physics[key].sample(time).position])) as VideoFrame;
      return {width: `${value.width}px`, height: `${value.height}px`, transform: `translate(${value.left}px, ${value.top}px)`};
    });
    const screenFrames = positions.map(p => ({transform: `translateY(${p * geometry.travel}px)`, opacity: 1 - clamp(p / .8)}));
    const barFrames = positions.map(p => ({opacity: clamp((p - .55) / .45)}));
    this.jobs = [nodes.surface.element.animate(surfaceFrames, {duration, easing: 'linear', fill: 'forwards'}),
      nodes.screen.animate(screenFrames, {duration, easing: 'linear', fill: 'forwards'}), nodes.bar.animate(barFrames, {duration, easing: 'linear', fill: 'forwards'}),
      ...(this.saved?.copies.map(copy => copy.node.animate(positions.map(p => ({opacity: copy.base * (1 - clamp(p / .45))})), {duration, easing: 'linear', fill: 'forwards'})) ?? [])];
    void Promise.all(this.jobs.map(job => job.finished)).then(() => this.finish(g, target, destination, epoch)).catch(() => {});
  }
  private finish(g: Drag, target: 0 | 1, destination: VideoFrame, epoch: number) {
    const nodes = g.nodes;
    if (epoch !== this.epoch) return;
    if (!this.leaseCurrent(g) || nodes.surface.element.dataset.videoGesture !== this.token) {this.cancel();return;}
    this.paint(nodes, destination, target, g.geometry!.travel);this.settling = undefined;
    if (target !== g.start && this.live && this.input.current()) {this.committedTarget = target;if (!this.input.commit(target === 0)) this.reset();}
    else this.reset();
  }
  /** Quiet motion retains current paint, fades out, changes real bounds at zero
   * opacity and fades in. No transform/spring keyframes or renewed deadline. */
  private settleQuiet(g: Drag, target: 0 | 1, destination: VideoFrame, duration: number) {
    const nodes = g.nodes, elements = [nodes.surface.element, nodes.screen, nodes.bar, ...nodes.copies];
    const opacity = elements.map(element => Number(getComputedStyle(element).opacity));
    this.stopJobs();this.progress = undefined;const epoch = this.epoch;
    const options = {duration: duration / 2, easing: 'linear', fill: 'forwards' as const};
    const out = elements.map((element, i) => element.animate([{opacity: opacity[i]}, {opacity: 0}], options));this.jobs = out;
    void Promise.all(out.map(job => job.finished)).then(async () => {
      if (epoch !== this.epoch || !this.leaseCurrent(g) || nodes.surface.element.dataset.videoGesture !== this.token) return;
      const element = nodes.surface.element;
      element.style.width = `${destination.width}px`;element.style.height = `${destination.height}px`;
      element.style.transform = `translate(${destination.left}px, ${destination.top}px)`;nodes.screen.style.transform = '';
      this.paintPosition = target;
      const values = [1, target === 0 ? 1 : 0, target === 1 ? 1 : 0, ...(this.saved?.copies.map(copy => target === 0 ? copy.base : 0) ?? [])];
      const enter = elements.map((element, i) => element.animate([{opacity: 0}, {opacity: values[i]}], options));this.jobs.push(...enter);
      await Promise.all(enter.map(job => job.finished));this.finish(g, target, destination, epoch);
    }).catch(() => {});
  }
  motionChanged() {
    const reduced = this.input.reduced();if (reduced === this.quiet) return;this.quiet = reduced;
    const held = this.settling;
    if (reduced && held && this.leaseCurrent(held.drag)) {
      held.drag.position = clamp(this.progress?.sample(performance.now()).position ?? this.paintPosition ?? held.drag.position);
      this.settle(held.drag, held.target, 0, Math.max(0, Math.min(160, held.deadline - performance.now())));
    }
  }
  /** Only the expected existing expansion commit can release settled paint. */
  committed(expanded: boolean) {
    if (this.committedTarget === undefined || expanded !== (this.committedTarget === 0)) return;
    const nodes = this.painted, now = this.input.nodes();
    if (!nodes || !now || !this.input.current() || now.surface.element !== nodes.surface.element || now.dock !== nodes.dock
      || now.full !== nodes.full || now.screen !== nodes.screen || now.bar !== nodes.bar) {this.cancel();return;}
    this.reset(false);now.screen.inert = !expanded;now.surface.resume();
  }
  private stopJobs() {this.epoch++;for (const job of this.jobs) job.cancel();this.jobs = [];}
  private reset(resume = true) {
    this.stopJobs();const nodes = this.painted, saved = this.saved;
    this.painted = undefined;this.saved = undefined;this.committedTarget = undefined;this.progress = undefined;this.geometry = undefined;this.settling = undefined;this.paintPosition = undefined;
    if (nodes?.surface.element.dataset.videoGesture === this.token) {
      delete nodes.surface.element.dataset.videoGesture;
      if (saved) {nodes.screen.style.transform = saved.screen;nodes.screen.style.opacity = saved.opacity;
        nodes.screen.style.background = saved.background;nodes.screen.inert = saved.inert;nodes.bar.style.opacity = saved.bar;saved.copies.forEach(copy => {copy.node.style.opacity = copy.opacity;});}
      if (resume && this.input.nodes()?.surface === nodes.surface) nodes.surface.resume();
    }
    this.input.state(false, false);
  }
  cancel = () => {
    const g = this.drag;this.drag = undefined;
    if (g?.moved) this.refusedPointer = g.id;
    if (g?.pointer.isConnected && g.pointer.hasPointerCapture(g.id)) g.pointer.releasePointerCapture(g.id);
    this.reset();
  };
  dispose = () => {if (!this.live) return;this.live = false;if (this.drag) this.refusedPointer = this.drag.id;this.cancel();};
}
