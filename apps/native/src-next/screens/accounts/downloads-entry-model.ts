export type AccountDownloadsSnapshot = Readonly<{available: boolean}>;
export type AccountDownloadsEntry = Readonly<{
  getSnapshot: () => AccountDownloadsSnapshot;
  subscribe: (listener: () => void) => () => void;
  current: () => boolean;
  open: () => void;
  dispose: () => void;
}>;
export const noAccountDownloads: AccountDownloadsSnapshot = Object.freeze({available: false});
