import * as Vega from '@amazon-devices/react-native-kepler';
import React, {createContext, forwardRef, useCallback, useContext, useEffect, useMemo, useRef, useState} from 'react';
import {keyboardHasBack} from './typing';

/**
 * `TVFocusGuideView` as the Apple TV build means it, in the three respects where the SDK's differs.
 *
 * `focusable={false}`: nothing inside the guide can be reached with the remote. The shared code
 * relies on it to keep a text field's real input, a screen that is not the front one, and the
 * player's hidden controls out of the way. The SDK's guide is a view that passes `focusable` on
 * to itself, which says nothing about what is inside it. Here the guide tells its contents
 * instead: a `Pressable` inside an unreachable guide is not focusable, and a `TextInput` there
 * exists only while it is being typed into (`pressable.tsx`, `text-input.tsx`). The guide still
 * gets the prop: a guide that is focusable is how focus arriving from outside finds its way in.
 *
 * `autoFocus`: focus entering the guide goes to the control inside it that had focus last, or to
 * its first one. The SDK's guide remembers the last one natively and keeps sending focus there
 * after that control has been hidden or has gone (a destination root the shell keeps alive but
 * not displayed, a card a list no longer mounts), where focus is refused or lost. So the choice
 * is made here, from the controls that are in reach right now, and given to the guide as its
 * destination.
 *
 * Focus that is nowhere. On tvOS the focus engine finds a new home for focus whenever the focused
 * view goes away: a pushed screen takes it, and the screen beneath takes it back. On Vega nothing
 * does, and the remote then moves nothing. A screen's guide (one told whether it is the front
 * screen, `focusable={true | false}`) therefore asks for focus when it becomes the front screen,
 * or gets its first control, while no control holds it.
 */
export const Unreachable = createContext(false);

type FocusNode = {requestTVFocus?: () => void};

/**
 * A control that can take focus, as a guide knows it: `open` as of its last render (not inside an
 * unreachable guide), `live` once the guides around it know it is in reach, `prefers` while it
 * asks to be where focus starts, `label` its accessible name.
 */
export type Member = {node(): unknown; open: boolean; live: boolean; prefers: boolean; label?: string};

type Scope = {
  parent: Scope | null;
  add(member: Member): void;
  remove(member: Member): void;
  focused(member: Member): void;
  /** A front screen's guide inside this one has appeared (1) or gone (-1). */
  fronts(change: number): void;
};

const ScopeContext = createContext<Scope | null>(null);

/** The control that has focus, as far as its own focus and blur events tell. */
let holder: Member | null = null;
const watchers = new Set<() => void>();

function hold(member: Member | null): void {
  if (holder === member) return;
  holder = member;
  for (const watcher of [...watchers]) watcher();
}

export const focusHolder = (): Member | null => holder;

/** What a control tells the guides around it: that it is in reach, out of reach, focused or not. */
export function useGuides(): {join(member: Member): () => void; focused(member: Member): void; blurred(member: Member): void} {
  const scope = useContext(ScopeContext);
  return useMemo(() => ({
    join(member) {
      member.live = true;
      for (let around = scope; around; around = around.parent) around.add(member);
      return () => {
        member.live = false;
        for (let around = scope; around; around = around.parent) around.remove(member);
        // Out of reach, it has lost focus for good.
        if (holder === member) hold(null);
      };
    },
    focused(member) {
      // A screen being covered loses its controls one after another, and focus runs along them
      // on its way out: that is not the viewer choosing them.
      if (!member.open) return;
      hold(member);
      for (let around = scope; around; around = around.parent) around.focused(member);
    },
    blurred(member) {
      if (holder === member) hold(null);
    },
  }), [scope]);
}

/**
 * Asks for focus on `target()` until `done()`, once by default: a request made while a screen is
 * still arriving, or while the keyboard's window is still closing, is not honored, and nothing
 * says so. A caller may pass further `tries` for its own case. `finished` is called once, when
 * done or out of tries. Returns how to stop.
 */
export function seekFocus(target: () => Member | null | undefined, done: () => boolean, tries: readonly number[] = [0], finished?: () => void): () => void {
  let over = false;
  const timers: ReturnType<typeof setTimeout>[] = [];
  const stop = () => {
    if (over) return false;
    over = true;
    for (const timer of timers) clearTimeout(timer);
    watchers.delete(check);
    return true;
  };
  const finish = () => { if (stop()) finished?.(); };
  const check = () => { if (done()) finish(); };
  watchers.add(check);
  tries.forEach((delay, index) => timers.push(setTimeout(() => {
    if (over) return;
    if (done()) { finish(); return; }
    (target()?.node() as FocusNode | null | undefined)?.requestTVFocus?.();
    if (index === tries.length - 1) timers.push(setTimeout(finish, 300));
  }, delay)));
  return () => { stop(); };
}

type GuideProps = React.ComponentProps<typeof Vega.TVFocusGuideView>;
type GuideNode = React.ComponentRef<typeof Vega.TVFocusGuideView>;

const HISTORY = 24;

/**
 * A guide that, when `managing`, chooses where focus goes when it enters. (One component either
 * way: a guide that starts or stops being given its destinations must not draw its contents anew.)
 */
