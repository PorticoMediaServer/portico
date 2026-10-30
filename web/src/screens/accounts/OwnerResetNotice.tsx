import {defaultI18n} from '@i18n';
import {ServicesNotice} from '../../kit/accounts/ServicesAuth';
import type {IconName} from '../../kit/Icon';

const t = defaultI18n.t;

type Kind = 'invalid' | 'expired' | 'limited' | 'write';

const drawn: Record<Kind, {title: Parameters<typeof t>[0]; body: Parameters<typeof t>[0]; tone: 'warning' | 'error'; icon: IconName}> = {
  invalid: {title: 'accounts.recovery.invalidTitle', body: 'accounts.recovery.invalidBody', tone: 'error', icon: 'i-warn'},
  expired: {title: 'accounts.recovery.expiredTitle', body: 'accounts.recovery.expiredBody', tone: 'warning', icon: 'i-clock'},
  limited: {title: 'accounts.recovery.limitedTitle', body: 'accounts.recovery.limitedBody', tone: 'warning', icon: 'i-clock'},
  write: {title: 'accounts.recovery.writeTitle', body: 'accounts.recovery.writeBody', tone: 'error', icon: 'i-warn'},
};

/**
 * auth20f: the four closed answers of the owner reset-code page, as one drawn
 * message. The kind is supplied by the owner's own classification of the original
 * reset endpoint; this file infers nothing, reads no status, carries no file path,
 * code, account data, request, timer or callback. Copy comes from ACC-LEAF-10 and
 * the message uses the existing notice kit: danger for wrong/used/unknown and an
 * unwritable file, amber with a clock for expired and rate-limited.
 */
export function OwnerResetNotice({kind}: {kind: Kind}) {
  const copy = drawn[kind];
  return <ServicesNotice tone={copy.tone} icon={copy.icon}><b>{t(copy.title)}</b> {t(copy.body)}</ServicesNotice>;
}
