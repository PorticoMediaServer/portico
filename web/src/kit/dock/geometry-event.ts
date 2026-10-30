/** Geometry only, within one document. Carries no media/session/business state. */
export const dockMotionEvent = 'portico:dock-motion';
export type DockMotionDetail = Readonly<
  {kind: 'begin' | 'track'; offset: number} |
  {kind: 'release'; offsets: readonly number[]; opacity: readonly number[]; duration: number} |
  {kind: 'hold'; raised: boolean; scales: readonly number[]; duration: number} |
  {kind: 'reset'}
>;
export function signalDockMotion(element: HTMLElement, detail: DockMotionDetail) {
  element.dispatchEvent(new CustomEvent<DockMotionDetail>(dockMotionEvent, {bubbles: true, detail}));
}
