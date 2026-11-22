import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {AmbientArt} from '../../../kit/artwork';
import {ReadingPeriodicalFrame} from '../../../kit/frames/ReadingPeriodicalFrame';
import {TitleFrame} from '../../../kit/frames';
import {HeroMetadata} from '../../../kit/layout';
import {usePageWidth} from '../../../kit/layout/page-inset';
import {MagazineIssues, NewspaperEditions, PeriodicalCover, PeriodicalHero} from '../../../kit/reading/PeriodicalParts';
import {ReadingItemMenu} from '../../../kit/reading/ReadingActions';
import {StateView} from '../../../kit/states';
import {useTheme} from '../../../kit/theme';
import {useMagazineHead} from '../../shared/read/useMagazineHead';
import {useReadPeriodical, type ReadPeriodicalProps} from '../../shared/read/useReadPeriodical';

const {t} = defaultI18n;

/** A magazine's or newspaper's archive. A magazine on a phone or tablet is a title page (phone-read 24): the latest
 * issue's cover over its glow, Magazine over the name, its facts, Read with that issue, My List and ⋯, then the year
 * chips with Latest first and the issues under each year. A newspaper, and TV, keep the archive frame. */
export function ReadPeriodicalPage(props: ReadPeriodicalProps) {
  const {route, runtime, onBack} = props;
  const {state, actions, archive, open, offer, magazine, selected, library, backdrop} = useReadPeriodical(props);
  const theme = useTheme(), width = usePageWidth(), tablet = theme.platform !== 'phone';
  const head = useMagazineHead({state, issue: selected, offer, actions, runtime, onOpen: open});
  const common = {state, runtime, actions, onOpen: open, offer};
  const ready = state.phase === 'ready' || state.phase === 'loading' && state.items.length > 0;
  const back = {label: t('action.back'), onPress: onBack};
  if (magazine && !theme.isTV && state.phase === 'ready') return <>
    <TitleFrame testID={`native-read-periodical-${route.groupId}`} back={back} topAction={head.more}
      art={<AmbientArt treatment="page" source={backdrop}/>}
      cover={{shape: 'poster', node: size => <PeriodicalCover issue={selected} title={state.groupName ?? ''} width={size} height={size * 1.5} runtime={runtime}/>}}
      kicker={library ? {kind: t('next.reader.magazine'), label: library.name, onPress: library.open} : {label: t('next.reader.magazine')}} title={state.groupName ?? ''} metadata={<HeroMetadata facts={head.facts}/>}
      primary={head.primary && {icon: 'i-book', ...head.primary, testID: 'read-periodical-read'}}
      actions={[...(head.listed ? [head.listed] : []), ...(head.more ? [head.more] : [])]}
      scroller={(content, inset, collapse) => <MagazineIssues {...common} theme={theme} tablet={tablet} width={Math.min(1180, width)} archive={archive}
        hero={content} scroll={{inset, collapse}}/>}/>
    <ReadingItemMenu port={actions} runtime={runtime}/>
  </>;
  return <ReadingPeriodicalFrame testID={`native-read-periodical-${route.groupId}`} back={back}
    magazine={magazine} ready={ready}
    state={state} backdrop={backdrop} onNext={archive.next}
    error={state.phase === 'error' ? <StateView {...runtime.error(state.error, archive.retry, 'library')}/> : undefined}
    nextError={!magazine && state.phase !== 'error' && state.error ? <StateView {...runtime.error(state.error, archive.next, 'library')}/> : undefined}
    hero={layout => <PeriodicalHero {...common} {...layout} issue={selected} magazine={magazine}/>}
    body={layout => magazine ? <MagazineIssues {...common} {...layout} archive={archive}/> : <NewspaperEditions {...common} {...layout} archive={archive}/>}
    menu={<ReadingItemMenu port={actions} runtime={runtime}/>}/>;
}
