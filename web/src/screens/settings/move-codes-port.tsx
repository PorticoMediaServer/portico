import {createContext, useContext, useMemo, useState, type ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import {Button} from '../../kit/controls/Button';

const t = defaultI18n.t;

/** The one receipt owner publishes this narrow port; the People table's row action
 * uses it. Nobody else mints or holds a code, and nothing here admits a person. */
export type MoveCodePort = Readonly<{
  /** The existing code's own expiry per account, only where the read proved one. */
  expires: ReadonlyMap<string, string>;
  /** The account currently being issued for, if any. */
  busy: string;
  make: (accountId: string) => void;
}>;
type Channel = Readonly<{current?: MoveCodePort; publish: (value?: MoveCodePort) => void}>;
const ChannelContext = createContext<Channel>({publish: () => {}});

export function MoveCodesPortProvider({children}: {children: ReactNode}) {
  const [current, setCurrent] = useState<MoveCodePort>();
  const channel = useMemo<Channel>(() => ({current, publish: setCurrent}), [current]);
  return <ChannelContext.Provider value={channel}>{children}</ChannelContext.Provider>;
}
/** The host publishes through this stable setter. */
export function useMoveCodePublisher() { return useContext(ChannelContext).publish; }

/** One waiting person's Make code / New code, in the row the frame draws it in. */
export function MoveCodeRowAction({accountId, hasCode = false}: {accountId: string; hasCode?: boolean}) {
  const port = useContext(ChannelContext).current;
  if (!port) return null;
  const expires = port.expires.get(accountId), existing = Boolean(expires) || hasCode;
  const label = existing ? t('accounts.move.newCode') : t('accounts.move.makeCode');
  return <Button size="xs" variant="glass" loading={port.busy === accountId} disabled={Boolean(port.busy)} aria-label={label}
    title={expires ? `${t('accounts.move.validUntil')} ${defaultI18n.date(expires)}` : hasCode ? t('accounts.move.hasCode') : t('accounts.move.noCodeYet')}
    onClick={() => port.make(accountId)}>{label}</Button>;
}
