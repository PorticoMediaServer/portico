import {defaultI18n} from '@i18n';
import {Button} from '../../kit/controls';

const t = defaultI18n.t;

/** m:ss for the drawn countdown; ceil keeps a positive second from reading 0:00. */
function clock(remainingSeconds: number) {
  const whole = Math.max(0, Math.ceil(remainingSeconds));
  const minutes = Math.floor(whole / 60), seconds = whole % 60;
  return `${minutes}:${String(seconds).padStart(2, '0')}`;
}

/**
 * auth20f: the one action on the expired and rate-limited reset-code states.
 * The owner supplies the real deadline through remainingSeconds and keeps the
 * countdown, the request and the saved code; this button renders only the drawn
 * label and the tabular figures the frame gives it. It is disabled while busy,
 * while the supplied countdown still runs, and whenever the remaining value is not
 * a finite number — a malformed deadline must never look like an ended one.
 */
export function OwnerResetRewriteButton({remainingSeconds, busy, primary = false, onRewrite}: {
  remainingSeconds: number;
  busy: boolean;
  primary?: boolean;
  onRewrite: () => void;
}) {
  const finite = Number.isFinite(remainingSeconds);
  const waiting = finite && remainingSeconds > 0;
  return <Button variant={primary ? 'primary' : 'ghost'} size="small" disabled={busy || !finite || waiting} onClick={onRewrite}
    style={{fontVariantNumeric: 'tabular-nums'}}>{waiting ? t('accounts.recovery.writeNewIn', {time: clock(remainingSeconds)}) : t('accounts.recovery.rewrite')}</Button>;
}
