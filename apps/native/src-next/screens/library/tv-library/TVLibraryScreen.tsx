import React, {useMemo, useSyncExternalStore} from 'react';
import type {ReactNode} from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import {Button} from '../../../kit/controls';
import {useFocusRegistry} from '../../../kit/focus';
import {Banner, SidePanel} from '../../../kit/overlays';
import {alpha, browseType} from '../../../kit/browse/presentation';
import {useTheme} from '../../../kit/theme';
import {NativeTVKeyboard} from '../../search/TVKeyboard';
import {nativeTVNameEdit, nativeTVNamingFirstKey} from './naming';
import type {NativeTVLibraryNaming, NativeTVLibraryNamingStore} from './naming';

/** The drawn ten-foot name field (tv.html frame 5 `.sfield`) with its own caret
 * (ruling 27.2): 500x80, radius 16, 8% ink wash, 34/700 value, 3x40 accent caret. */
const tvNamingField = {width: 500, height: 80, borderRadius: 16, paddingHorizontal: 24, flexDirection: 'row' as const, alignItems: 'center' as const, gap: 16};

/** Ruling 37 in the approved TV style: the filters panel's own 680px side
 * surface (tv.html frame 4) carries today's SaveViewDialog wording and fields,
 * with the F-LIB-52 name keyboard in place of the platform text input. */
export function NativeTVLibrarySaveViewPanel({naming, id}: {naming: NativeTVLibraryNaming; id: string}) {
  const theme = useTheme(), registry = useFocusRegistry();
  const state = useSyncExternalStore(naming.input.subscribe, naming.input.getSnapshot);
  const form = state.form, keyboardId = `${id}.keyboard`, fieldId = `${id}.field`;
  const first = useMemo(() => nativeTVNamingFirstKey(keyboardId), [keyboardId]);
  const initialFocusRef = useMemo(() => ({current: {focus: () => {registry.focus(first);}}}), [registry, first]);
  const busy = form.busy, blocked = busy || form.nameLocked;
  const edit = (next: string) => {const name = nativeTVNameEdit(form, state.keyboard, next); if (name !== undefined) naming.controller.setName(name);};
  return <SidePanel testID={id} open={form.open} onOpenChange={open => {if (!open) naming.close();}} showClose={false} dismissOnBack={!busy}
    title={defaultI18n.t('lib.saveViewTitle')} description={defaultI18n.t('lib.saveViewLede')}
    titleType={{size: 44, weight: 800, tracking: -.03, lineHeight: 52.8}}
    width={680} topInset={0} initialFocusRef={initialFocusRef}
    style={{paddingTop: 42, paddingLeft: 56, paddingRight: 96, paddingBottom: 56}}
    footer={<View style={{flexDirection: 'row', gap: 16}}>
      <Button testID={`${id}-cancel`} focusId="cancel" variant="glass" disabled={busy} onPress={() => {naming.close();}}>{defaultI18n.t('action.cancel')}</Button>
      <Button testID={`${id}-save`} focusId="save-view" variant="primary" loading={busy} disabled={blocked || !form.name.trim()}
        onPress={() => {void naming.controller.save();}}>{defaultI18n.t('lib.saveView')}</Button>
    </View>}>
    <View style={{gap: 24}}>
      {form.error !== undefined && <Banner testID={`${id}-error`} variant="notice" tone="error" placement="inline"
        message={presentError(form.error, 'library', {operation: 'save'}).body}/>}
      <View style={{gap: 10}}>
        <Text style={[browseType(theme, 20, 700), {color: theme.colors.textTertiary}]}>{defaultI18n.t('lib.saveViewName')}</Text>
        <View testID={fieldId} accessibilityRole="text" accessibilityLabel={defaultI18n.t('lib.saveViewName')}
          style={[tvNamingField, {backgroundColor: alpha(theme.colors.text, .08)}]}>
          <Text numberOfLines={1} style={[browseType(theme, 34, 700), {color: form.name ? theme.colors.text : theme.colors.textTertiary, flexShrink: 1}]}>
            {form.name || defaultI18n.t('lib.saveViewPlaceholder')}</Text>
          <View testID={`${fieldId}-caret`} accessible={false} importantForAccessibility="no" pointerEvents="none"
            style={{width: 3, height: 40, borderRadius: 2, marginLeft: -12, backgroundColor: theme.colors.accentStrong}}/>
        </View>
      </View>
      <NativeTVKeyboard id={keyboardId} testID={`${id}-keyboard`} width={500} value={form.name} onChangeText={edit}
        activation={state.keyboard} leftRegionId={keyboardId}/>
      <Text style={[browseType(theme, 20, 400, 0, 33), {color: theme.colors.textTertiary}]}>{defaultI18n.t('lib.saveViewHelp')}</Text>
    </View>
  </SidePanel>;
}

/** The TV Library surface: the genuine library page with the one TV-only
 * overlay it owns. Nothing here replaces the library's own grid, toolbar or
 * filters, which keep their single mounted controller. */
export function NativeTVLibrarySurface({library, naming, panelId}: {
  library: ReactNode; naming: NativeTVLibraryNamingStore; panelId: string;
}) {
  const current = useSyncExternalStore(naming.subscribe, naming.getSnapshot);
  // The closed opening stays mounted, exactly as the filter panel does, so its
  // own exit motion and focus return finish; the store retires it on the next
  // opening or when the page's authority ends.
  return <>{library}{current && <NativeTVLibrarySaveViewPanel key={panelId} naming={current} id={panelId}/>}</>;
}
