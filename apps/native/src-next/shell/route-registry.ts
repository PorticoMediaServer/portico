import type {ReactNode} from 'react';
import type {NativeRouteSnapshot, NativeShellRoute} from './routes';
import type {NativeShellRuntime} from './runtime';
import type {NativeShellBootstrapContext} from './bootstrap';
import {sameAuthority, type ShellDestination} from './model';

export type NativeRouteRenderer = Readonly<{
  render: (route: NativeShellRoute, runtime: NativeShellRuntime) => ReactNode;
  /** The module retains its real history views, native refs and visibility gates. */
  renderRetained?: (runtime: NativeShellRuntime) => ReactNode;
  renderOverlays?: (runtime: NativeShellRuntime) => ReactNode;
  scrollToTop?: (route: NativeShellRoute) => void;
  dispose?: () => void;
}>;
type RouteKind = NativeShellRoute['kind'];
export type NativeRouteFactory = (context: NativeShellBootstrapContext) => NativeRouteRenderer;
export type NativeWatchRenderer = Readonly<{render: (runtime: NativeShellRuntime) => ReactNode; renderRetained?: (runtime: NativeShellRuntime) => ReactNode; renderOverlays?: (runtime: NativeShellRuntime) => ReactNode; back?: () => boolean; dispose?: () => void}>;
export type NativeWatchFactory = (context: NativeShellBootstrapContext) => NativeWatchRenderer;
type RendererFactory = (context: NativeShellBootstrapContext) => NativeRouteRenderer | NativeWatchRenderer;
type PreparationLifetime = Readonly<{signal: AbortSignal; isCurrent: () => boolean}>;
export type NativeBoundRoutes = Readonly<{
  render: (route: NativeShellRoute, runtime: NativeShellRuntime) => ReactNode;
  /** Create the route's module (if admitted) without rendering it, so its retained pages exist. */
  prepare: (route: NativeShellRoute) => void;
  renderWatch: (runtime: NativeShellRuntime) => ReactNode;
  renderRetained: (runtime: NativeShellRuntime) => readonly ReactNode[] | null;
  renderOverlays: (runtime: NativeShellRuntime) => readonly ReactNode[] | null;
  scrollToTop: (route: NativeShellRoute) => void;
  back: () => boolean;
  dispose: () => void;
}>;

/** Screen modules register factories. Each restored viewer owns its created screens;
 * importing a module never captures credentials or creates a shared screen controller. */
