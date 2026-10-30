import {useCallback, useMemo, useSyncExternalStore} from 'react';
import {runAccountExport} from '@core/account-notifications.ts';
import {ServicesGroup, ServicesGroupNote, ServicesWorkspaceRow} from '../kit/accounts/ServicesWorkspace';
import {HostedError, ServicesQuiet} from '../kit/accounts/ServicesAuth';
import {Button} from '../kit/controls';
import {accountIdentity, sameAccount, type AccountIdentity, type ServicesAccount} from './session';
import {servicesI18n} from './i18n';
import {useAccountResource} from './use-account-resource';
import type {WorkspaceApi} from './workspace-api';
import {AccountExportDownload, handoffAccountExport} from './account-export-download';

export function AccountExportRow({account, api, identity}: {account: ServicesAccount; api: WorkspaceApi; identity: AccountIdentity}) {
  const create = useCallback(() => new AccountExportDownload(
    signal => runAccountExport(api.notifications, crypto.randomUUID(), {signal}), handoffAccountExport,
    () => {
      const session = account.service.getSnapshot().session;
      return !!session && sameAccount(accountIdentity(session), identity);
    }), [account, api, identity]);
  const bound = useAccountResource(create);
  return bound ? <ExportBody key={bound.id} download={bound.value}/> : null;
}
function ExportBody({download}: {download: AccountExportDownload}) {
  const {t} = useMemo(servicesI18n, []);
  const state = useSyncExternalStore(download.subscribe, download.getSnapshot, download.getSnapshot);
  return <>
    <ServicesGroup title={t('services.security.yourData')}><ServicesWorkspaceRow phoneIcon="i-file" title={t('web.account.export.button')} help={t('web.account.export.help')}>
      <Button size="small" icon="i-download" loading={state.phase === 'exporting'} onClick={() => void download.download(state)}>{t('web.account.export.button')}</Button>
    </ServicesWorkspaceRow></ServicesGroup>
    {state.phase === 'pending' && <ServicesGroupNote><ServicesQuiet>{t('web.account.export.preparing')}</ServicesQuiet></ServicesGroupNote>}
    {state.phase === 'downloaded' && <ServicesGroupNote><ServicesQuiet>{t('web.account.export.downloaded')}</ServicesQuiet></ServicesGroupNote>}
    {state.phase === 'failed' && <ServicesGroupNote><HostedError error={state.error}/></ServicesGroupNote>}
  </>;
}
