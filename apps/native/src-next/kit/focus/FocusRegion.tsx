import React, {createContext, forwardRef, useCallback, useContext, useLayoutEffect, useMemo, useRef, useSyncExternalStore} from 'react';
import {findNodeHandle, TVFocusGuideView, View, type FocusGuideMethods, type ViewProps} from 'react-native';
import {useTheme} from '../theme';
import {createFocusHandleResolver, createFocusRegistry, focusTestID, type FocusAddress, type FocusHost, type FocusRegionConfig, type FocusRegistry} from './registry';
import type {FocusCell} from './cell-navigation';
import {useHostRef} from './useHostRef';

const nativeHandle = createFocusHandleResolver((host: FocusHost) => findNodeHandle(host as View));

const RegistryContext = createContext<FocusRegistry | null>(null);
const RegionContext = createContext<string | undefined>(undefined);
const ItemContext = createContext<Readonly<{focusId: string; testID?: string; waiting?: boolean; cell?: FocusCell; entityId?: string}> | undefined>(undefined);
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

export const FocusRegion = forwardRef<View, FocusRegionProps>(function FocusRegion({children, onFocusChange, ...props}, ref) {
  const parent = useOptionalFocusRegistry();
  return parent ? <Region {...props} onFocusChange={onFocusChange} ref={ref}>{children}</Region>
    : <FocusProvider><Region {...props} onFocusChange={onFocusChange} ref={ref}>{children}</Region></FocusProvider>;
});
const Region = forwardRef<View, FocusRegionProps>(function Region({id, entry, entryRegion, parent: configuredParent, entryPolicy = 'memory', order, neighbors, screenStart, edges, grid, timeGrid, stops, orientation = 'free', hold = false, enabled = true, onFocusChange, children, testID, ...view}, ref) {
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

export type FocusTargetOptions<T extends FocusHost = View> = {focusId?: string; testID?: string; disabled?: boolean; ref?: React.ForwardedRef<T>};
/** Bind the actual native host. No remote-key listener or per-key app decision. */
export function useFocusTarget<T extends FocusHost = View>({focusId, testID, disabled, ref}: FocusTargetOptions<T>) {
  const registry = useOptionalFocusRegistry(), regionId = useContext(RegionContext), item = useContext(ItemContext);
  const childId = focusId ?? item?.focusId ?? testID, entityId = item?.entityId;
  const stableTestID = testID ?? item?.testID ?? (regionId && childId ? focusTestID(regionId, childId) : undefined);
  if (regionId && !childId && !disabled) throw new Error('A region child requires a stable focusId or testID from its caller.');
  const owner = useRef({}), cellKey = JSON.stringify(item?.cell);
  const cell = useMemo<FocusCell | undefined>(() => cellKey ? JSON.parse(cellKey) : undefined, [cellKey]);
  const committed = useRef({registry, regionId, childId, disabled, cell, entityId});
  const onHost = useCallback((host: T | null) => {
    const c = committed.current;
    if (c.registry && c.regionId && c.childId) c.registry.attach({regionId: c.regionId, childId: c.childId}, c.disabled ? null : host, owner.current, c.cell, c.entityId);
  }, []);
  const {native, ref: setRef} = useHostRef<T>(ref ?? null, onHost);
  const config = regionId ? registry?.config(regionId) : undefined;
  // Edge props below also point at neighbour regions from destinations(); a
  // neighbour host that attaches later must republish them.
  const neighbours = registry && regionId && childId && !config?.hold
    ? Object.values(registry.destinations({regionId, childId})).flatMap(target => 'regionId' in target ? [target.regionId] : []) : [];
  const dependencyKey = JSON.stringify([...new Set([...Object.values(config?.edges ?? {}).filter((id): id is string => !!id), ...neighbours])]);
  const dependencies = useMemo<readonly string[]>(() => JSON.parse(dependencyKey), [dependencyKey]);
  // Structure, edge hosts and (in grids) this cell's own focus: never another cell's focus move.
  const subscribe = useCallback((listener: () => void) => registry?.subscribeTarget(regionId, dependencies, listener, childId) ?? (() => {}), [childId, dependencies, regionId, registry]);
  const getSnapshot = useCallback(() => registry?.targetSnapshot(regionId, dependencies, childId) ?? 0, [childId, dependencies, regionId, registry]);
  useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
  useLayoutEffect(() => {
    committed.current = {registry, regionId, childId, disabled, cell, entityId};
    if (!registry || !regionId || !childId) return;
    const address = {regionId, childId};
    registry.attach(address, disabled ? null : native.current, owner.current, cell, entityId);
    const registrationOwner = owner.current;
    return () => {registry.attach(address, null, registrationOwner);};
  }, [cell, childId, disabled, entityId, native, regionId, registry]);
  const edgeProps: Pick<ViewProps, 'nextFocusUp' | 'nextFocusDown' | 'nextFocusLeft' | 'nextFocusRight'> = {};
  const order = regionId && (config?.orientation === 'horizontal' || config?.orientation === 'vertical') ? registry?.order(regionId) : undefined;
  if (registry && config?.edges && childId) {
    for (const direction of ['up', 'down', 'left', 'right'] as const) {
      const target = config.edges[direction];
      if (!target || config.hold) continue;
      if (config.orientation === 'horizontal' && (direction === 'left' && order?.[0] !== childId || direction === 'right' && order?.at(-1) !== childId)) continue;
      if (config.orientation === 'vertical' && (direction === 'up' && order?.[0] !== childId || direction === 'down' && order?.at(-1) !== childId)) continue;
      const host = registry.host(target);
      const handle = nativeHandle(host);
      if (handle != null) {
        const key = {up: 'nextFocusUp', down: 'nextFocusDown', left: 'nextFocusLeft', right: 'nextFocusRight'} as const;
        edgeProps[key[direction]] = handle;
      }
    }
  }
  const address: FocusAddress | undefined = regionId && childId ? {regionId, childId} : undefined;
  if (registry && address && !config?.hold) {
    for (const [direction, target] of Object.entries(registry.destinations(address))) {
      const host = 'regionId' in target ? registry.host(target.regionId) : registry.child({regionId: address.regionId, childId: target.childId});
      const handle = nativeHandle(host);
      if (handle != null) {
        const key = {up: 'nextFocusUp', down: 'nextFocusDown', left: 'nextFocusLeft', right: 'nextFocusRight'} as const;
        edgeProps[key[direction as keyof typeof key]] = handle;
      }
    }
  }
  return {ref: setRef, testID: stableTestID, waiting: item?.waiting ?? false, ...edgeProps,
    onFocus: () => {if (registry && address && !disabled) registry.didFocus(address);},
    onBlur: () => {if (registry && address) registry.didBlur(address);},
  };
}