export class NativeShellRouteRegistry {
  private renderers = new Map<RouteKind, NativeRouteRenderer>();
  private routes = new Map<RouteKind, NativeRouteFactory>();
  private destinations = new Map<ShellDestination['kind'], NativeRouteFactory>();
  private watch?: NativeWatchFactory;
  private listeners = new Set<() => void>();
  private preparers = new WeakMap<NativeShellBootstrapContext, (destination: ShellDestination, expected: NativeRouteSnapshot, lifetime: PreparationLifetime) => boolean>();
  /** Borrow a registered viewer-owned service factory without navigating or rendering. */
  prepareDestination(context: NativeShellBootstrapContext, destination: ShellDestination, expected: NativeRouteSnapshot, lifetime: PreparationLifetime): boolean {
    const original = Object.freeze({signal: lifetime.signal, isCurrent: lifetime.isCurrent});
    if (original.signal.aborted || !original.isCurrent()) return false;
    return this.preparers.get(context)?.(destination, expected, original) ?? false;
  }
  subscribe = (listener: () => void): (() => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  register(kind: RouteKind, renderer: NativeRouteRenderer): () => void {
    if (this.renderers.has(kind) || this.routes.has(kind)) throw new Error('native_route_already_registered');
    this.renderers.set(kind, renderer); this.changed();
    // Legacy objects belong to their registration, not to any one bound viewer.
    return () => {if (this.renderers.get(kind) === renderer) {
      this.renderers.delete(kind);
      try {renderer.dispose?.();} finally {this.changed();}
    }};
  }
  get(kind: RouteKind): NativeRouteRenderer | undefined {return this.renderers.get(kind);}
  /** The menu projects only destinations this build can actually render. */
  hasRoute(kind: RouteKind): boolean {return this.renderers.has(kind) || this.routes.has(kind);}
  hasDestination(kind: ShellDestination['kind']): boolean {return this.destinations.has(kind);}
  registerRoute(kind: RouteKind, create: NativeRouteFactory): () => void {
    if (this.renderers.has(kind) || this.routes.has(kind)) throw new Error('native_route_already_registered');
    return this.add(this.routes, kind, create);
  }
  registerDestination(kind: ShellDestination['kind'], create: NativeRouteFactory): () => void {
    return this.add(this.destinations, kind, create);
  }
  registerWatch(create: NativeWatchFactory): () => void {
    if (this.watch) throw new Error('native_watch_already_registered');
    this.watch = create; this.changed();
    return () => {if (this.watch === create) {this.watch = undefined; this.changed();}};
  }
  private add<K>(map: Map<K, NativeRouteFactory>, kind: K, create: NativeRouteFactory): () => void {
    if (map.has(kind)) throw new Error('native_route_already_registered');
    map.set(kind, create); this.changed();
    return () => {if (map.get(kind) === create) {map.delete(kind); this.changed();}};
  }
  bind(context: NativeShellBootstrapContext): NativeBoundRoutes {
    let alive = true;
    const boundAuthority = Object.freeze({...context.authority()});
    const instances = new Map<RendererFactory, NativeRouteRenderer | NativeWatchRenderer>();
    const initializing = new Set<RendererFactory>();
    const current = () => alive && context.current() && sameAuthority(boundAuthority, context.authority());
    const active = () => new Set<RendererFactory>([...this.routes.values(), ...this.destinations.values(), ...(this.watch ? [this.watch] : [])]);
    const release = (factory: RendererFactory) => {
      const instance = instances.get(factory); instances.delete(factory); instance?.dispose?.();
    };
    const unsubscribe = this.subscribe(() => {const registered = active(); for (const factory of instances.keys()) if (!registered.has(factory)) release(factory);});
    const instance = <T extends NativeRouteRenderer | NativeWatchRenderer>(factory: (context: NativeShellBootstrapContext) => T, callerCurrent?: () => boolean): T | undefined => {
      if (!current() || !active().has(factory) || callerCurrent?.() === false) return undefined;
      const value = instances.get(factory) as T | undefined;
      if (value) return value;
      if (initializing.has(factory)) return undefined;
      initializing.add(factory);
      try {
        const created = factory(context);
        if (!current() || !active().has(factory) || callerCurrent?.() === false) {created.dispose?.();return undefined;}
        instances.set(factory, created);return created;
      } finally {initializing.delete(factory);}
    };
    const renderer = (route: NativeShellRoute): NativeRouteRenderer | undefined => {
      const create = (route.kind === 'shell' ? this.destinations.get(route.destination.kind) : undefined) ?? this.routes.get(route.kind);
      return create ? instance(create) : this.get(route.kind);
    };
    const prepare = (destination: ShellDestination, expected: NativeRouteSnapshot, lifetime: PreparationLifetime): boolean => {
      const target = Object.freeze({...destination}), route: NativeShellRoute = Object.freeze({kind: 'shell', destination: target});
      const valid = () => current() && sameAuthority(boundAuthority, context.authority()) && sameAuthority(boundAuthority, expected.authority)
        && context.navigation.getSnapshot() === expected && !lifetime.signal.aborted && lifetime.isCurrent() && context.navigation.allows(route);
      if (!valid()) return false;
      const create = this.destinations.get(target.kind);
      if (!create || initializing.has(create)) return false;
      const value = instance(create, valid);
      return !!value && valid();
    };
    this.preparers.set(context, prepare);
    return Object.freeze({
      render: (route, runtime) => current() && context.navigation.allows(route) ? renderer(route)?.render(route, runtime) : null,
      prepare: route => {if (current() && context.navigation.allows(route)) renderer(route);},
      // Persistent media has its own current-fence guard; page admission does
      // not hide valid playback or skip its topmost panel's Back handling.
      renderWatch: runtime => current() && this.watch ? instance(this.watch)?.render(runtime) : null,
      renderRetained: runtime => current() ? [...instances.values()].map(value => value.renderRetained?.(runtime)) : null,
      // The Watch menu host is one authority-local instance shared with the watch
      // outlet. A cold Saved card's More needs that instance even when no title,
      // channel or watch route has rendered yet; the host itself renders nothing
      // without a current surface.
      renderOverlays: runtime => {
        if (!current()) return null;
        if (this.watch && context.session()) instance(this.watch);
        return [...instances.values()].map(value => value.renderOverlays?.(runtime));
      },
      back: () => current() && this.watch ? instance(this.watch)?.back?.() ?? false : false,
      scrollToTop: route => {if (current() && context.navigation.allows(route)) renderer(route)?.scrollToTop?.(route);},
      dispose: () => {if (!alive) return; alive = false;if (this.preparers.get(context) === prepare) this.preparers.delete(context); unsubscribe(); for (const factory of instances.keys()) release(factory);},
    } satisfies NativeBoundRoutes);
  }
  private changed() {for (const listener of [...this.listeners]) listener();}
}

export const nativeShellRoutes = new NativeShellRouteRegistry();
