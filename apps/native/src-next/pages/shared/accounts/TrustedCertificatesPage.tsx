import React from 'react';
import type {View} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import {ForgetCertificateDialog, TrustedCertificateRow, TrustedCertificatesGroup} from '../../../kit/accounts/TrustedCertificatesParts';
import {useTrustedCertificates} from './useTrustedCertificates';

/** The device's trusted certificates, on the server chooser (phone and tablet) and on a TV's Switch server Settings page.
 * `refreshRef` and `refreshDown`: on TV the chooser names Up into this Refresh and Down out of it to Refresh servers
 * (Apple TV moves by position, and the two don't line up). Nothing is drawn while there are none. */
export function TrustedCertificatesPage({context, refreshRef, refreshDown}: {context?: NativeShellBootstrapContext; refreshRef?: React.Ref<View>; refreshDown?: number}) {
  const t = defaultI18n.t, certificates = useTrustedCertificates(context);
  if (!certificates.rows.length && !certificates.error) return null;
  return <TrustedCertificatesGroup title={t('connection.certificates.title')} refreshLabel={t('accounts.servers.refresh')} busy={certificates.busy}
    error={certificates.error != null ? presentError(certificates.error, 'account').body : undefined} onRefresh={certificates.refresh}
    refreshRef={refreshRef} refreshDown={refreshDown}
    after={<ForgetCertificateDialog open={!!certificates.captured} title={t('connection.certificates.confirm')}
        body={t('connection.certificates.body', {address: certificates.captured?.address ?? ''})} fingerprintLabel={t('connection.certificates.fingerprint')}
        fingerprint={certificates.captured?.fingerprint} cancelLabel={t('action.cancel')} forgetLabel={t('connection.certificates.forget')}
        busy={certificates.busy} cancelRef={certificates.cancelFocus} onClose={certificates.close} onConfirm={certificates.confirm}/>}>
    {certificates.rows.map(value => <TrustedCertificateRow key={value.address} address={value.address} fingerprint={value.fingerprint}
      forgetLabel={t('connection.certificates.forget')} busy={certificates.busy} onForget={() => certificates.ask(value)}/>)}
  </TrustedCertificatesGroup>;
}
