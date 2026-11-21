import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {ReadingPeriodicalFrame} from '../../../kit/frames/ReadingPeriodicalFrame';
import {MagazineIssues, NewspaperEditions, PeriodicalHero} from '../../../kit/reading/PeriodicalParts';
import {ReadingItemMenu} from '../../../kit/reading/ReadingActions';
import {StateView} from '../../../kit/states';
import {useReadPeriodical, type ReadPeriodicalProps} from '../../shared/read/useReadPeriodical';

const {t} = defaultI18n;

export function ReadPeriodicalPage(props: ReadPeriodicalProps) {
  const {route, runtime, onBack} = props;
  const {state, actions, archive, open, offer, magazine, selected, backdrop} = useReadPeriodical(props);
  const common = {state, runtime, actions, onOpen: open, offer};
  return <ReadingPeriodicalFrame testID={`native-read-periodical-${route.groupId}`} back={{label: t('action.back'), onPress: onBack}}
    magazine={magazine} ready={state.phase === 'ready' || state.phase === 'loading' && state.items.length > 0}
    state={state} backdrop={backdrop} onNext={archive.next}
    error={state.phase === 'error' ? <StateView {...runtime.error(state.error, archive.retry, 'library')}/> : undefined}
    nextError={!magazine && state.phase !== 'error' && state.error ? <StateView {...runtime.error(state.error, archive.next, 'library')}/> : undefined}
    hero={layout => <PeriodicalHero {...common} {...layout} issue={selected} magazine={magazine}/>}
    body={layout => magazine ? <MagazineIssues {...common} {...layout} archive={archive}/> : <NewspaperEditions {...common} {...layout} archive={archive}/>}
    menu={<ReadingItemMenu port={actions} runtime={runtime}/>}/>;
}
