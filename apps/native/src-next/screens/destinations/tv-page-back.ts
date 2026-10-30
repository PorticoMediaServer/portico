import type {FocusAddress, FocusRegistry} from '../../kit/focus/registry';

type Page = Readonly<{
  current: () => boolean; start: () => FocusAddress | undefined;
  atTop: () => boolean; scrollTop: () => void;
}>;
/** Explicit Back is a lifecycle jump. Arrows remain entirely native. */
export function createTVPageBack(registry: Pick<FocusRegistry, 'capture' | 'focus' | 'subscribe'>, page: Page) {
  let pending = false, alive = true, opening: FocusAddress | undefined;
  const retry = () => {
    if (!pending || !alive) return;
    if (!page.current()) {pending = false; return;}
    const focused = registry.capture();
    if (focused && opening && (focused.regionId !== opening.regionId || focused.childId !== opening.childId)) {pending = false; return;}
    const target = page.start();
    if (target) {
      // A native host may synchronously publish focus. Retire before requesting it.
      pending = false;
      if (!registry.focus(target, {resetCellMemory: true})) pending = alive && page.current();
    }
  };
  const unsubscribe = registry.subscribe(retry);
  const top = () => {
    if (!alive || !page.current()) return false;
    pending = true; opening = registry.capture(); page.scrollTop(); retry(); return true;
  };
  return {
    top,
    back() {
      if (!alive || !page.current()) return false;
      if (pending) {retry(); return true;}
      const target = page.start(), focused = registry.capture();
      if (page.atTop() && (!target || focused?.regionId === target.regionId && focused.childId === target.childId)) return false;
      // Scroll first: a virtualized starting card need not have a native host yet.
      return top();
    },
    dispose() {alive = false; pending = false; unsubscribe();},
  };
}
