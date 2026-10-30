import type {FeedbackTarget, ViewerFeedback} from './feedback-controller';
export type {FeedbackTarget} from './feedback-controller';

let current: ViewerFeedback | undefined;
const listeners = new Set<() => void>();
const changed = () => {for (const listener of listeners) listener();};
/** The protected action host owns the service; these module-level functions
 * neither create sessions nor start I/O while registrations are imported. */
export const feedbackAvailable = () => current?.available() === true;
export const subscribeFeedback = (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};};
export function openFeedback(target?: FeedbackTarget): void {current?.open(target);}
export function installFeedback(controller: ViewerFeedback): () => void {
  current = controller; const off = controller.subscribe(changed); changed();
  return () => {off(); if (current === controller) {current = undefined; changed();}};
}
