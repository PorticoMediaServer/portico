import {useLayoutEffect, useSyncExternalStore} from 'react';
import {presentedInPlayer} from '../platform/playback/presented';
import type {I18n} from '@i18n';
import {presentError} from '@core/presentation/errors.ts';
import {ToastHost, type ToastAction, type ToastData} from '../kit/overlays';
import {useShellWidth} from './viewport';

export class ShellFeedback {
  private value: ToastData | null = null;
  private retired = false;
  private serial = 0;
  private readonly listeners = new Set<() => void>();
  constructor(private readonly i18n: I18n, private readonly isCurrent: () => boolean) {}
  getSnapshot = () => this.value;
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  private publish(value: ToastData | null) { this.value = value; for (const listener of this.listeners) listener(); }
  reportError = (error: unknown) => {
    // A playback start the player already explains is not repeated as a toast (J-17).
    if (this.retired || !this.isCurrent() || presentedInPlayer(error)) return;
    const presented = presentError(error, 'home', {operation: 'action', i18n: this.i18n});
    if (!presented.silent) this.publish({id: String(++this.serial), message: presented.body, icon: 'i-warn', tone: 'error'});
  };
  /** A short confirmation: work finished, such as a selection's My List after the selection ended (J-14). */
  notify = (message: string, action?: ToastAction) => {
    if (this.retired || !this.isCurrent()) return;
    const id = String(++this.serial);
    // An action (Undo) is offered once: taking it closes the toast.
    this.publish({id, message, icon: 'i-check', ...(action ? {action: {...action, onAction: () => {this.dismiss(id); action.onAction();}}, durationMs: 30_000} : {})});
  };
  dismiss = (id: string) => { if (this.value?.id === id) this.publish(null); };
  dispose = () => { this.retired = true; this.publish(null); this.listeners.clear(); };
}
/** Where a toast rests: above the tab bar, and above the player bar when there is one
 * (motion.html 2.15, 3.10). Phone values clear the home indicator too. */
export function shellToastBottom(width: number, dockVisible: boolean): string {
  return width >= 1024 ? `${dockVisible ? 108 : 24}px` : `calc(${dockVisible ? 150 : 80}px + env(safe-area-inset-bottom, 0px))`;
}
export function ShellFeedbackHost({source, dockVisible}: {source: ShellFeedback; dockVisible: boolean}) {
  const toast = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot), width = useShellWidth();
  const bottom = shellToastBottom(width, dockVisible);
  // Screen-owned toast hosts portal to the document, outside the shell; they read the same rest.
  useLayoutEffect(() => {
    const root = document.documentElement;
    root.style.setProperty('--shell-toast-bottom', bottom);
    return () => {root.style.removeProperty('--shell-toast-bottom');};
  }, [bottom]);
  return <ToastHost toast={toast} onDismiss={source.dismiss} />;
}
