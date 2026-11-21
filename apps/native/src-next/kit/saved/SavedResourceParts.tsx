import React from 'react';
import {Text, View} from 'react-native';
import {defaultI18n, type MessageId} from '../../../../../packages/i18n/src';
import type {PersonalResource} from '../../../../../packages/client-core/src/personal-saved';
import {savedVisibilityHelp} from '../../screens/saved/presentation';
import {Switch} from '../controls';
import {useDockClearance} from '../dock/DockClearance';
import {ToastHost, type ToastMessage} from '../overlays';
import {useTheme} from '../theme';

const t = defaultI18n.t;

/** How a saved view is sorted, in words ("Newest first" style labels from its first sort field). */
export function viewSortLabel(resource: PersonalResource): string {
  const field = resource.definition?.sort?.[0]?.field;
  return t(field ? sortFieldId(field) : 'web.saved.sort.other');
}

function sortFieldId(field: string): MessageId {
  if (field === 'title' || field === 'sort_title') return 'web.saved.sort.title';
  if (field === 'added' || field === 'added_at') return 'web.saved.sort.added';
  if (field === 'last_played' || field === 'recent') return 'web.saved.sort.recent';
  if (field === 'year') return 'web.saved.sort.year';
  if (field === 'duration') return 'web.saved.sort.duration';
  if (field === 'progress') return 'web.saved.sort.progress';
  return 'web.saved.sort.other';
}

/** collections 12a: "Visible to everyone on this server" with its switch and what it means, for the collection's owner. */
export function SavedResourceVisibility({resource, disabled, onChange}: {
  resource: PersonalResource; disabled?: boolean; onChange: (visible: boolean) => void;
}) {
  const theme = useTheme();
  return <View style={{gap: theme.space.xs}}>
    <Switch focusId="saved-visibility" checked={resource.visibility === 'server'} disabled={disabled} onCheckedChange={onChange}>
      {t('web.saved.visibleToAll')}
    </Switch>
    <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{savedVisibilityHelp(resource)}</Text>
  </View>;
}

/** The facts under a row menu's title: what it is, its library, the list it is in. */
export function SavedResourceMenuFacts({values}: {values: readonly (string | undefined)[]}) {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: theme.space.xs}}>
    {values.filter((part): part is string => !!part).map((part, index) =>
      <Text key={index} style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{part}</Text>)}
  </View>;
}

/** A saved view's About sheet: its sort, its note, and that the server re-runs it. */
export function SavedResourceViewInfo({resource}: {resource?: PersonalResource | null}) {
  const theme = useTheme();
  return <View style={{gap: theme.space.sm}}>
    {resource && <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{viewSortLabel(resource)}</Text>}
    {!!resource?.summary && <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{resource.summary}</Text>}
    <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{t('next.saved.viewsHelp')}</Text>
  </View>;
}

/** collections 11c: "Removed … from the playlist." with Undo, above the tab bar. */
export function SavedResourceUndo({toast, onDismiss}: {toast: ToastMessage | null; onDismiss: () => void}) {
  const dockTop = useDockClearance();
  return <ToastHost toast={toast} onDismiss={onDismiss} bottomOffset={0} obstructionTop={dockTop}/>;
}
