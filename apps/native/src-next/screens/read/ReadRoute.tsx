import React, {useLayoutEffect, useRef, useState} from 'react';
import {Platform} from 'react-native';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeRouteRenderer} from '../../shell/route-registry';
import type {NativePresentationBinding, NativeRouteSnapshot, NativeShellRoute} from '../../shell/routes';
import type {ReadNativeHost} from './ReadNativeHost';
import {nativeReadHost} from './host-component';
import {readRouteBack} from './read-route-back';

type ReadTarget = Extract<NativeShellRoute, {kind: 'read'}>;
type Preferences = React.ComponentProps<typeof ReadNativeHost>['preferences'];
type Entry = Readonly<{key: string; route: ReadTarget; binding: NativePresentationBinding}>;

/** Slots first remove the evicted guest. A new slot is assigned only after the
 * exact old guest reports its layout-effect teardown, never on a timer. */
function ReadingResidence({entries, desired, expected, context, preferences, Host, openSettings, alive}: {
  entries: readonly Entry[]; desired: readonly string[]; expected: NativeRouteSnapshot; context: NativeShellBootstrapContext;
  preferences: Preferences; Host: typeof ReadNativeHost; openSettings: () => void; alive: () => boolean;
}) {
  const [slots, setSlots] = useState<readonly string[]>([]), [teardowns, changed] = useState(0);
  const mounted = useRef(new Map<string, object>());
  const desiredKey = desired.join('\0');
  useLayoutEffect(() => {
    // An evicted guest's cleanup runs before it reports false. Do not allocate
    // its successor while even one superseded guest is still mounted. The ordered keys come
    // from the same `desiredKey` the deps already track: occurrences are minted as
    // `navigation-<n>:<m>`, nonempty and NUL-free, so the join/split round-trips exactly.
    const keys = desiredKey ? desiredKey.split('\0') : [];
    if (slots.some(key => !keys.includes(key) && mounted.current.has(key))) return;
    if (slots.length !== keys.length || slots.some((key, index) => key !== keys[index])) setSlots([...keys]);
  }, [desiredKey, slots, teardowns]);
  return <>{entries.map(entry => {
    const active = expected.route === entry.route && entry.binding.activeCurrent();
    const resident = slots.includes(entry.key) && desired.includes(entry.key);
    return <Host key={entry.key} context={context} target={entry.route} binding={entry.binding} active={active} resident={resident} preferences={preferences}
      onGuestState={(present, incarnation) => {if (present) mounted.current.set(entry.key, incarnation);
        else if (mounted.current.get(entry.key) === incarnation) {mounted.current.delete(entry.key); changed(value => value + 1);}}}
      onBack={readRouteBack(context.navigation, expected, entry.route, () => alive() && context.current() && entry.binding.activeCurrent())}
      onOpenSettings={openSettings}/>;
  })}</>;
}

/** Each visited occurrence retains its own original logical owner. Only the
 * current and most recently active other occurrence may retain guest views. */
export function createNativeReadRenderer(context: NativeShellBootstrapContext, preferences: Preferences, openSettings: () => void): NativeRouteRenderer {
  if (Platform.isTV) throw new Error('native_read_unavailable');
  const Host = nativeReadHost(); let alive = true, lastActive: string | undefined;
  const entries = new Map<string, Entry>(), retired = new Set<string>(), recency: string[] = [];
  const clear = () => {for (const entry of entries.values()) entry.binding.dispose(); entries.clear(); retired.clear(); recency.length = 0;};
  const reconcile = () => {
    if (!alive || !context.current()) {clear(); return;}
    const stack = context.navigation.presentationStack(), retained = new Set(stack.map(value => value.occurrence));
    for (const [key, entry] of entries) if (!retained.has(key) || !entry.binding.retainedCurrent()) {entry.binding.dispose(); entries.delete(key); retired.add(key);}
    for (const key of [...retired]) if (!retained.has(key)) retired.delete(key);
    for (let index = recency.length - 1; index >= 0; index--) if (!entries.has(recency[index]!)) recency.splice(index, 1);
    const current = stack.at(-1);
    if (current?.route.kind === 'read' && !retired.has(current.occurrence)) {
      if (!entries.has(current.occurrence)) {
        const binding = context.navigation.bindPresentation(current, {current: context.current, subscribe: context.subscribeAuthority});
        if (binding) entries.set(current.occurrence, {key: current.occurrence, route: current.route, binding});
        else retired.add(current.occurrence);
      }
      if (entries.has(current.occurrence) && lastActive !== current.occurrence) {
        const index = recency.indexOf(current.occurrence); if (index >= 0) recency.splice(index, 1);
        recency.unshift(current.occurrence); lastActive = current.occurrence;
      }
    } else lastActive = undefined;
  };
  return {
    render(route) {if (route.kind !== 'read') throw new Error('native_read_route_mismatch'); reconcile(); return null;},
    renderRetained() {
      reconcile(); if (!alive || !context.current() || !entries.size) return null;
      const expected = context.navigation.getSnapshot(), desired = recency.slice(0, 2);
      return <ReadingResidence entries={[...entries.values()]} desired={desired} expected={expected} context={context}
        preferences={preferences} Host={Host} openSettings={openSettings} alive={() => alive}/>;
    },
    dispose() {alive = false; clear();},
  };
}
