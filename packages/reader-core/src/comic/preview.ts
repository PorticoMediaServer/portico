import {throwSignalAborted} from '../abort.ts';
import {ComicInputError} from './zip.ts';

/** Trusted host canvas only; older WebKit has no OffscreenCanvas. Never mount
 * the image/HTML in the book, and retire the bounded scratch canvas on exit. */
export async function comicPreview(bitmap:ImageBitmap,width:number,signal:AbortSignal) {
  throwSignalAborted(signal);
  const scaledWidth=Math.min(width,bitmap.width),scaledHeight=Math.max(1,Math.round(bitmap.height*scaledWidth/bitmap.width));
  if(!Number.isInteger(width)||width<32||width>2048||scaledWidth*scaledHeight>16_000_000)throw new ComicInputError('image_limit');
  const canvas=typeof OffscreenCanvas!=='undefined'?new OffscreenCanvas(scaledWidth,scaledHeight):document.createElement('canvas');
  canvas.width=scaledWidth;canvas.height=scaledHeight;
  try {
    const context=canvas.getContext('2d');if(!context)throw new ComicInputError('image_invalid');
    (context as CanvasRenderingContext2D|OffscreenCanvasRenderingContext2D).drawImage(bitmap,0,0,scaledWidth,scaledHeight);
    const blob=await ('convertToBlob'in canvas?canvas.convertToBlob({type:'image/png'}):new Promise<Blob>((resolve,reject)=>canvas.toBlob(value=>value?resolve(value):reject(new ComicInputError('image_invalid')),'image/png')));
    throwSignalAborted(signal);if(blob.size>16*1024*1024)throw new ComicInputError('image_limit');
    const bytes=new Uint8Array(await blob.arrayBuffer());throwSignalAborted(signal);
    return {mime:'image/png' as const,bytes,width:scaledWidth,height:scaledHeight};
  }finally{canvas.width=0;canvas.height=0;}
}
