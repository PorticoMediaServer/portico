// Consumer-owned entry point: the project includes no reader-core source glob
// or ambient declarations. Public imports must carry their complete type graph.
export {ReaderController, createEpubAdapter, createPdfAdapter, createComicAdapter,
  loadLiterataFont, rasterInfo, createReadingController} from '@portico/reader-core';
export type {ReaderSource, ReaderLocation, PdfAdapterOptions, ComicAdapterOptions,
  ComicPageProvider, RasterInfo, ReadingController, ReadingControllerOptions} from '@portico/reader-core';
export {createReadingPublication,readerLocationFromReading,readingLocationFromReader} from '@portico/reader-core';
export type {ReaderSessionInput,ReadingPublication} from '@portico/reader-core';

import {rasterInfo} from '@portico/reader-core';
import type {ComicPage, RasterInfo} from '@portico/reader-core';
import {createReadingController} from '@portico/reader-core';
import type {ReadingControllerOptions} from '@portico/reader-core';
// A provider derives the returned variant's dimensions from bounded bytes,
// before any browser decoder, without importing adapter-private modules.
export function inspectedComicPage(index:number,name:string,bytes:Uint8Array):ComicPage {
  const image:RasterInfo=rasterInfo(bytes);
  return {index,name,bytes,image};
}

export function confirmedReader(options:ReadingControllerOptions) {
  const reader=createReadingController(options);
  void reader.updatePreferenceSnapshot(options.preferenceSnapshot);
  // Production hosts forward confirmed snapshots, not projected/raw values.
  // @ts-expect-error raw preference intake is intentionally absent from this facade
  void reader.updatePreferences({});
  return reader;
}
