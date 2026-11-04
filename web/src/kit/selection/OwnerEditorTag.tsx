import {ServerAudienceTag} from '../shell';
import {usePhone} from '../overlays/shared';

const owner = Object.freeze({authority: 'owner' as const});
/** select.html frames 8 to 11: the owner's bulk editor sits under the Server mark and its audience line, the same
 * treatment as the shell's editor, so it can't be mistaken for a viewer's panel. Only the owner opens these editors. */
export function OwnerEditorTag() {
  return <ServerAudienceTag audience={owner} layout={usePhone() ? 'phone' : 'desktop'} />;
}
