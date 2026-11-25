import React, {createContext, forwardRef, useCallback, useContext, useLayoutEffect, useMemo, useRef, useSyncExternalStore} from 'react';
import {findNodeHandle, Platform, TVFocusGuideView, View, type FocusGuideMethods, type ViewProps} from 'react-native';
import {useTheme} from '../theme';
import {createFocusHandleResolver, createFocusRegistry, deferStartFocus, focusTestID, requestStartFocus, type FocusAddress, type FocusHost, type FocusRegionConfig, type FocusRegistry} from './registry';
import type {FocusCell} from './cell-navigation';
import {useHostRef} from './useHostRef';
import {useLayerCovered} from './CoveredLayer';

deferStartFocus(Platform?.OS === 'android');
const nativeHandle = createFocusHandleResolver((host: FocusHost) => findNodeHandle(host as View));
// R1: a declared edge is real on Android TV. Android drops a next-focus aimed at a region's TVFocusGuideView (the guide
// is often not focusable natively, e.g. after react-native-tvos's focus recovery), and plain geometry decides instead. So
// there an edge names the region's entry leaf, the same view the guide would hand focus to, with the guide as fallback.
// The control re-reads the leaf when that region changes or focus moves within it (its remembered child), so Right from
// the rail returns to where focus was on the page, as the guide's memory does. tvOS keeps the guide.
const edgesToLeaves = () => Platform?.OS === 'android' && !!Platform.isTV;

const RegistryContext = createContext<FocusRegistry | null>(null);
const RegionContext = createContext<string | undefined>(undefined);
const ItemContext = createContext<Readonly<{focusId: string; testID?: string; waiting?: boolean; cell?: FocusCell; entityId?: string}> | undefined>(undefined);
// Created after the other contexts: fixtures address contexts by creation order.
/** TV-FOCUS-REVIEW step 5: the nearest TVRow counts the focus targets inside it (kit/layout/TVRow). */
const ScrollRowContext = createContext<((change: 1 | -1) => void) | undefined>(undefined);
export function FocusScrollRowScope({count, children}: {count: (change: 1 | -1) => void; children: React.ReactNode}) {
  return <ScrollRowContext.Provider value={count}>{children}</ScrollRowContext.Provider>;
}
/** Hears focus arriving on any kit control inside it (and leaving it), through the same `useFocusTarget` every control
 * uses, with the control's own view: a TV title page folds its hero when focus enters the panel and keeps the focused
 * control on screen. An outer scope hears what an inner one does. */
type FocusWithinListener = (focused: boolean, host: FocusHost | null) => void;
const FocusWithinContext = createContext<FocusWithinListener | undefined>(undefined);
export function FocusWithin({onChange, children}: {onChange: FocusWithinListener; children: React.ReactNode}) {
  const parent = useContext(FocusWithinContext);
  const notify = useCallback<FocusWithinListener>((focused, host) => {onChange(focused, host); parent?.(focused, host);}, [onChange, parent]);
  return <FocusWithinContext.Provider value={notify}>{children}</FocusWithinContext.Provider>;
}
/**
 * R1 start focus (tv-focus "START" marks). A TV frame names the control its page starts on (the first Continue
 * Watching card on Home, Play or Resume on a title, the first row of a list, sheet or panel, Cancel in a confirmation)
 * by that control's `focusId` or `testID`, and the frame wraps its content in this scope. The control then asks for
 * focus through the same preferred ask every kit control already has (`useFocusTarget`), so Android TV and tvOS place
 * it the same way and there is one mechanism. The rail is outside every frame, so a page never starts in it.
 * Phones and tablets ignore it.
 */
