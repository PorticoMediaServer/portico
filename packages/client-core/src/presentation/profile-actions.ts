/**
 * The profile actions menu (CON-12): one model for web Settings › Account,
 * web Server › People and Apple Settings › Account. The same profile shows
 * the same menu in the same order: Change picture… · Remove picture ·
 * Rename… · Limits and permissions… · Change PIN… / Forgot PIN… ·
 * Forget remembered devices · Delete profile… (destructive). Only permitted
 * items are returned; pages hide items they have no handler for (never dead UI).
 */
import type {IconId} from '../../../design/src/icons.ts';
import type {MessageId} from '../../../i18n/src/index.ts';

export type ProfileActionId =
  | 'picture'
  | 'removePicture'
  | 'rename'
  | 'limits'
  | 'changePin'
  | 'forgotPin'
  | 'forgetDevices'
  | 'delete';

export type ProfileAction = Readonly<{
  id: ProfileActionId;
  label: MessageId;
  icon: IconId;
  destructive?: boolean;
  /** Opens a further step: "…" is already in the label (CON-22). */
  furtherStep?: boolean;
}>;

export type ProfileRef = Readonly<{
  hasPicture?: boolean;
  pinRequired?: boolean;
  primary?: boolean;
}>;

export type ProfileViewer = Readonly<{
  canManage?: boolean;
  isCurrent?: boolean;
}>;

export function profileActions(profile: ProfileRef, viewer?: ProfileViewer): readonly ProfileAction[] {
  const canManage = !!viewer?.canManage;
  const isCurrent = !!viewer?.isCurrent;
  const out: ProfileAction[] = [];
  if (canManage || isCurrent) {
    out.push({
      id: 'picture',
      label: profile.hasPicture ? 'profile.changePicture' : 'profile.addPicture',
      icon: 'camera',
      furtherStep: true,
    });
  }
  if (profile.hasPicture && (canManage || isCurrent)) {
    out.push({id: 'removePicture', label: 'profile.removePicture', icon: 'image'});
  }
  if (canManage) {
    out.push({id: 'rename', label: 'profile.rename', icon: 'edit', furtherStep: true});
  }
  // A profile may look at its own limits; only a managing profile may open (and change) another's.
  if (canManage || isCurrent) out.push({id: 'limits', label: 'profile.limits', icon: 'shield', furtherStep: true});
  if (canManage) {
    out.push({
      id: 'changePin',
      label: profile.pinRequired ? 'profile.changePin' : 'profile.setPin',
      icon: 'lock',
      furtherStep: true,
    });
  } else if (profile.pinRequired) {
    out.push({id: 'forgotPin', label: 'profile.forgotPin', icon: 'lock', furtherStep: true});
  }
  if (canManage || isCurrent) {
    out.push({id: 'forgetDevices', label: 'profile.forgetDevices', icon: 'shield'});
  }
  // Deleting a profile is managing it: not offered to a profile that cannot.
  if (!profile.primary && canManage) {
    out.push({id: 'delete', label: 'profile.delete', icon: 'trash', destructive: true, furtherStep: true});
  }
  return Object.freeze(out);
}
