import React from 'react';
import {View, type NativeScrollEvent} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import {Button} from '../../../kit/controls';
import {MusicErrorView} from './MusicErrorView';
import type {MusicSection} from './paging';

/** Called only by real scroll/focus events, never by a mount/response loop. */
export function musicNearEnd(event: NativeScrollEvent, horizontal = false): boolean {
  return horizontal ? event.contentOffset.x + event.layoutMeasurement.width >= event.contentSize.width - 240
    : event.contentOffset.y + event.layoutMeasurement.height >= event.contentSize.height - 320;
}
export function MusicSectionControls<T>({section, next, previous, retry, refresh, id}: {
  section: MusicSection<T>; next: () => void; previous: () => void; retry: () => void; refresh: () => void; id: string;
}) {
  const failed=section.phase==='error'||section.phase==='refresh-required';
  const recovery=failed?presentError(section.error,'detail',{operation:'load'}).action:undefined;
  const canRecover=!failed||recovery==='try-again'||recovery==='refresh';
  const nextVisible=section.canNext||!!section.nextCursor;
  const nextAction=section.phase==='error'?retry:section.phase==='refresh-required'?refresh:next;
  const nextLabel=canRecover&&section.phase==='error'?'action.tryAgain':canRecover&&section.phase==='refresh-required'?'action.refresh':'next.layout.nextItems';
  return <View testID={`music-pages-${id}`} style={{gap: 12, marginTop: 12}}>
    {section.phase === 'error' || section.phase === 'refresh-required'
      ? <MusicErrorView testID={`music-error-${id}`} retryAction={!nextVisible} error={section.error} onRetry={section.phase === 'refresh-required' ? refresh : retry}/> : null}
    <View style={{flexDirection: 'row', gap: 16}}>
      {section.canPrevious ? <Button testID={`music-previous-${id}`} focusId={`music-previous-${id}`} focusableWhenDisabled disabled={section.phase !== 'ready'} variant="text" onPress={previous}>{defaultI18n.t('next.layout.previousItems')}</Button> : null}
      {nextVisible ? <Button testID={`music-next-${id}`} focusId={`music-next-${id}`} focusableWhenDisabled disabled={section.phase === 'loading'||!canRecover} variant="text" onPress={nextAction}>{defaultI18n.t(nextLabel)}</Button> : null}
    </View>
  </View>;
}
