import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {ReadingSeriesFrame} from '../../../kit/frames/ReadingSeriesFrame';
import {ReadingItemMenu, ReadingSelectionToolbar} from '../../../kit/reading/ReadingActions';
import {SeriesHero} from '../../../kit/reading/SeriesParts';
import {StateView} from '../../../kit/states';
import {useTheme} from '../../../kit/theme';
import {readingVolumeKey, useReadSeries, type ReadSeriesProps} from '../../shared/read/useReadSeries';

const {t} = defaultI18n;

export function ReadSeriesPage(props: ReadSeriesProps) {
  const {route, comic, runtime, onBack} = props, theme = useTheme();
  const model = useReadSeries(props, theme.platform === 'phone'), {state, actions} = model;
  return <ReadingSeriesFrame testID={`native-read-series-${route.seriesId}`} title={model.title}
    back={{label: t('action.back'), onPress: onBack, testID: 'read-series-back'}} backdrop={model.backdrop}
    collection={model.collection} getKey={readingVolumeKey} ready={state.phase === 'ready' || state.phase === 'loading' && state.items.length > 0}
    immediate={state.phase === 'ready'} error={state.phase === 'error' ? <StateView {...runtime.error(state.error, model.retry, 'library')}/> : undefined}
    hero={onHeight => <SeriesHero title={model.title} author={model.author} facts={model.facts} volumes={model.covers}
      direction={comic && (state.direction === 'rtl' || state.direction === 'ltr') ? state.direction : undefined}
      progression={model.progression} readable={!!model.readable} readLabel={model.readLabel} onHeight={onHeight}
      runtime={runtime} onRead={() => {if (model.readable) model.open(model.readable);}}/>}
    selection={<ReadingSelectionToolbar port={actions} runtime={runtime}/>}
    menu={<ReadingItemMenu port={actions} runtime={runtime}/>} heading={t(comic ? 'next.reader.volumes' : 'next.kit.booksInSeries')}
    count={model.count} continuedId={model.continuedId} progression={model.progression} comic={comic} runtime={runtime}
    open={model.open} offer={model.offer} pick={actions.selection} onMenu={offer => actions.actions.show(offer)}
    notice={<>
      {!state.items.length && state.phase === 'ready' && <StateView testID="native-read-series-empty" variant="inline"
        title={t('library.emptySeries')} body={t('library.emptySeriesBody')}/>}
      {!!state.moreError && <StateView {...runtime.error(state.moreError, model.more, 'library')}/>}
    </>}/>;
}
