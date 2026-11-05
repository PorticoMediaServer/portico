import {AttachmentSelectionTransferQueue} from '@core/attachment-selection-transfers.ts';
export type {BrowserSelectionActionApi, SelectionActionTransfers} from '@core/attachment-selection-transfers.ts';
import type {BrowserSelectionActionOptions as CoreOptions} from '@core/attachment-selection-transfers.ts';
export type BrowserSelectionActionOptions = Omit<CoreOptions, 'handoff'> & Partial<Pick<CoreOptions, 'handoff'>>;
function attachment(url: string, filename: string) {
  const anchor = document.createElement('a');
  anchor.href = url; anchor.download = filename; anchor.rel = 'noreferrer'; anchor.referrerPolicy = 'no-referrer';
  document.body.append(anchor); try {anchor.click();} finally {anchor.remove();}
}
export class SelectionActionTransferQueue extends AttachmentSelectionTransferQueue {
  constructor(options: BrowserSelectionActionOptions) {super({...options, handoff: options.handoff ?? attachment});}
}
