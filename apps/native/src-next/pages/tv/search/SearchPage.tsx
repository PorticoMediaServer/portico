import React from 'react';
import {SearchFrame} from '../../../kit/frames';
import {TVSearchQuery, TVSearchResults, TVSearchSelectionArtwork, TVSearchVoice} from '../../../kit/search/TVSearchContent';
import {NativeTVKeyboard} from '../../../kit/search/TVKeyboard';
import {useTVSearch, type NativeTVSearchPageProps} from '../../shared/search/useTVSearch';

export function TVSearchPage(props: NativeTVSearchPageProps) {
  const {destination, runtime, explore, actions, selection, copy} = props;
  const {view, active, state, t, ids, scroll, keyboard, keyboardKey, setKeyboardRow, voice, beginVoice} = useTVSearch(props);
  return <SearchFrame visible={active.visible} testID="native-tv-search-screen"
    field={<TVSearchQuery text={state.text} label={t('web.shell.search')} placeholder={t('web.search.placeholder')}/>}
    keyboard={<>
      <NativeTVKeyboard id={ids.searchKeyboard} value={state.text} onRowFocus={setKeyboardRow}
        onChangeText={value => destination.setText(value)} activation={active} leftRegionId={ids.rail} rightRegionId={ids.searchResults}/>
      {!view.emptyAll && <TVSearchVoice focus={{id: `${ids.searchKeyboard}.voice`, enabled: active.visible && !!voice.begin,
        edges: {left: ids.rail, right: ids.searchResults, up: keyboard}}} title={t(copy.preferToTalk)} description={t(copy.voiceHint)} busy={voice.busy}
        onPress={beginVoice}/>}
    </>}
    list={<TVSearchResults {...{view, destination, runtime, explore, actions, selection, ids, keyboard, keyboardKey, scroll}}/>}
    overlay={<>{voice.field}{selection?.render(path => <TVSearchSelectionArtwork runtime={runtime} path={path}/>)}</>}/>;
}
