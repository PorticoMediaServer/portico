import React, {useEffect, useRef} from 'react';
import {Text} from 'react-native';
import {tvAccountSpec} from '@portico/design';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Button} from '../../kit/controls';
import {FocusProvider, FocusRegion} from '../../kit/focus';
import {useDestinationEntryFocus} from '../../screens/destinations/useEntryFocus';
import {useTVBusyEntryRevision} from './entry-revision';
import {TVAuthLayout} from './TVAuthLayout';
import {TVCodeCard} from './TVCodePresenter';
import {nativeType, useTheme} from '../../kit/theme';

export type TVFrontDoorProps = Readonly<{
  busy: boolean;
  /** Why no code could be fetched, in plain words; shown in the code card. */
  reason?: string;
  /** Start the activation code at once: the code screen is the TV's first screen. */
  autoStart?: boolean;
  onPortico: () => void; onLocal: () => void;
}>;

/** The TV's Activate screen when no code could be fetched (the code screen is the first screen otherwise). It keeps
 * the same composition: the title and help line, the two ways in, and the code card, which says why there is no code
 * and what to do. While it shows a reason it asks for a code again every 30 seconds, one request each time. */
export function TVFrontDoor(props: TVFrontDoorProps) {
  return <FocusProvider><FrontDoor {...props}/></FocusProvider>;
}
const RETRY_MS = 30_000;
function FrontDoor({busy, reason, autoStart = false, onPortico, onLocal}: TVFrontDoorProps) {
  const theme = useTheme(), t = defaultI18n.t;
  const start = useRef({autoStart, onPortico}); start.current = {autoStart, onPortico};
  useEffect(() => {if (start.current.autoStart) start.current.onPortico();}, []);
  useEffect(() => {
    if (!reason || busy) return;
    const timer = setTimeout(() => start.current.onPortico(), RETRY_MS);
    return () => clearTimeout(timer);
  }, [reason, busy]);
  const id = 'tv.auth.front-door';
  const entryRevision = useTVBusyEntryRevision(busy);
  useDestinationEntryFocus({visible: !busy, intent: 'enter', revision: entryRevision}, {regionId: id, childId: 'portico'});
  const type = (size: number, weight: 400 | 700 = 400) => nativeType({size, weight, tracking: 0, lineHeight: size * 1.45}, theme.textScale);
  const card = <TVCodeCard>
    <Text accessibilityRole="alert" style={[type(30, 700), {color: theme.colors.text, textAlign: 'center', marginBottom: 16}]}>{reason ?? t('activation.preparingCode')}</Text>
    <Text style={[type(22), {color: theme.colors.textSecondary, textAlign: 'center'}]}>{t('activation.noCodeHelp')}</Text>
  </TVCodeCard>;
  return <TVAuthLayout testID="tv-front-door" portico title={t('activation.title')} body={t('accounts.consent.portico')} aside={card}>
    <FocusRegion id={id} entry="portico" order={['portico', 'local']} orientation="horizontal" enabled={!busy}
      style={{flexDirection: 'row', gap: tvAccountSpec.actionGap}}>
      <Button focusId="portico" testID="tv-front-door-portico" disabled={busy} onPress={() => {if (!busy) onPortico();}}>{t('accounts.signIn.porticoOnTV')}</Button>
      <Button focusId="local" testID="tv-front-door-local" variant="glass" disabled={busy} onPress={() => {if (!busy) onLocal();}}>{t('auth.signInDirectly')}</Button>
    </FocusRegion>
  </TVAuthLayout>;
}
