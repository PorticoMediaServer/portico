import {ACCOUNT_SECURITY_EVENTS, type AccountNotification} from '@core/account-notifications.ts';
import type {MessageId} from '@i18n';
import type {IconName} from '../kit/Icon';
import {servicesI18n} from './i18n';

export function serviceNoticeCopy(item: AccountNotification, i18n: ReturnType<typeof servicesI18n>) {
  const {t} = i18n;
  let icon: IconName = 'i-bell', tone: 'blue' | 'green' | 'amber' = 'blue', title: string, body: string;
  switch (item.kind) {
    case 'new_device': icon = 'i-tv'; tone = 'amber'; title = t('web.account.notif.newDevice', {name: item.detail.name ?? ''}); body = t('web.account.notif.newDeviceBody'); break;
    case 'account_export_ready': icon = 'i-download'; tone = 'green'; title = t('web.account.notif.exportReady'); body = t('services.feed.exportBody'); break;
    case 'security_event': {
      icon = 'i-shield'; tone = 'blue';
      const event = item.detail.event;
      title = (ACCOUNT_SECURITY_EVENTS as readonly string[]).includes(event) ? t(`web.account.notif.event.${event}` as MessageId) : t('web.account.notif.security');
      body = t('web.account.notif.securityBody'); break;
    }
    case 'server_offline': icon = 'i-server'; tone = 'amber'; title = t('web.account.notif.serverOffline', {server: item.server?.name ?? ''}); body = t('web.account.notif.serverOfflineBody'); break;
    case 'signing_key_expiring': icon = 'i-lock'; tone = 'amber'; title = t('web.account.notif.keyExpiring'); body = t('web.account.notif.keyExpiringBody', {key: item.detail.keyId ?? '', date: item.detail.expiresAt ? i18n.date(item.detail.expiresAt) : ''}); break;
    default: title = t('web.account.notif.other'); body = '';
  }
  return {icon, tone, title, body};
}
