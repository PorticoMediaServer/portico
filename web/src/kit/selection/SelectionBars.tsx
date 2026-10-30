import {PhoneActionBar, SelectionBar} from './SelectionBar';
import {usePhoneSelectionContent, usePublishPhoneSelection} from './phone-slot';
import type {SelectionBarProps} from './types';

/** The selection's drawn surfaces: the bar in the screen's toolbar slot (whose own
 * phone header becomes the top bar), and the phone action bar, which the shell
 * mounts in place of its tab bar. Both read the same selection and actions; this
 * publishes only the live count and actions, never an element snapshot. */
export function SelectionBars(props: SelectionBarProps) {
  usePublishPhoneSelection({selection: props.selection, actions: props.actions});
  return <SelectionBar {...props}/>;
}

/** Mounted by the shell in the tab bar's place while a screen selection is live. */
export function PhoneSelectionSlot() {
  const content = usePhoneSelectionContent();
  return content ? <PhoneActionBar selection={content.selection} actions={content.actions} episodeSelection={content.selection.kind === 'episode'} /> : null;
}
