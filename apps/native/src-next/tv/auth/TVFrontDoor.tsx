import React, {useEffect, useRef} from 'react';
import {tvAccountSpec} from '@portico/design';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Button} from '../../kit/controls';
import {FocusProvider, FocusRegion} from '../../kit/focus';
import {useDestinationEntryFocus} from '../../screens/destinations/useEntryFocus';
import {useTVBusyEntryRevision} from './entry-revision';
import {TVAuthLayout} from './TVAuthLayout';
import {TVNoCodeCard} from './TVCodePresenter';
import {useCodeRetry} from './code-retry';

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
 * and what to do. While it shows it asks for a code again every 30 seconds, one request each time, so its sentence holds with or without a reason. */
export function TVFrontDoor(props: TVFrontDoorProps) {
  return <FocusProvider><FrontDoor {...props}/></FocusProvider>;
}
function FrontDoor({busy, reason, autoStart = false, onPortico, onLocal}: TVFrontDoorProps) {
  const t = defaultI18n.t;
  const start = useRef({autoStart, onPortico}); start.current = {autoStart, onPortico};
  useEffect(() => {if (start.current.autoStart) start.current.onPortico();}, []);
  useCodeRetry(!busy, onPortico);
  const id = 'tv.auth.front-door';
  const entryRevision = useTVBusyEntryRevision(busy);
  useDestinationEntryFocus({visible: !busy, intent: 'enter', revision: entryRevision}, {regionId: id, childId: 'portico'});
  const card = <TVNoCodeCard reason={reason}/>;
  return <TVAuthLayout testID="tv-front-door" portico title={t('activation.title')} body={t('accounts.consent.portico')} aside={card}>
    <FocusRegion id={id} entry="portico" order={['portico', 'local']} orientation="horizontal" enabled={!busy}
      style={{flexDirection: 'row', gap: tvAccountSpec.actionGap}}>
      <Button focusId="portico" testID="tv-front-door-portico" disabled={busy} onPress={() => {if (!busy) onPortico();}}>{t('accounts.signIn.porticoOnTV')}</Button>
      <Button focusId="local" testID="tv-front-door-local" variant="glass" disabled={busy} onPress={() => {if (!busy) onLocal();}}>{t('auth.signInDirectly')}</Button>
    </FocusRegion>
  </TVAuthLayout>;
}
