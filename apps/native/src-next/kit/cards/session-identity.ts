import type {FactPart} from '../../../../../packages/client-core/src/presentation/kit-model/index';

/** Already-authorized display identity. name always retains the actual profile label. */
export type SessionViewer = Readonly<{name: string; username?: string; device: string}>;

/** Match the shell's trim/case-insensitive comparison without inferring account identity. */
export function sessionViewerParts(viewer: SessionViewer): readonly FactPart[] {
  const username = viewer.username?.trim() ? viewer.username : undefined;
  const profile = username && viewer.name.trim() && viewer.name.trim().toLocaleLowerCase() !== username.trim().toLocaleLowerCase() ? viewer.name : undefined;
  return [
    {id: 'viewer', text: username ?? viewer.name, role: 'identity'},
    ...(profile ? [{id: 'profile', text: profile, role: 'context'} as const] : []),
    {id: 'device', text: viewer.device, role: 'context'},
  ];
}