const StartFocusContext = createContext<string | undefined>(undefined);
export function StartFocusScope({id, children}: {id?: string; children: React.ReactNode}) {
  const {isTV} = useTheme(), outer = useContext(StartFocusContext);
  // A frame inside a frame (a sheet over a page) names its own start; one that names none keeps the page's.
  const value = isTV ? id ?? outer : undefined;
  return value === outer ? <>{children}</> : <StartFocusContext.Provider value={value}>{children}</StartFocusContext.Provider>;
}
/** Whether the control with these ids is its frame's start. */
export function useIsStartFocus(childId: string | undefined, testID: string | undefined): boolean {
  const start = useContext(StartFocusContext);
  return start !== undefined && (start === childId || start === testID);
}
export function FocusProvider({registry, children}: {registry?: FocusRegistry; children: React.ReactNode}) {
  const parent = useContext(RegistryContext), own = useRef<FocusRegistry | undefined>(undefined);
  if (!own.current) own.current = createFocusRegistry();
  return <RegistryContext.Provider value={registry ?? parent ?? own.current}>{children}</RegistryContext.Provider>;
}
export function useOptionalFocusRegistry() {return useContext(RegistryContext);}
export function useFocusRegistry(): FocusRegistry {
  const registry = useOptionalFocusRegistry();
  if (!registry) throw new Error('A FocusProvider must contain regions that share focus.');
  return registry;
}
export function FocusItem({focusId, testID, waiting, cell, entityId, children}: {focusId: string; testID?: string; waiting?: boolean; cell?: FocusCell; entityId?: string; children: React.ReactNode}) {
  return <ItemContext.Provider value={{focusId, testID, waiting, cell, entityId}}>{children}</ItemContext.Provider>;
}
export function useFocusWaiting(): boolean {return useContext(ItemContext)?.waiting ?? false;}
export type FocusRegionProps = Omit<ViewProps, 'id' | 'onFocus' | 'onBlur'> & FocusRegionConfig & {onFocusChange?: (ownsFocus: boolean) => void};
type Guide = View & FocusGuideMethods;

const noopUnsubscribe = () => {};
const noopSubscribe = () => noopUnsubscribe;
const zeroSnapshot = () => 0;
const emptyDeps: readonly string[] = [];

