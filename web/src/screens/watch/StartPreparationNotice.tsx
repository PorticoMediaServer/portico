import {defaultI18n} from '@i18n';
import type {StartPreparation} from '@core/playback-v1/index.ts';
import waiting from '../../kit/states/playback-waiting.module.css';

export type StartPreparationNoticeProps = Readonly<{
  /** The canonical public slow-start state from the current playback snapshot. */
  preparation?: StartPreparation;
  className?: string;
}>;

/** The server's own slow-start message, drawn once in the existing player waiting treatment.
 * It keeps no session, timer, retry or transport of its own: absent state renders nothing,
 * and the canonical snapshot clears it when the start settles. */
export function StartPreparationNotice({preparation, className}: StartPreparationNoticeProps) {
  if (!preparation) return null;
  return <div className={className ? `${waiting.preparation} ${className}` : waiting.preparation}
    role="status" aria-live="polite" aria-atomic="true" data-start-preparation>
    <p>{defaultI18n.t(preparation.messageId)}</p>
  </div>;
}
