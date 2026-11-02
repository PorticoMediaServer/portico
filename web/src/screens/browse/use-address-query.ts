import {useLayoutEffect, useRef, useState} from 'react';
import {router} from '../../router';
import {AddressContinuity, type AddressedSearch} from './address-query';

/** The grid's build: the address query it was opened with, and which adoption of it this is.
 *  A genuine navigation back to the very query the grid was built from still rebuilds it. */
export type AddressBuild = Readonly<{scope: string; search: AddressedSearch; generation: number}>;

/**
 * The part of the address↔grid continuity that outlives one grid: the query the grid is built
 * from, the agreement record and the last history action. It is reset when `scope` (the address
 * without the toolbar's state) changes, because that is another screen.
 */
export function useAddressBuild(scope: string, routed: AddressedSearch) {
  const [held, setHeld] = useState<AddressBuild>(() => ({scope, search: routed, generation: 0}));
  const [continuity, setContinuity] = useState(() => ({scope, value: new AddressContinuity()}));
  // The history action that produced the current address, so a genuine navigation is never
  // mistaken for the page's own replace echo.
  const arrival = useRef<Readonly<{href: string; type: string}> | undefined>(undefined);
  useLayoutEffect(() => router.history.subscribe(({location, action}) => {arrival.current = {href: location.href, type: action.type};}), []);
  const build = held.scope === scope ? held : {scope, search: routed, generation: held.generation + 1};
  const current = continuity.scope === scope ? continuity.value : undefined;
  // Store the new screen's build, so a later adoption on it is a later generation.
  const ports = useRef({routed});
  useLayoutEffect(() => {ports.current = {routed};});
  useLayoutEffect(() => {
    if (continuity.scope !== scope) setContinuity({scope, value: new AddressContinuity()});
    setHeld(previous => previous.scope === scope ? previous : {scope, search: ports.current.routed, generation: previous.generation + 1});
  }, [scope, continuity.scope]);
  const adopt = (search: AddressedSearch) => setHeld(previous => ({scope, search, generation: previous.generation + 1}));
  const genuine = () => arrival.current?.href === router.state.location.href && arrival.current.type !== 'REPLACE';
  return {build, continuity: current, adopt, genuine};
}
export type AddressSync = ReturnType<typeof useAddressBuild>;

/**
 * Settles the address and the grid whenever either changes: publishes the toolbar's query on the
 * address with a replace (no scroll reset), or adopts a navigated address into a rebuilt grid.
 * `routeKey`/`controllerKey` are absent until both sides are comparable.
 */
export function useAddressReconcile(sync: AddressSync, routeKey: string | undefined, controllerKey: string | undefined, routed: AddressedSearch,
  current: () => boolean, publish: () => void) {
  const ports = useRef({sync, routed, current, publish});
  useLayoutEffect(() => {ports.current = {sync, routed, current, publish};});
  useLayoutEffect(() => {
    const {sync, routed, current, publish} = ports.current;
    if (!routeKey || !controllerKey || !sync.continuity || !current()) return;
    const decision = sync.continuity.reconcile(routeKey, controllerKey, sync.genuine());
    if (decision === 'publish') publish();
    else if (decision === 'adopt') sync.adopt(routed);
  }, [routeKey, controllerKey, sync.continuity]);
}