export const FocusRegion = forwardRef<View, FocusRegionProps>(function FocusRegion({children, onFocusChange, ...props}, ref) {
  const {isTV} = useTheme();
  const parent = useOptionalFocusRegistry();
  if (!isTV) {
    const {id, entry: _entry, entryRegion: _entryRegion, parent: _parent, entryPolicy: _entryPolicy, order: _order,
      neighbors: _neighbors, screenStart: _screenStart, edges: _edges, grid: _grid, timeGrid: _timeGrid,
      stops: _stops, orientation: _orientation, hold: _hold, enabled: _enabled, testID, ...view} = props;
    return <View {...view} testID={testID} ref={ref}><RegionContext.Provider value={id}>{children}</RegionContext.Provider></View>;
  }
  return parent ? <Region {...props} onFocusChange={onFocusChange} ref={ref}>{children}</Region>
    : <FocusProvider><Region {...props} onFocusChange={onFocusChange} ref={ref}>{children}</Region></FocusProvider>;
});
const Region = forwardRef<View, FocusRegionProps>(function Region({id, entry, entryRegion, parent: configuredParent, entryPolicy = 'memory', order, neighbors, screenStart, edges, grid, timeGrid, stops, orientation = 'free', hold = false, enabled: declared = true, onFocusChange, children, testID, ...view}, ref) {
  // A region in a layer a route or the player covers takes no focus either (nor do its controls: `Focusable`, `CardFrame`).
  const layerCovered = useLayerCovered(), enabled = declared && !layerCovered;
  const parent = useContext(RegionContext);
  const registry = useFocusRegistry(), {isTV} = useTheme(), owner = useRef({});
  const onHost = useCallback((host: Guide | null) => {registry.attachRegion(id, host, owner.current);}, [id, registry]);
  const {native, ref: setRef} = useHostRef<View, Guide>(ref, onHost);
  const subscribe = useCallback((listener: () => void) => registry.subscribeRegion(id, listener), [id, registry]);
  const getSnapshot = useCallback(() => registry.regionSnapshot(id), [id, registry]);
  const revision = useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
  const sentEnabled = useRef(enabled);
  const sentDestination = useRef<Readonly<{host: Guide; handle: number | null}> | undefined>(undefined);
  const config = {id, entry, entryRegion, parent: configuredParent ?? parent, entryPolicy, order, neighbors, screenStart, edges, grid, timeGrid, stops, orientation, hold, enabled};
  useLayoutEffect(() => {registry.configure(config);});
  useLayoutEffect(() => {
    const registrationOwner = owner.current;
    const release = registry.mountRegion(id, registrationOwner);
    registry.attachRegion(id, native.current, registrationOwner);
    return () => {registry.attachRegion(id, null, registrationOwner); release();};
  }, [id, native, registry]);
  if (isTV && !id.trim()) throw new Error('A TV focus region requires a stable caller identity.');
  const ownsFocus = registry.isRegionActive(id);
  useLayoutEffect(() => {onFocusChange?.(ownsFocus);}, [ownsFocus, onFocusChange]);
  useLayoutEffect(() => {
    if (!isTV) return;
    const target = registry.entryTarget(id);
    // Imperative destination API accepts real host handles (installed RN TV types).
    const handle = nativeHandle(target), host = native.current;
    // TVFocusGuideView clears its destinations when `focusable` goes false and never restores
    // them, so a region that was disabled (a retained, hidden page) must send them again.
    if (sentEnabled.current !== enabled) {sentEnabled.current = enabled; sentDestination.current = undefined;}
    if (!host || sentDestination.current?.host === host && sentDestination.current.handle === handle) return;
    sentDestination.current = {host, handle};
    host.setDestinations(handle == null ? [] : [handle]);
  }, [enabled, id, isTV, native, registry, revision]);
  const body = <RegionContext.Provider value={id}>{children}</RegionContext.Provider>;
  return isTV ? <TVFocusGuideView {...view} testID={testID ?? `portico.region.${encodeURIComponent(id)}`} ref={setRef} autoFocus
    focusable={enabled} trapFocusUp={hold || stops?.up || orientation === 'vertical' && !edges?.up}
    trapFocusDown={hold || stops?.down || orientation === 'vertical' && !edges?.down}
    trapFocusLeft={hold || stops?.left} trapFocusRight={hold || stops?.right || orientation === 'horizontal' && !edges?.right}>{body}</TVFocusGuideView>
    : <View {...view} testID={testID} ref={setRef}>{body}</View>;
});

/** The edges a region inherits from the regions around it, for the directions that leave through their side: a
 * horizontal parent passes on Up and Down, a vertical parent Left and Right. A free, held or stopped parent ends it. */
function inheritedEdges(registry: FocusRegistry, regionId: string): Partial<Record<'up' | 'down' | 'left' | 'right', string>> {
  const out: Partial<Record<'up' | 'down' | 'left' | 'right', string>> = {}, seen = new Set<string>([regionId]);
  for (const direction of ['up', 'down', 'left', 'right'] as const) {
    seen.clear(); seen.add(regionId);
    for (let parent = registry.config(regionId)?.parent; parent && !seen.has(parent); parent = registry.config(parent)?.parent) {
      seen.add(parent);
      const config = registry.config(parent);
      const across = config?.orientation === 'horizontal' ? direction === 'up' || direction === 'down' : config?.orientation === 'vertical' ? direction === 'left' || direction === 'right' : false;
      if (!config || config.hold || config.stops?.[direction] || !across) break;
      if (config.edges?.[direction]) {out[direction] = config.edges[direction]; break;}
    }
  }
  return out;
}
export type FocusTargetOptions<T extends FocusHost = View> = {focusId?: string; testID?: string; disabled?: boolean; ref?: React.ForwardedRef<T>;
  /** TV-FOCUS-REVIEW step 4: ask for native focus once when this becomes true, the same on Android TV and tvOS. It
   * replaces `hasTVPreferredFocus`, which tvOS re-applies on every later layout change and so takes focus back
   * (§4.4.2). A caller passes false while the control cannot answer (covered, disabled); asking again needs the
   * caller to turn it off and on, so a page returned to by Back never takes focus from the stack's restore. */
  preferred?: boolean;
  /** While true the preferred ask waits (the control is covered or not interactive yet). */
  preferredBlocked?: boolean};
