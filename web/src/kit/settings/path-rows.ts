/** Settings rows that show a folder path, which a phone stacks under its label from the first paint (#615, #700).
 * A `folder` control always is one. Among read-only values the spec marks paths and short values alike as `mono`
 * (Server ID, Local port, Config file…), and those stay two-column as the frames draw them, so the read-only path
 * rows are named here. */
export const settingsReadonlyPathRows: ReadonlySet<string> = new Set(['storage.dataFolder', 'signin.dataFolder']);
export function settingsPathRow(id: string, controlType: string): boolean {
  return controlType === 'folder' || controlType === 'readonly' && settingsReadonlyPathRows.has(id);
}
