import type {Context, ReactNode} from 'react';

type Hold = () => () => void;
type ReactLike = Readonly<{
  createContext: <T>(value: T) => Context<T>;
  createElement: (type: unknown, props: Record<string, unknown> | null, ...children: ReactNode[]) => ReactNode;
  useCallback: <T extends (...args: never[]) => unknown>(callback: T, deps: readonly unknown[]) => T;
  useContext: <T>(context: Context<T>) => T;
  useLayoutEffect: (effect: () => void | (() => void), deps: readonly unknown[]) => void;
  useState: <T>(initial: T) => [T, (next: (current: T) => T) => void];
}>;

/** tablet-play6: while a page's side panel is open the expanded sidebar folds to the rail, and it opens
 * again when the panel closes. Shell-owned and never remembered: the person's sidebar choice is untouched.
 * The factory has no React of its own, so a test harness can build it over a fixture's React. */
export function railHoldHooks(react: ReactLike) {
  const Holder = react.createContext<Hold | undefined>(undefined);
  const Held = react.createContext(false);
  function RailHoldProvider({children}: {children: ReactNode}) {
    const [holds, setHolds] = react.useState(0);
    const hold = react.useCallback<Hold>(() => {
      let released = false;
      setHolds(count => count + 1);
      return () => {if (released) return; released = true; setHolds(count => count - 1);};
    }, []);
    return react.createElement(Holder.Provider, {value: hold}, react.createElement(Held.Provider, {value: holds > 0}, children));
  }
  /** Holds the rail folded for as long as `active` is true and the caller is mounted. */
  const useRailHold = (active: boolean) => {
    const hold = react.useContext(Holder);
    react.useLayoutEffect(() => active && hold ? hold() : undefined, [active, hold]);
  };
  const useRailHeld = (): boolean => react.useContext(Held);
  return {RailHoldProvider, useRailHold, useRailHeld};
}