/** Bind the actual native host. No remote-key listener or per-key app decision. */
export function useFocusTarget<T extends FocusHost = View>({focusId, testID, disabled, ref, preferred = false, preferredBlocked = false}: FocusTargetOptions<T>) {
  const {isTV} = useTheme();
  const registry = useOptionalFocusRegistry(), regionId = useContext(RegionContext), item = useContext(ItemContext);
  const childId = focusId ?? item?.focusId ?? testID, entityId = item?.entityId;
  const stableTestID = testID ?? item?.testID ?? (regionId && childId ? focusTestID(regionId, childId) : undefined);
  if (isTV && regionId && !childId && !disabled) throw new Error('A region child requires a stable focusId or testID from its caller.');
  const activeRegistry = isTV ? registry : null;
  const owner = useRef({}), cellKey = activeRegistry && item?.cell ? JSON.stringify(item.cell) : undefined;
  const cell = useMemo<FocusCell | undefined>(() => cellKey ? JSON.parse(cellKey) : undefined, [cellKey]);
  const committed = useRef({registry: activeRegistry, regionId, childId, disabled, cell, entityId});
  const onHost = useCallback((host: T | null) => {
    const c = committed.current;
    if (c.registry && c.regionId && c.childId) c.registry.attach({regionId: c.regionId, childId: c.childId}, c.disabled ? null : host, owner.current, c.cell, c.entityId);
  }, []);
  const {native, ref: setRef} = useHostRef<T>(ref ?? null, onHost);
  // A live focus stop inside a TVRow lets that row scroll (and so be focusable) on Android TV.
  const scrollRow = useContext(ScrollRowContext), within = useContext(FocusWithinContext);
  useLayoutEffect(() => {
    if (!isTV || !scrollRow || disabled) return;
    scrollRow(1);
    return () => scrollRow(-1);
  }, [disabled, isTV, scrollRow]);
  const asked = useRef(false);
  // R1: the frame's start focus (StartFocusScope) is a preferred ask like any other.
  const start = useIsStartFocus(childId, stableTestID), wanted = isTV && (preferred || start);
  useLayoutEffect(() => {
    if (!wanted) {asked.current = false; return;}
    if (asked.current || disabled || preferredBlocked) return;
    asked.current = true;
    if (native.current) activeRegistry?.notePreferred?.(native.current);
    requestStartFocus(native.current);
  }, [activeRegistry, disabled, native, preferredBlocked, wanted]);
  const config = activeRegistry && regionId ? activeRegistry.config(regionId) : undefined;
  const inherited = activeRegistry && regionId ? inheritedEdges(activeRegistry, regionId) : undefined;
  // Edge props below also point at neighbour regions from destinations(); a
  // neighbour host that attaches later must republish them.
  const neighbours = activeRegistry && regionId && childId && !config?.hold
    ? Object.values(activeRegistry.destinations({regionId, childId})).flatMap(target => 'regionId' in target ? [target.regionId] : []) : undefined;
  const dependencyKey = activeRegistry && regionId
    ? JSON.stringify([...new Set([...Object.values({...inherited, ...config?.edges}).filter((id): id is string => !!id), ...(neighbours ?? [])])])
    : '';
  const dependencies = useMemo<readonly string[]>(() => dependencyKey ? JSON.parse(dependencyKey) : emptyDeps, [dependencyKey]);
  // Structure, edge hosts and (in grids) this cell's own focus: never another cell's focus move.
  const leaves = isTV && edgesToLeaves();
  const subscribe = useCallback((listener: () => void) => activeRegistry?.subscribeTarget(regionId, dependencies, listener, childId, leaves) ?? noopUnsubscribe, [activeRegistry, childId, dependencies, leaves, regionId]);
  const getSnapshot = useCallback(() => activeRegistry?.targetSnapshot(regionId, dependencies, childId, leaves) ?? 0, [activeRegistry, childId, dependencies, leaves, regionId]);
  useSyncExternalStore(activeRegistry ? subscribe : noopSubscribe, activeRegistry ? getSnapshot : zeroSnapshot, activeRegistry ? getSnapshot : zeroSnapshot);
  useLayoutEffect(() => {
    committed.current = {registry: activeRegistry, regionId, childId, disabled, cell, entityId};
    if (!activeRegistry || !regionId || !childId) return;
    const address = {regionId, childId};
    activeRegistry.attach(address, disabled ? null : native.current, owner.current, cell, entityId);
    const registrationOwner = owner.current;
    return () => {activeRegistry.attach(address, null, registrationOwner);};
  }, [activeRegistry, cell, childId, disabled, entityId, native, regionId]);
  if (!activeRegistry) {
    return {
      ref: setRef, testID: stableTestID, waiting: item?.waiting ?? false,
      onFocus: () => {within?.(true, native.current);},
      onBlur: () => {within?.(false, native.current);},
    };
  }
  const regionTarget = (id: string) => (leaves ? activeRegistry.entryTarget(id) : undefined) ?? activeRegistry.host(id);
  const edgeProps: Pick<ViewProps, 'nextFocusUp' | 'nextFocusDown' | 'nextFocusLeft' | 'nextFocusRight'> = {};
  const order = regionId && (config?.orientation === 'horizontal' || config?.orientation === 'vertical') ? activeRegistry.order(regionId) : undefined;
  if (config && childId) {
    for (const direction of ['up', 'down', 'left', 'right'] as const) {
      // A region without its own edge in this direction takes its parent's when it leaves through the parent's side
      // (a choice matrix inside Settings rows: Left goes to the page column, never by geometry to search, D11).
      const own = config.edges?.[direction], target = own ?? (config.orientation === 'free' || config.stops?.[direction] ? undefined : inherited?.[direction]);
      if (!target || config.hold) continue;
      if (config.orientation === 'horizontal' && (direction === 'left' && order?.[0] !== childId || direction === 'right' && order?.at(-1) !== childId)) continue;
      if (config.orientation === 'vertical' && (direction === 'up' && order?.[0] !== childId || direction === 'down' && order?.at(-1) !== childId)) continue;
      const handle = nativeHandle(regionTarget(target));
      if (handle != null) {
        const key = {up: 'nextFocusUp', down: 'nextFocusDown', left: 'nextFocusLeft', right: 'nextFocusRight'} as const;
        edgeProps[key[direction]] = handle;
      }
    }
  }
  const address: FocusAddress | undefined = regionId && childId ? {regionId, childId} : undefined;
  if (address && !config?.hold) {
    for (const [direction, target] of Object.entries(activeRegistry.destinations(address))) {
      const host = 'regionId' in target ? regionTarget(target.regionId) : activeRegistry.child({regionId: address.regionId, childId: target.childId});
      const handle = nativeHandle(host);
      if (handle != null) {
        const key = {up: 'nextFocusUp', down: 'nextFocusDown', left: 'nextFocusLeft', right: 'nextFocusRight'} as const;
        edgeProps[key[direction as keyof typeof key]] = handle;
      }
    }
  }
  return {ref: setRef, testID: stableTestID, waiting: item?.waiting ?? false, ...edgeProps,
    onFocus: () => {if (address && !disabled) activeRegistry.didFocus(address); within?.(true, native.current);},
    onBlur: () => {if (address) activeRegistry.didBlur(address); within?.(false, native.current);},
  };
}
