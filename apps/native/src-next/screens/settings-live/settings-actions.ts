/** The one decision each Settings action row makes, kept out of the renderer so it is
 * checkable without mounting React Native: two rows call an existing port, the rest are
 * confirmations owned by the Settings controller. */
export type NativeSettingsActionTarget = 'linkTV' | 'customizeHome' | 'quality' | 'confirm';

export function nativeSettingsActionTarget(id: string): NativeSettingsActionTarget {
  return id === 'linkTV' ? 'linkTV' : id === 'customizeHome' ? 'customizeHome' : id === 'devicePlayerFixes' ? 'quality' : 'confirm';
}
