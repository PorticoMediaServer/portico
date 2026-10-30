import type {DockState, PlaybackFence} from '../../../../../packages/player-core/src';

/** E-mobile-14b: Minimize is presentation only. It folds whatever local entry is expanded now,
 * even when the fence the button was drawn with has since changed (a stop, an end or a failed
 * start replaced it), so the tap never does nothing while a player is open. */
export function minimizeTarget(dock: Pick<DockState, 'local'>, pressed: PlaybackFence): PlaybackFence {
  return dock.local?.expanded ? dock.local.fence : pressed;
}
