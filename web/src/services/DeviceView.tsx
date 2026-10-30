import {useMemo} from 'react';
import {normalizeSetupCode, type SetupPreview} from '@core/setup-code.ts';
import type {MessageId} from '@i18n';
import {HostedError, ServicesActions, ServicesAuthStage, ServicesDeviceAccount, ServicesDeviceAnother, ServicesDeviceCode, ServicesDeviceCodeHelp, ServicesDeviceName, ServicesDeviceWarning, ServicesFacts, ServicesHeading, ServicesLead, ServicesNotice, ServicesQuiet, ServicesStateIcon} from '../kit/accounts/ServicesAuth';
import {Button, TextField} from '../kit/controls';
import {servicesI18n} from './i18n';
import styles from './services.module.css';

const platforms: Partial<Record<string, MessageId>> = {ios: 'services.platform.ios', tvos: 'services.platform.tvos', android: 'services.platform.android', androidtv: 'services.platform.androidtv', firetv: 'services.platform.firetv', web: 'services.platform.web', macos: 'services.platform.macos'};
/** Presentation only. The controller retains the canonical account/family and device grant. */
export function DeviceView({name, code, fromQR = false, preview, busy, error, onCodeChange, onInspect, onDecision, onAnother, onSignOut}: {
  name: string; code: string; fromQR?: boolean; preview?: Pick<SetupPreview, 'deviceName' | 'platform' | 'userCode'>;
  busy: boolean; error?: unknown; onCodeChange: (code: string) => void; onInspect: () => void;
  onDecision: (decision: 'approve' | 'deny') => void; onAnother: () => void; onSignOut: () => void;
}) {
  const {t} = useMemo(servicesI18n, []);
  const errorCode = (error as {code?: string} | undefined)?.code;
  const notFound = [400, 404, 410, 422].includes((error as {status?: number} | undefined)?.status ?? 0);
  let complete = false; try { normalizeSetupCode(code); complete = true; } catch { /* incomplete field */ }
  return <ServicesAuthStage variant={preview ? 'deviceReview' : 'device'}><ServicesHeading singleLine>{t('web.device.title')}</ServicesHeading><ServicesLead>{t(preview ? 'services.device.reviewLead' : 'web.device.lede')}</ServicesLead>
    <ServicesDeviceAccount name={name} disabled={busy} onSignOut={onSignOut} />
    {error && !notFound ? errorCode === 'account_changed' || errorCode === 'timeout' ? <ServicesNotice icon={errorCode === 'timeout' ? 'i-clock' : 'i-warn'} tone={errorCode === 'timeout' ? 'warning' : 'error'}>{t(errorCode === 'account_changed' ? 'services.device.accountChanged' : 'services.device.timeout')}</ServicesNotice> : <HostedError error={error} /> : null}
    {preview ? <>
      <ServicesFacts items={[
        {id: 'device', label: t('services.device.device'), value: <ServicesDeviceName name={preview.deviceName} television={preview.platform === 'tvos' || preview.platform === 'androidtv' || preview.platform === 'firetv'} />},
        {id: 'platform', label: t('services.device.platform'), value: platforms[preview.platform] ? t(platforms[preview.platform]!) : preview.platform},
        {id: 'code', label: t('auth.code'), value: <ServicesDeviceCode>{preview.userCode}</ServicesDeviceCode>},
      ]} />
      <ServicesDeviceWarning>{t('web.device.approveWarn')}</ServicesDeviceWarning>
      <ServicesActions><Button variant="primary" loading={busy} onClick={() => onDecision('approve')}>{t('web.device.approveDevice')}</Button><Button disabled={busy} onClick={() => onDecision('deny')}>{t('services.device.deny')}</Button></ServicesActions>
      <ServicesDeviceAnother disabled={busy} onClick={onAnother} />
    </> : <><form className={styles.form} onSubmit={event => { event.preventDefault(); onInspect(); }}>
      <TextField variant="auth" label={t('auth.code')} name="code" autoComplete="one-time-code" autoCapitalize="characters" spellCheck={false} placeholder={t('services.device.codePlaceholder')} value={code} maxLength={12} onChange={event => onCodeChange(event.currentTarget.value.toUpperCase())} description={<ServicesDeviceCodeHelp fromQR={fromQR} />} error={notFound ? t('services.device.notFound') : undefined} disabled={busy} autoFocus={!fromQR} required />
      <ServicesActions><Button variant="primary" type="submit" loading={busy} disabled={!complete}>{t('web.device.review')}</Button></ServicesActions>
    </form><ServicesQuiet>{t('services.device.expiry')}</ServicesQuiet></>}
  </ServicesAuthStage>;
}

export function DeviceDecision({decision, onAnother}: {decision: 'approve' | 'deny'; onAnother: () => void}) {
  const {t} = useMemo(servicesI18n, []);
  return <ServicesAuthStage variant="state"><ServicesStateIcon tone={decision === 'approve' ? 'success' : 'info'} icon={decision === 'approve' ? 'i-check' : 'i-close'} /><ServicesHeading>{t(decision === 'approve' ? 'services.device.approvedTitle' : 'services.device.deniedTitle')}</ServicesHeading><ServicesLead>{t(decision === 'approve' ? 'services.device.approved' : 'services.device.denied')}</ServicesLead><ServicesActions><Button onClick={onAnother}>{t('web.device.anotherCode')}</Button></ServicesActions></ServicesAuthStage>;
}