const Remembering = forwardRef<GuideNode, GuideProps & {reachable: boolean; managing: boolean}>(function Remembering({reachable, managing, ...props}, ref) {
  const parent = useContext(ScopeContext);
  const members = useRef(new Set<Member>());
  // The controls that had focus here, latest first.
  const history = useRef<Member[]>([]);
  const [destination, setDestination] = useState<unknown>(null);
  // The control focused most recently that is still in reach. A list may have drawn it again
  // since, as a new control: then the one with its name. Failing that, the first control that
  // asks to be where focus starts, or the first one there is.
  const chosen = useCallback((): Member | undefined => {
    const all = members.current;
    // The control that had focus here, while it is still here: a real remembered landing, not a
    // name looked up again. A control drawn anew is a different control.
    for (const was of history.current) if (all.has(was)) return was;
    let preferring: Member | undefined, first: Member | undefined;
    for (const member of all) {
      first ??= member;
      if (!preferring && member.prefers) preferring = member;
    }
    return preferring ?? first;
  }, []);
  // A screen's guide: brings focus to its choice when it is the front screen and focus is
  // nowhere, or has just landed on another of its controls (the system's pick as the screen
  // came back). Focus held outside the screen (the rail, a sheet over it) is left alone. Screens
  // nest (a destination holds a stack of pages): the innermost front one is the one that acts.
  const front = managing && props.focusable === true && reachable;
  const state = useRef({front, mounted: true});
  state.current.front = front;
  const inner = useRef(0);
  const seeking = useRef<(() => void) | undefined>(undefined);
  const pending = useRef(false);
  const decide = useCallback(() => {
    pending.current = false;
    // Never while a field is being typed into: the keyboard has focus then.
    if (!state.current.mounted || !state.current.front || inner.current > 0 || seeking.current || keyboardHasBack()) return;
    // Anything already holding focus stays where it is. A landing inside this region is the
    // viewer's or the native guide's, and there is no window in which it is "put right": that
    // window was a second manager, and it could take a valid in-region landing away.
    if (holder) return;
    const intended = chosen();
    const settled = () => !!holder;
    // One request, once, while focus is nowhere: the native guide owns where it lands.
    seeking.current = seekFocus(() => intended ?? chosen(), settled, undefined, () => { seeking.current = undefined; });
  }, [chosen]);
  // Decided once everything that changed together has said so: a screen's controls arrive, and
  // another screen's leave, in one go.
  const claim = useCallback(() => {
    if (pending.current) return;
    pending.current = true;
    void Promise.resolve().then(decide);
  }, [decide]);
  const scope = useMemo<Scope>(() => {
    const choose = () => {
      // With nothing in reach (this screen is covered) the last choice stands: when the screen
      // comes back, the native guide acts before any control here has said it is back.
      const node = chosen()?.node();
      if (node) setDestination((current: unknown) => (current === node ? current : node));
    };
    return {
      parent,
      add(member) {
        const first = members.current.size === 0;
        members.current.add(member);
        choose();
        if (first) claim();
      },
      remove(member) { members.current.delete(member); choose(); },
      focused(member) {
        const past = history.current.filter(other => other !== member);
        history.current = [member, ...past].slice(0, HISTORY);
        choose();
      },
      fronts(change) { inner.current += change; },
    };
  }, [parent, chosen, claim]);
  useEffect(() => {
    if (!front) return;
    for (let around = parent; around; around = around.parent) around.fronts(1);
    claim();
    // The screen that was in front may still hold focus until it has gone.
    watchers.add(claim);
    return () => {
      watchers.delete(claim);
      for (let around = parent; around; around = around.parent) around.fronts(-1);
      seeking.current?.();
      seeking.current = undefined;
    };
  }, [front, parent, claim]);
  useEffect(() => () => { state.current.mounted = false; }, []);
  return (
    <ScopeContext.Provider value={managing ? scope : parent}>
      <Vega.TVFocusGuideView ref={ref} {...props} destinations={managing ? (destination ? [destination as never] : undefined) : props.destinations} />
    </ScopeContext.Provider>
  );
});

const Guide = forwardRef<GuideNode, GuideProps>(function TVFocusGuideView(props, ref) {
  const unreachable = useContext(Unreachable) || props.focusable === false;
  // The caller may own the destinations instead: the shared focus region hands them over through
  // `ref.setDestinations`, and from that first call on they are the caller's to set.
  const [imperative, setImperative] = useState(false);
  // The host whose method was wrapped, and the method it had before: leaving it - or leaving for
  // another host - puts that method back, so a caller never holds a wrapper of a wrapper and a
  // genuinely new host starts with the guide deciding for itself again.
  const wrapped = useRef<{node: GuideNode; own: GuideNode['setDestinations']} | undefined>(undefined);
  const unwrap = useCallback(() => {
    const previous = wrapped.current;
    if (!previous) return;
    previous.node.setDestinations = previous.own;
    wrapped.current = undefined;
  }, []);
  const attach = useCallback((node: GuideNode | null) => {
    if (wrapped.current && wrapped.current.node !== node) {
      unwrap();
      setImperative(false);
    }
    if (node && !wrapped.current) {
      const own = node.setDestinations;
      wrapped.current = {node, own};
      node.setDestinations = destinations => {setImperative(true); own.call(node, destinations);};
    }
    if (typeof ref === 'function') ref(node);
    else if (ref) (ref as {current: GuideNode | null}).current = node;
  }, [ref, unwrap]);
  useEffect(() => unwrap, [unwrap]);
  // A guide given its destinations decides for itself; one the caller drives does not.
  const managing = props.autoFocus === true && props.destinations === undefined && !imperative;
  return (
    <Unreachable.Provider value={unreachable}>
      <Remembering ref={attach} reachable={!unreachable} managing={managing} {...props} />
    </Unreachable.Provider>
  );
});
// The SDK implementation maps every destination through findNodeHandle, which
// accepts existing numeric handles as well as host refs. Its declaration omits numbers.
type Destination = Parameters<Vega.FocusGuideMethods['setDestinations']>[0][number] | number;
type GuideHandle = Omit<GuideNode, 'setDestinations'> & {setDestinations(destinations: Destination[]): void};
export const TVFocusGuideView = Guide as unknown as React.ForwardRefExoticComponent<GuideProps & React.RefAttributes<GuideHandle>>;
