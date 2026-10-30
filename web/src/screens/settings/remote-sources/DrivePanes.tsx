import {defaultI18n} from '@i18n';
import {RadioGroup} from 'radix-ui';
import {Button, TextField} from '../../../kit/controls';
import {Icon} from '../../../kit/Icon';
import styles from './remote-sources.module.css';

const t = defaultI18n.t;

/** The two Google sign-in choices exactly as remote-sources.html 3g/3h draw
 * them: plain radio cards with the existing canonical descriptions. The
 * caller keeps the mode-switch callback and all OAuth transport. */
export function DriveModePane({mode, disabled, onChange}: Readonly<{
  mode: 'service' | 'oauth'; disabled: boolean; onChange: (mode: 'service' | 'oauth') => void;
}>) {
  const choose = (next: 'service' | 'oauth') => {if (!disabled) onChange(next);};
  return <RadioGroup.Root className={styles.driveModes} aria-label={t('next.remoteSources.driveSignIn')} value={mode} disabled={disabled}
    onValueChange={value => {if (value === 'service' || value === 'oauth') choose(value);}}>
    <RadioGroup.Item value="service" data-picked={mode === 'service' || undefined}
      className={styles.driveMode} disabled={disabled}>
      <span className={styles.driveModeRadio} aria-hidden="true"/>
      <span className={styles.driveModeBody}>
        <span className={styles.driveModeTitle}>{t('next.remoteSources.driveServiceAccount')}
          <span className={styles.driveModeTag}>{t('next.remoteSources.driveRecommended')}</span></span>
        <span className={styles.driveModeHint}>{t('next.remoteSources.driveServiceAccountHint')}</span>
      </span>
    </RadioGroup.Item>
    <RadioGroup.Item value="oauth" data-picked={mode === 'oauth' || undefined}
      className={styles.driveMode} disabled={disabled}>
      <span className={styles.driveModeRadio} aria-hidden="true"/>
      <span className={styles.driveModeBody}>
        <span className={styles.driveModeTitle}>{t('next.remoteSources.driveOAuth')}</span>
        <span className={styles.driveModeHint}>{t('next.remoteSources.driveOAuthHint')}</span>
      </span>
    </RadioGroup.Item>
  </RadioGroup.Root>;
}

/** The service-account key pane. Empty, invalid, OAuth-client and valid states
 * are the parser's existing facts; Upload/Clear/Copy and the OAuth switch are
 * the caller's own handlers. No parser, upload transport or secret handling. */
export function DriveKeyPane({value, disabled, error, email, oauth, onChange, onUpload, onClear, onCopy, onUseOAuth}: Readonly<{
  value: string; disabled: boolean; error?: string; email?: string; oauth: boolean;
  onChange: (value: string) => void; onUpload: () => void; onClear: () => void; onCopy: () => void; onUseOAuth: () => void;
}>) {
  const empty = !value.trim();
  return <>
    <label className={styles.configField}><span className={styles.configLabel}>{t('next.remoteSources.driveKey')}</span>
      <textarea className={`${styles.config} ${styles.driveKeyArea}`} aria-label={t('next.remoteSources.driveKey')} value={value} placeholder={t('next.remoteSources.driveKeyPlaceholder')}
        maxLength={65536} disabled={disabled} spellCheck={false} rows={5} onChange={event => onChange(event.target.value)}/>
      <small className={styles.note}>{t('next.remoteSources.driveKeyHelp')}</small>
    </label>
    <div className={styles.testRow}>
      <Button size="compact" variant="glass" disabled={disabled} onClick={onUpload}>{t('next.remoteSources.driveUpload')}</Button>
      <Button size="compact" variant="text" disabled={disabled || empty} onClick={onClear}>{t('next.remoteSources.driveClear')}</Button>
    </div>
    {empty ? <p className={styles.note}>{t('next.remoteSources.driveKeyEmptyHint')}</p>
      : error ? <div className={styles.driveKeyError}><Icon name="i-warn" size={18}/><span>{error}</span>
        {oauth && <Button size="xs" variant="glass" disabled={disabled} onClick={onUseOAuth}>{t('next.remoteSources.driveOAuth')}</Button>}</div>
      : email ? <div className={styles.driveKeyValid}>
        <div className={styles.driveKeyHead}><Icon name="i-check" size={20}/><div>
          <b>{t('next.remoteSources.driveEmailHeading')}</b><small>{t('next.remoteSources.driveEmailHelp')}</small>
        </div></div>
        <span className={styles.driveKeyEmail}>{email}</span>
        <Button size="xs" variant="text" onClick={onCopy}>{t('next.remoteSources.driveCopy')}</Button>
      </div> : null}
  </>;
}

/** The OAuth client pane. It renders only the phases the source can produce;
 * the producer does not distinguish refused, unknown-client or no-code errors,
 * so those remain the one existing invalid result. Handlers stay with the
 * caller; no window, storage or URL parsing lives here. */
export function DriveOAuthPane({phase, clientId, clientSecret, address, error, disabled, onClientId, onClientSecret, onSignIn, onAddress, onPaste}: Readonly<{
  phase: 'idle' | 'starting' | 'waiting' | 'ready' | 'expired' | 'denied' | 'error';
  clientId: string; clientSecret: string; address: string; error?: string; disabled: boolean;
  onClientId: (value: string) => void; onClientSecret: (value: string) => void; onSignIn: () => void;
  onAddress: (value: string) => void; onPaste: () => void;
}>) {
  return <>
    <TextField label={t('next.remoteSources.driveClientId')} value={clientId} maxLength={512} disabled={disabled}
      autoCapitalize="off" spellCheck={false} className={styles.monoInput} onChange={event => onClientId(event.target.value)}/>
    <TextField label={t('next.remoteSources.driveClientSecret')} type="password" value={clientSecret} maxLength={2048} disabled={disabled}
      onChange={event => onClientSecret(event.target.value)}/>
    <div className={styles.testRow}>
      <Button size="compact" variant="glass" disabled={disabled || !clientId.trim() || !clientSecret} onClick={onSignIn}>{t('next.remoteSources.driveOAuthSignIn')}</Button>
      {phase === 'expired' && <span className={styles.note} role="status">{t('next.remoteSources.driveOAuthExpired')}</span>}
    </div>
    {phase === 'waiting' && <div className={styles.driveOAuthCard}><Icon name="i-link" size={20}/><span>{t('next.remoteSources.driveOAuthWaiting')}</span></div>}
    {phase === 'ready' && <div className={styles.driveOAuthReady}><Icon name="i-check" size={20}/><span>{t('next.remoteSources.driveOAuthReady')}</span></div>}
    {phase === 'denied' && <p className={styles.error} role="alert">{t('next.remoteSources.driveOAuthDenied')}</p>}
    <p className={styles.note}>{t('next.remoteSources.driveOAuthTestingNote')}</p>
    {phase === 'waiting' && <>
      <TextField label={t('next.remoteSources.driveOAuthPaste')} value={address} maxLength={8192} disabled={disabled}
        autoCapitalize="off" spellCheck={false} className={styles.monoInput} onChange={event => onAddress(event.target.value)}/>
      <Button size="compact" variant="glass" disabled={disabled || !address.trim()} onClick={onPaste}>{t('next.remoteSources.driveOAuthPasteAction')}</Button>
    </>}
    {error && <p className={styles.error} role="alert">{error}</p>}
  </>;
}
