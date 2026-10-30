import {useId, type ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import {Button, Switch} from '../controls';
import {Dialog} from '../overlays/Dialog';
import styles from './member-reset-code-sheet.module.css';

const t = defaultI18n.t;

/** The caller's exact published result. A prior token is never a stand-in for
 * readiness: rotation withdraws it before IO, and an uncertain reply leaves the
 * result unresolved until the caller can publish an exact current one. */
export type MemberResetCodePublished = Readonly<
  | {state: 'ready'; code: string; expiresAt: string; error?: ReactNode}
  | {state: 'pending'}
  | {state: 'unresolved'; error: ReactNode}
>;

export type MemberResetCodeSheetProps = Readonly<{
  open: boolean;
  username: string;
  result: MemberResetCodePublished;
  turnOffTwoStep: boolean;
  /** Supplied by the owner-authored `useMemberResetCodeView` opening. The sheet
   * only forwards the change; the opening owns the published code, replacement,
   * deadline and uncertainty. The sheet never issues or reissues. */
  onTurnOffTwoStepChange?: (value: boolean) => void;
  onCopy: () => void;
  onDone: () => void;
  busy?: boolean;
}>;

/** auth21 / owner-people's reset-code result. Presentation only: the caller owns
 * issuance, rotation, clipboard and the published result's lifetime. */
export function MemberResetCodeSheet({open, username, result, turnOffTwoStep, onTurnOffTwoStepChange, onCopy, onDone, busy = false}: MemberResetCodeSheetProps) {
  const offId = useId(), ready = result.state === 'ready';
  return <Dialog open={open} onOpenChange={value => {if (!value) onDone();}} title={t('next.settings.people.resetCodeTitle', {username})}
    description={t('next.settings.people.resetCodeGive')} showCloseButton={false} footerClassName={ready ? styles.footer : undefined}
    footer={<>{ready && <Button size="small" icon="i-copy" iconSize={18} disabled={busy} onClick={onCopy}>{t('next.settings.people.resetCodeCopy')}</Button>}
      <Button size="small" variant="primary" onClick={onDone}>{t('action.done')}</Button></>}>
    {result.state === 'pending' ? <p className={styles.pending} role="status">{t('next.settings.loading')}</p>
      : result.state === 'unresolved' ? <p className={styles.error} role="alert">{result.error}</p>
      : <>
        {result.error && <p className={styles.error} role="alert">{result.error}</p>}
        <div className={styles.code} dir="ltr">{result.code}</div>
        <p className={styles.help}>{t('next.settings.people.resetCodeOnce', {time: defaultI18n.time(result.expiresAt)})}</p>
        <label className={styles.choice}>
          <span>{t('next.settings.people.resetCodeOff')}<small id={offId}>{t('next.settings.people.resetCodeOffHelp')}</small></span>
          <Switch checked={turnOffTwoStep} disabled={busy || !onTurnOffTwoStepChange} aria-describedby={offId}
            onCheckedChange={value => onTurnOffTwoStepChange?.(value)} />
        </label>
      </>}
  </Dialog>;
}
