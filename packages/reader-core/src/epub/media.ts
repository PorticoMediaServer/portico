import {rasterInfo} from '../comic/image.ts';
import {ReaderError} from '../errors.ts';

/** Reuse the checked MAIN raster parser before a browser can decode book media.
 * Sniff as well as checking declared type/path, so MIME disguises cannot bypass
 * the shared 40MP/32768-dimension budget. No image is decoded here. */
export function validateEpubRaster(bytes:Uint8Array,path:string,mime:string):ReturnType<typeof rasterInfo>|undefined {
  const prefix=String.fromCharCode(...bytes.subarray(0,12));
  const raster=/\.(?:jpe?g|png|gif|webp)$/i.test(path)||/^image\/(?:jpeg|png|gif|webp)$/.test(mime)
    || prefix.startsWith('\x89PNG\r\n\x1a\n') || (bytes[0]===255&&bytes[1]===216)
    || prefix.startsWith('GIF87a') || prefix.startsWith('GIF89a')
    || (prefix.startsWith('RIFF')&&prefix.slice(8,12)==='WEBP');
  if(!raster)return;
  try {return rasterInfo(bytes);}catch(cause) {
    throw new ReaderError(cause&&typeof cause==='object'&&'code' in cause&&cause.code==='image_limit'?'limit_exceeded':'invalid_book',{cause});
  }
}
