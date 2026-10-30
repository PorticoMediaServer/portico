import {createCellNavigator, cellFocusMemory, type FocusCell, type CellNavigation} from './cell-navigation';
/** Explicit lifecycle restoration only. Native guides perform directional traversal. */
export type FocusAddress = Readonly<{regionId: string; childId: string}>;
export type FocusHost = {requestTVFocus?: () => void; focus?: () => void};
export type FocusEdges = Readonly<Partial<Record<'up' | 'down' | 'left' | 'right', string>>>;
export type FocusRegionConfig = Readonly<{
  id: string; entry?: string; entryRegion?: string; parent?: string; entryPolicy?: 'memory' | 'entry'; order?: readonly string[];
  neighbors?: Readonly<{next?: string; previous?: string}>; screenStart?: FocusAddress;
  grid?: CellNavigation['grid']; timeGrid?: CellNavigation['timeGrid'];
  edges?: FocusEdges; stops?: Readonly<Partial<Record<'up' | 'down' | 'left' | 'right', boolean>>>; orientation?: 'horizontal' | 'vertical' | 'free'; hold?: boolean; enabled?: boolean;
}>;
export type FocusSnapshot = FocusAddress & Readonly<{
  order: readonly string[]; cell?: FocusCell; neighbors?: FocusRegionConfig['neighbors']; screenStart?: FocusAddress; returnTo?: FocusSnapshot;
}>;
const idPart = (value: string) => encodeURIComponent(value).replaceAll('.', '%2E');
export const focusTestID = (regionId: string, childId: string): string => `portico.focus.${idPart(regionId)}.${idPart(childId)}`;
const same = (a: FocusAddress | undefined, b: FocusAddress | undefined) => !!a && !!b && a.regionId === b.regionId && a.childId === b.childId;
/** The TV fork exposes its native View command on phones too; TextInput retains focus(). */
export function requestNativeFocus(host: FocusHost | null | undefined): boolean {
  if (host?.requestTVFocus) {host.requestTVFocus(); return true;}
  if (host?.focus) {host.focus(); return true;}
  return false;
}
/** Native host identity owns its tag. Retry unresolved hosts after attachment. */
export function createFocusHandleResolver(resolve: (host: FocusHost) => number | null) {
  const handles = new WeakMap<FocusHost, number>();
  return (host: FocusHost | undefined): number | null => {
    if (!host) return null;
    const cached = handles.get(host);
    if (cached !== undefined) return cached;
    const handle = resolve(host);
    if (handle !== null) handles.set(host, handle);
    return handle;
  };
}
const configKey = (c: FocusRegionConfig) => JSON.stringify([c.entry, c.entryRegion, c.parent, c.entryPolicy, c.order, c.neighbors?.next, c.neighbors?.previous,
  c.screenStart?.regionId, c.screenStart?.childId, c.edges?.up, c.edges?.down, c.edges?.left, c.edges?.right, c.grid, c.timeGrid, c.stops, c.orientation, c.hold, c.enabled]);

