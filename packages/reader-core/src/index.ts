export * from './ports.ts';
export {ReaderError, readerErrorCode, throwIfAborted} from './errors.ts';
export {ReaderController, validateLocation} from './controller.ts';
export {createReadingController} from './reading.ts';
export type {ReadingController, ReadingControllerOptions} from './reading.ts';
export {createReadingPublication,readerLocationFromReading,readingLocationFromReader} from './publication.ts';
export type {ReaderSessionInput,ReadingPublication} from './publication.ts';
export {MAX_READER_RANGE_BYTES, readRange, sourceFromBlob, validateSource} from './source.ts';
export {planFixedLayout, spreadFits, compactLandscape, validateViewport, creaseSpread, validReaderCrease} from './layout.ts';
export type {FixedLayoutInput} from './layout.ts';

export {createEpubAdapter} from './epub/index.ts';
export type {EpubAdapterOptions} from './epub/index.ts';
export {loadLiterataFont, literataFontUrl, LITERATA_FONT_SHA256} from './fonts.ts';
// Format implementations are consumed only after their checked MAIN landing.
export {createPdfAdapter} from './pdf/adapter.ts';
export type {PdfOptions as PdfAdapterOptions} from './pdf/engine.ts';
export {createComicAdapter} from './comic/adapter.ts';
export type {ComicAdapterOptions} from './comic/adapter.ts';
export type {ComicPageProvider, ComicPage} from './comic/archive.ts';
// Inspect bounded encoded page bytes before browser decoding. This is a size
// and MIME sniffer; callers retain byte limits and decode-failure handling.
export {rasterInfo} from './comic/image.ts';
export type {RasterInfo} from './comic/image.ts';
