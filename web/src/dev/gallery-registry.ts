import type {ComponentType} from 'react';
import type {MessageId} from '@i18n';
import familyLoaders from 'virtual:portico-kit-families';
export type GalleryCase = Readonly<{id: string; labelKey: MessageId; width: number; height: number; render: ComponentType}>;
export type GalleryFamily = Readonly<{id: string; labelKey: MessageId; cases: readonly GalleryCase[]}>;
export function defineGalleryFamily<const T extends GalleryFamily>(family: T): T { return family; }
export async function loadGalleryFamilies(onlyFamily?: string): Promise<readonly GalleryFamily[]> {
  const selected = onlyFamily === undefined ? familyLoaders : familyLoaders.filter(load => load.familyId === onlyFamily);
  const families = await Promise.all(selected.map(load => load().then(module => module.default)));
  const ids = new Set<string>();
  for (const family of families) {
    if (onlyFamily !== undefined && family.id !== onlyFamily) throw new Error('Selected gallery family does not match its public module.');
    if (!/^[a-z0-9][a-z0-9-]*$/.test(family.id) || ids.has(family.id)) throw new Error('Invalid or duplicate gallery family.');
    ids.add(family.id);
    const cases = new Set<string>();
    for (const item of family.cases) {
      if (!/^[a-z0-9][a-z0-9-]*$/.test(item.id) || cases.has(item.id) || !Number.isInteger(item.width) || !Number.isInteger(item.height) || item.width < 1 || item.height < 1) throw new Error('Invalid or duplicate gallery state.');
      cases.add(item.id);
    }
  }
  return families;
}