export function createFocusRegistry() {
  type Child = {host: FocusHost; owner?: object; cell?: FocusCell; entityId?: string};
  type Region = {config: FocusRegionConfig; host?: FocusHost; hostOwner?: object; owner?: object; children: Map<string, Child>; entities: Map<string, string>; remembered?: string; descendant?: FocusAddress; cellMemory?: Readonly<{column?: number; time?: number}>; navigate?: ReturnType<typeof createCellNavigator>; cellOrder?: readonly string[]; resetCellFocus?: string; returns: Map<object, FocusSnapshot | undefined>};
  const regions = new Map<string, Region>(), listeners = new Set<() => void>();
  const scopedListeners = new Map<string, Set<() => void>>(), scopedRevisions = new Map<string, number>();
  // Focus targets read only structure (config, order, attached hosts) and, in grid and time-grid
  // regions, the cell memory a focus move updates. A plain focus move must not re-render every
  // target of the region and its ancestors (P2-71-PERF): targets get their own revision.
  const targetListeners = new Map<string, Set<() => void>>(), targetRevisions = new Map<string, number>();
  // In a grid the cell memory a move updates changes edges, but only the focused cell's edges are
  // ever read (the native engine reads them from the focused view): it alone is told.
  const childListeners = new Map<string, Set<() => void>>(), childRevisions = new Map<string, number>();
  const childKey = (regionId: string, childId: string) => `${regionId}\u0000${childId}`;
  const hostListeners = new Map<string, Set<() => void>>(), hostRevisions = new Map<string, number>();
  const entryDependents = new Map<string, Set<string>>();
  const retained = new Map<object, FocusSnapshot[]>(), memory = new Map<string, string>();
  let active: FocusSnapshot | undefined, lastFocus: FocusAddress | undefined, revision = 0, epoch = 0, recoveredEpoch = -1;
  const emit = (ids: readonly (string | undefined)[], hostId?: string, kind: 'structure' | 'focus' = 'structure', focused?: FocusAddress) => {
    revision++;
    const affected = new Set<string>();
    const visit = (id: string | undefined) => {
      if (!id || affected.has(id)) return;
      affected.add(id);
      visit(regions.get(id)?.config.parent);
      for (const dependent of entryDependents.get(id) ?? []) visit(dependent);
    };
    ids.forEach(visit);
    const notify = new Set(listeners);
    for (const id of affected) {
      if (regions.has(id) || scopedListeners.has(id)) scopedRevisions.set(id, revision);
      for (const listener of scopedListeners.get(id) ?? []) notify.add(listener);
      const config = regions.get(id)?.config;
      if (kind === 'structure') {
        if (regions.has(id) || targetListeners.has(id)) targetRevisions.set(id, revision);
        for (const listener of targetListeners.get(id) ?? []) notify.add(listener);
      } else if (focused?.regionId === id && (config?.grid || config?.timeGrid)) {
        const key = childKey(id, focused.childId);
        childRevisions.set(key, revision);
        for (const listener of childListeners.get(key) ?? []) notify.add(listener);
      }
    }
    if (hostId !== undefined) {
      hostRevisions.set(hostId, revision);
      for (const listener of hostListeners.get(hostId) ?? []) notify.add(listener);
    }
    for (const listener of notify) listener();
  };
  const subscribeRegion = (id: string, listener: () => void) => {
    let callbacks = scopedListeners.get(id);
    if (!callbacks) {callbacks = new Set(); scopedListeners.set(id, callbacks);}
    callbacks.add(listener);
    return () => {
      callbacks!.delete(listener);
      if (!callbacks!.size) {scopedListeners.delete(id); if (!regions.has(id)) scopedRevisions.delete(id);}
    };
  };
  const subscribeTargetRegion = (id: string, listener: () => void) => {
    let callbacks = targetListeners.get(id);
    if (!callbacks) {callbacks = new Set(); targetListeners.set(id, callbacks);}
    callbacks.add(listener);
    return () => {
      callbacks!.delete(listener);
      if (!callbacks!.size) {targetListeners.delete(id); if (!regions.has(id)) targetRevisions.delete(id);}
    };
  };
  const subscribeChild = (key: string, listener: () => void) => {
    let callbacks = childListeners.get(key);
    if (!callbacks) {callbacks = new Set(); childListeners.set(key, callbacks);}
    callbacks.add(listener);
    return () => {callbacks!.delete(listener); if (!callbacks!.size) {childListeners.delete(key); childRevisions.delete(key);}};
  };
  const subscribeHost = (id: string, listener: () => void) => {
    let callbacks = hostListeners.get(id);
    if (!callbacks) {callbacks = new Set(); hostListeners.set(id, callbacks);}
    callbacks.add(listener);
    return () => {
      callbacks!.delete(listener);
      if (!callbacks!.size) {hostListeners.delete(id); if (!regions.has(id)) hostRevisions.delete(id);}
    };
  };
  const removeEntryDependency = (id: string, entry: string | undefined) => {
    if (!entry) return;
    const dependents = entryDependents.get(entry);
    dependents?.delete(id);
    if (!dependents?.size) entryDependents.delete(entry);
  };
  const isRetained = (id: string) => [...retained.values()].some(chain => chain.some(saved => saved.regionId === id));
  const prune = (id: string) => {
    const r = regions.get(id);
    if (!r || r.owner || r.host || r.children.size || r.returns.size || active?.regionId === id || isRetained(id)) return;
    if (r.remembered) {
      memory.delete(id); memory.set(id, r.remembered);
      if (memory.size > 64) memory.delete(memory.keys().next().value!);
    }
    regions.delete(id);
    removeEntryDependency(id, r.config.entryRegion);
    emit([id, r.config.parent]);
    if (!scopedListeners.has(id)) scopedRevisions.delete(id);
    if (!targetListeners.has(id)) targetRevisions.delete(id);
    if (!hostListeners.has(id)) hostRevisions.delete(id);
  };
  const region = (id: string): Region => {
    let value = regions.get(id);
    if (!value) {value = {config: {id}, children: new Map(), entities: new Map(), returns: new Map(), remembered: memory.get(id)}; memory.delete(id); regions.set(id, value);}
    return value;
  };
  const order = (r: Region) => r.config.order ?? (r.config.grid || r.config.timeGrid ? r.cellOrder ??= [...r.children].sort(([, a], [, b]) =>
    r.config.grid ? (a.cell?.index ?? Infinity) - (b.cell?.index ?? Infinity)
      : (a.cell?.row ?? Infinity) - (b.cell?.row ?? Infinity) || (a.cell?.kind === 'channel' ? -Infinity : a.cell?.start ?? Infinity) - (b.cell?.kind === 'channel' ? -Infinity : b.cell?.start ?? Infinity)).map(([id]) => id) : [...r.children.keys()]);
  const cellOf = (r: Region, id: string): FocusCell | undefined => {
    const cell = r.children.get(id)?.cell; if (cell) return cell;
    const index = r.config.grid ? r.config.order?.indexOf(id) : undefined;
    return index !== undefined && index >= 0 ? {index} : undefined;
  };
  const snapshot = (address: FocusAddress): FocusSnapshot => {
    const r = region(address.regionId);
    return {...address, order: [...order(r)], ...(cellOf(r, address.childId) ? {cell: cellOf(r, address.childId)} : {}), neighbors: r.config.neighbors, screenStart: r.config.screenStart, returnTo: [...r.returns.values()].at(-1)};
  };
  const currentSnapshot = (saved: FocusSnapshot) => {
    const r = regions.get(saved.regionId);
    // Once removed, keep its last committed relative order. Saved overlay openers are immutable.
    return r && order(r).includes(saved.childId) ? snapshot(saved) : saved;
  };
  const focus = (address: FocusAddress, options?: Readonly<{resetCellMemory?: boolean}>): boolean => {
    const r = regions.get(address.regionId), child = r?.children.get(address.childId);
    if (r?.config.enabled === false || !child || !child.host.requestTVFocus && !child.host.focus) return false;
    if (options?.resetCellMemory && r) {
      r.cellMemory = cellFocusMemory(cellOf(r, address.childId), undefined, r.config, {}); r.resetCellFocus = address.childId; r.navigate = undefined;
    }
    return requestNativeFocus(child.host);
  };
  const belongsTo = (child: string, ancestor: string): boolean => {
    const visited = new Set<string>();
    for (let id: string | undefined = child; id && !visited.has(id); id = regions.get(id)?.config.parent) {
      if (id === ancestor) return true;
      visited.add(id);
    }
    return false;
  };
  const enabledPath = (id: string): boolean => {
    const seen = new Set<string>();
    for (let current: string | undefined = id; current; current = regions.get(current)?.config.parent) {
      if (seen.has(current)) return false;
      seen.add(current);
      const r = regions.get(current);
      if (!r || r.config.enabled === false) return false;
    }
    return true;
  };
  /** Compound regions remember the actual descendant; native guides still perform every move. */
  const resolveEntry = (id: string, visited: Set<string>, allowGuide: boolean): Readonly<{host: FocusHost; address?: FocusAddress}> | undefined => {
    if (visited.has(id)) return;
    visited.add(id);
    const r = regions.get(id);
    if (!r || r.config.enabled === false) return;
    if (r.config.entryPolicy !== 'entry' && r.descendant) {
      const childRegion = regions.get(r.descendant.regionId);
      if (childRegion?.config.enabled !== false && belongsTo(r.descendant.regionId, id) && (allowGuide || enabledPath(r.descendant.regionId))) {
        const host = childRegion?.children.get(r.descendant.childId)?.host;
        if (host) return {host, address: r.descendant};
      }
    }
    if (allowGuide || enabledPath(id)) {
      const first = r.config.entryPolicy === 'entry' ? r.config.entry : r.remembered ?? r.config.entry;
      for (const childId of [first, r.config.entry, ...order(r)]) {
        if (!childId) continue;
        const host = r.children.get(childId)?.host;
        if (host) return {host, address: {regionId: id, childId}};
      }
    }
    const nested = r.config.entryRegion && regions.get(r.config.entryRegion);
    if (!nested || nested.config.enabled === false) return;
    return resolveEntry(nested.config.id, visited, allowGuide) ?? (allowGuide && nested.host ? {host: nested.host} : undefined);
  };
  const entryTarget = (id: string, visited = new Set<string>()): FocusHost | undefined => resolveEntry(id, visited, true)?.host;
  const entryAddress = (id: string): FocusAddress | undefined => resolveEntry(id, new Set(), false)?.address;
  const focusRegion = (id: string): boolean => {
    const r = regions.get(id);
    return r?.config.enabled !== false && (requestNativeFocus(entryTarget(id)) || requestNativeFocus(r?.host));
  };
  const restore = (saved: FocusSnapshot | undefined, visited = new Set<string>()): boolean => {
    if (!saved || visited.has(saved.regionId)) return false;
    visited.add(saved.regionId);
    if (focus(saved)) return true;
    const currentRegion = regions.get(saved.regionId);
    if (saved.cell?.pending && saved.cell.index !== undefined && currentRegion?.config.grid) {
      const replacement = [...currentRegion.children.keys()].find(id => {const cell = cellOf(currentRegion, id);
        return cell !== undefined && cell.index === saved.cell!.index && !cell.pending
          && (saved.cell!.generation === undefined || cell.generation === saved.cell!.generation);});
      if (replacement && focus({regionId: saved.regionId, childId: replacement})) return true;
    }
    if (saved.cell?.kind === 'channel' && currentRegion?.config.timeGrid) {
      const replacement = [...currentRegion.children].find(([, child]) => child.cell?.row === saved.cell?.row && child.cell?.kind === 'channel');
      if (replacement && focus({regionId: saved.regionId, childId: replacement[0]})) return true;
    }
    if (saved.cell?.kind === 'programme' && currentRegion?.config.timeGrid) {
      const time = currentRegion.cellMemory?.time ?? saved.cell.start ?? currentRegion.config.timeGrid.initialTime;
      const row = [...currentRegion.children].filter(([, child]) => child.cell?.row === saved.cell?.row && child.cell?.kind === 'programme')
        .sort(([, a], [, b]) => a.cell!.start! - b.cell!.start!);
      const replacement = row.find(([, child]) => child.cell!.start! <= time && child.cell!.end! > time)
        ?? row.find(([, child]) => child.cell!.end! > time) ?? row.at(-1);
      if (replacement && focus({regionId: saved.regionId, childId: replacement[0]})) return true;
    }
    const at = saved.order.indexOf(saved.childId);
    for (const childId of [...saved.order.slice(at + 1), ...saved.order.slice(0, Math.max(0, at)).reverse()]) {
      if (focus({regionId: saved.regionId, childId})) return true;
    }
    const r = regions.get(saved.regionId);
    if (r?.children.size && focusRegion(saved.regionId)) return true;
    const neighbors = r?.config.neighbors ?? saved.neighbors;
    if (neighbors?.next && focusRegion(neighbors.next)) return true;
    if (neighbors?.previous && focusRegion(neighbors.previous)) return true;
    if (saved.returnTo && restore(saved.returnTo, visited)) return true;
    const start = r?.config.screenStart ?? saved.screenStart;
    return !!start && focus(start);
  };
  const recover = (saved: FocusSnapshot) => {
    const captured = epoch;
    queueMicrotask(() => {
      if (epoch !== captured || recoveredEpoch === captured || !same(active, saved)) return;
      if (!regions.get(saved.regionId)?.children.has(saved.childId)) {recoveredEpoch = captured; restore(saved);}
    });
  };
  const schedulePrune = (id: string) => queueMicrotask(() => {
    const r = regions.get(id);
    if (r && !r.owner && !r.host && !r.children.size && active?.regionId === id && !isRetained(id)) {active = undefined; emit([id], undefined, 'focus');}
    prune(id);
  });
  return {
    subscribe(listener: () => void) {listeners.add(listener); return () => {listeners.delete(listener);};},
    getSnapshot: () => revision,
    subscribeRegion,
    regionSnapshot: (id: string) => scopedRevisions.get(id) ?? 0,
    subscribeTarget(id: string | undefined, edges: readonly string[], listener: () => void, childId?: string) {
      const releases = [...new Set(edges)].map(edge => subscribeHost(edge, listener));
      if (id !== undefined) releases.push(subscribeTargetRegion(id, listener));
      if (id !== undefined && childId !== undefined) releases.push(subscribeChild(childKey(id, childId), listener));
      return () => {for (const release of releases) release();};
    },
    targetSnapshot: (id: string | undefined, edges: readonly string[], childId?: string) => edges.reduce((latest, edge) => Math.max(latest, hostRevisions.get(edge) ?? 0),
      id === undefined ? 0 : Math.max(targetRevisions.get(id) ?? 0, childId === undefined ? 0 : childRevisions.get(childKey(id, childId)) ?? 0)),
    capture: (): FocusSnapshot | undefined => active && {...currentSnapshot(active), order: [...currentSnapshot(active).order]},
    focus, focusRegion, entryTarget, entryAddress, restore,
    /** capture() also retains historical return proof; this requires a still-attached enabled leaf. */
    attachedFocus: (): FocusSnapshot | undefined => active && enabledPath(active.regionId) && regions.get(active.regionId)?.children.has(active.childId) ? snapshot(active) : undefined,
    hasRetainedFocus: () => retained.size > 0,
    cellPosition: (id: string) => regions.get(id)?.cellMemory,
    destinations(address: FocusAddress) {
      const r = regions.get(address.regionId);
      if (!r) return {};
      r.navigate ??= createCellNavigator([...r.children.keys()].flatMap(id => {const cell = cellOf(r, id); return cell ? [{id, cell}] : [];}), r.config, r.cellMemory ?? {});
      return r.navigate(address.childId);
    },
    order: (id: string): readonly string[] => {const r = regions.get(id); return r ? order(r) : [];},
    remembered: (id: string) => regions.get(id)?.remembered ?? memory.get(id),
    config: (id: string) => regions.get(id)?.config,
    host: (id: string): FocusHost | undefined => regions.get(id)?.host,
    child: (address: FocusAddress): FocusHost | undefined => regions.get(address.regionId)?.children.get(address.childId)?.host,
    /** Actual attached entity lookup is distinct from opaque native child identity. */
    entityAddress: (regionId: string, entityId: string): FocusAddress | undefined => {
      const r = regions.get(regionId), childId = r?.entities.get(entityId);
      return childId !== undefined && enabledPath(regionId) && r?.children.has(childId) ? {regionId, childId} : undefined;
    },
    recovery: (id: string): readonly FocusSnapshot[] => [active, ...[...retained.values()].flat()].filter((saved): saved is FocusSnapshot => saved?.regionId === id),
    isRegionActive: (id: string): boolean => active?.regionId === id || isRetained(id),
    retain(address: FocusAddress | FocusSnapshot) {
      const chain: FocusSnapshot[] = [], visited = new Set<string>();
      let saved: FocusSnapshot | undefined = 'order' in address ? address : snapshot(address);
      while (saved && !visited.has(saved.regionId)) {visited.add(saved.regionId); chain.push(saved); saved = saved.returnTo;}
      const owner = {}; retained.set(owner, chain); emit(chain.map(saved => saved.regionId), undefined, 'focus');
      return () => {if (!retained.delete(owner)) return; emit(chain.map(saved => saved.regionId), undefined, 'focus'); for (const saved of chain) schedulePrune(saved.regionId);};
    },
    registerReturn(id: string, opener: FocusSnapshot | undefined) {
      const r = region(id), owner = {}; r.returns.set(owner, opener);
      return () => {r.returns.delete(owner); schedulePrune(id);};
    },
    mountRegion(id: string, owner: object) {
      const r = region(id);
      if (r.owner && r.owner !== owner) throw new Error(`Duplicate live focus region: ${id}`);
      r.owner = owner;
      return () => {if (r.owner === owner) r.owner = undefined; schedulePrune(id);};
    },
    configure(config: FocusRegionConfig) {
      const r = region(config.id);
      if (configKey(r.config) === configKey(config)) return;
      const previousParent = r.config.parent;
      removeEntryDependency(config.id, r.config.entryRegion);
      if (config.entryRegion) {
        let dependents = entryDependents.get(config.entryRegion);
        if (!dependents) {dependents = new Set(); entryDependents.set(config.entryRegion, dependents);}
        dependents.add(config.id);
      }
      r.navigate = undefined; r.cellOrder = undefined;
      r.config = {...config, order: config.order && [...config.order], edges: config.edges && {...config.edges}, stops: config.stops && {...config.stops}, neighbors: config.neighbors && {...config.neighbors}, screenStart: config.screenStart && {...config.screenStart}};
      if (active?.regionId === config.id) active = currentSnapshot(active);
      emit([config.id, previousParent]);
    },
    attachRegion(id: string, host: FocusHost | null, owner?: object) {
      const r = host ? region(id) : regions.get(id);
      if (!r) return;
      if (host && r.host && (r.host !== host || r.hostOwner !== owner)) throw new Error(`Duplicate live focus region host: ${id}`);
      if (!host && owner && r.hostOwner !== owner) return;
      if (r.host === (host ?? undefined)) return;
      r.host = host ?? undefined; r.hostOwner = host ? owner : undefined; emit([id], id);
      if (!host && active?.regionId === id) recover(active);
      if (!host) schedulePrune(id);
    },
    attach(address: FocusAddress, host: FocusHost | null, owner?: object, cell?: FocusCell, entityId?: string) {
      const r = host ? region(address.regionId) : regions.get(address.regionId);
      if (!r) return;
      const old = r.children.get(address.childId);
      if (host && old && (old.host !== host || old.owner !== owner)) throw new Error(`Duplicate live focus child: ${address.regionId}/${address.childId}`);
      if (!host && owner && old?.owner !== owner) return;
      if (old?.host === (host ?? undefined) && JSON.stringify(old?.cell) === JSON.stringify(cell) && old?.entityId === entityId) return;
      const entityOwner = entityId === undefined ? undefined : r.entities.get(entityId);
      if (host && entityOwner !== undefined && entityOwner !== address.childId) throw new Error(`Duplicate live focus entity: ${address.regionId}/${entityId}`);
      if (old?.entityId !== undefined && r.entities.get(old.entityId) === address.childId) r.entities.delete(old.entityId);
      r.navigate = undefined; r.cellOrder = undefined;
      if (host) {r.children.set(address.childId, {host, owner, cell, entityId}); if (entityId !== undefined) r.entities.set(entityId, address.childId);}
      else r.children.delete(address.childId);
      emit([address.regionId]);
      if (!host && same(active, address) && active) recover(active);
      if (!host) schedulePrune(address.regionId);
    },
    didFocus(address: FocusAddress) {
      const previous = active?.regionId;
      epoch++; const r = region(address.regionId);
      const previousCell = lastFocus?.regionId === address.regionId && r.children.has(lastFocus.childId) ? cellOf(r, lastFocus.childId) : undefined;
      r.navigate = undefined;
      if (r.resetCellFocus !== address.childId) r.cellMemory = cellFocusMemory(cellOf(r, address.childId), previousCell, r.config, r.cellMemory ?? {});
      r.resetCellFocus = undefined; lastFocus = {...address};
      r.remembered = address.childId;
      const visited = new Set<string>([address.regionId]);
      for (let parent = r.config.parent; parent && !visited.has(parent); parent = region(parent).config.parent) {
        visited.add(parent); region(parent).descendant = {...address};
      }
      active = snapshot(address); emit([previous, address.regionId], undefined, 'focus', address); if (previous) schedulePrune(previous);
    },
    didBlur(address: FocusAddress) {
      const captured = epoch;
      queueMicrotask(() => {if (captured === epoch && same(active, address)) {
        // A detached focused host must recover before blur releases its ownership.
        if (!regions.get(address.regionId)?.children.has(address.childId)) {if (active) recover(active); return;}
        active = undefined; epoch++; emit([address.regionId], undefined, 'focus'); schedulePrune(address.regionId);
      }});
    },
    size: () => ({regions: regions.size, remembered: memory.size}),
  };
}
export type FocusRegistry = ReturnType<typeof createFocusRegistry>;
