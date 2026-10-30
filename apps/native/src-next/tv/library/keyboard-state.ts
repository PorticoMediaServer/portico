import type {NativeDestinationActivation} from '../../screens/destinations/focus';
import type {NativeLibrarySaveView, NativeSaveViewSnapshot} from '../../screens/library/save-view';
import {applyNativeTVKeyboardKey} from '../../screens/search/keyboard';

export type NativeTVNameInputSnapshot = Readonly<{form: NativeSaveViewSnapshot; keyboard: NativeDestinationActivation}>;

/** Name editing borrows the captured Save-view controller. Each explicit
 * keyboard opening gets one focus ticket; text changes never renew it. */
export function createNativeTVNameInput(controller: NativeLibrarySaveView) {
  let active = false, revision = 0, disposed = false;
  const listeners = new Set<() => void>();
  let snapshot: NativeTVNameInputSnapshot = Object.freeze({form: controller.getSnapshot(),
    keyboard: Object.freeze({visible: false, intent: 'enter', revision})});
  const read = (): NativeTVNameInputSnapshot => {
    const form = controller.getSnapshot();
    if (!form.open || form.nameLocked) active = false;
    const visible = !disposed && active && form.open && !form.busy && !form.nameLocked;
    if (snapshot.form !== form || snapshot.keyboard.visible !== visible || snapshot.keyboard.revision !== revision) {
      snapshot = Object.freeze({form, keyboard: Object.freeze({visible, intent: 'enter', revision})});
    }
    return snapshot;
  };
  const changed = () => {
    const previous = snapshot;
    if (read() !== previous) for (const listener of [...listeners]) listener();
  };
  const unsubscribe = controller.subscribe(changed);
  return Object.freeze({
    getSnapshot: read,
    subscribe: (listener: () => void) => {if (disposed) return () => {}; listeners.add(listener); return () => {listeners.delete(listener);};},
    openKeyboard: (): boolean => {
      const {form} = read();
      if (disposed || !form.open || form.busy || form.nameLocked) return false;
      if (!active) {active = true; revision++; changed();}
      return true;
    },
    press: (key: string): boolean => {
      const state = read();
      if (!state.keyboard.visible) return false;
      const name = applyNativeTVKeyboardKey(state.form.name, key);
      if (name === state.form.name || Array.from(name).length > 200) return false;
      controller.setName(name);
      const after = read();
      return after.form.open && after.form.name === name;
    },
    back: (): 'blocked' | 'keyboard' | 'close' => {
      const {form} = read();
      if (disposed || !form.open || form.busy) return 'blocked';
      if (active) {active = false; changed(); return 'keyboard';}
      // The owning module, not this borrowed input, closes the actual form.
      return 'close';
    },
    dispose: () => {
      if (disposed) return;
      disposed = true; active = false; unsubscribe(); changed(); listeners.clear();
    },
  });
}
