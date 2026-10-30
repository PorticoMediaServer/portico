import React, {useMemo} from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Focusable} from '../../kit/controls';
import {FocusRegion, useFocusRegistry} from '../../kit/focus';
import {SidePanel} from '../../kit/overlays';
import {alpha, browseType} from '../../kit/browse/presentation';
import {useTheme} from '../../kit/theme';
import type {JumpLetter} from './jump';

/** tv-focus 20 / F-LIB-49: the Jump to sheet holds focus and opens on the
 * letter of the card in view; Back returns to the toolbar's Jump to. */
export function LibraryJumpSheet({open, panelId, letters, selected, onSelect, onClose}: {
  open: boolean; panelId: string; letters: readonly JumpLetter[]; selected?: string;
  onSelect: (key: string) => void; onClose: () => void;
}) {
  const theme = useTheme(), registry = useFocusRegistry();
  const first = selected ?? letters[0]?.key, lettersId = `${panelId}.letters`;
  const initialFocusRef = useMemo(() => ({current: {focus: () => {
    if (first) registry.focus({regionId: lettersId, childId: `jump:${first}`});
  }}}), [registry, lettersId, first]);
  return <SidePanel testID={panelId} open={open} onOpenChange={next => {if (!next) onClose();}} showClose={false}
    title={defaultI18n.t('next.browse.native.jumpTo')} width={680} topInset={0} initialFocusRef={initialFocusRef}
    titleType={{size: 44, weight: 800, tracking: -.03, lineHeight: 52.8}} style={{paddingTop: 42, paddingLeft: 56, paddingRight: 96, paddingBottom: 56}}>
    <FocusRegion id={lettersId} entry={first ? `jump:${first}` : undefined} order={letters.map(letter => `jump:${letter.key}`)} orientation="vertical">
      <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 12}}>
        {letters.map(letter => {
          const active = letter.key === selected;
          return <Focusable key={letter.key} focusId={`jump:${letter.key}`} testID={`${panelId}-${letter.key}`} accessibilityRole="button"
            accessibilityLabel={letter.key} accessibilityState={{selected: active}} onPress={() => onSelect(letter.key)} focusRadius={14}
            style={{height: 52, minWidth: 64, paddingHorizontal: 16, borderRadius: 14, alignItems: 'center', justifyContent: 'center',
              backgroundColor: active ? alpha(theme.colors.accent, .18) : alpha(theme.colors.text, .06),
              borderWidth: active ? 1.5 : 0, borderColor: alpha(theme.colors.accentStrong, .5)}}>
            <Text style={[browseType(theme, 22, 700), {color: active ? theme.colors.text : theme.colors.textSecondary}]}>{letter.key}</Text>
          </Focusable>;
        })}
      </View>
    </FocusRegion>
  </SidePanel>;
}
