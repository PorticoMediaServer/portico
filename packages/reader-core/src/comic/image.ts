import {ComicInputError} from './zip.ts';

export type RasterInfo=Readonly<{mime:'image/jpeg'|'image/png'|'image/webp'|'image/gif';width:number;height:number}>;
const invalid=():never=>{throw new ComicInputError('image_invalid');};
export function rasterInfo(bytes:Uint8Array):RasterInfo {
  const view=new DataView(bytes.buffer,bytes.byteOffset,bytes.byteLength);
  let mime:RasterInfo['mime'],width=0,height=0;
  const ascii=(at:number,n:number)=>String.fromCharCode(...bytes.subarray(at,at+n));
  if(bytes.length>=24&&bytes[0]===137&&ascii(1,7)==='PNG\r\n\x1a\n'&&ascii(12,4)==='IHDR'){
    mime='image/png';width=view.getUint32(16);height=view.getUint32(20);
  }else if(bytes.length>=10&&(ascii(0,6)==='GIF87a'||ascii(0,6)==='GIF89a')){
    mime='image/gif';width=view.getUint16(6,true);height=view.getUint16(8,true);
  }else if(bytes.length>=30&&ascii(0,4)==='RIFF'&&ascii(8,4)==='WEBP'){
    mime='image/webp';const kind=ascii(12,4);
    if(kind==='VP8X'){width=1+(bytes[24]|bytes[25]<<8|bytes[26]<<16);height=1+(bytes[27]|bytes[28]<<8|bytes[29]<<16);}
    else if(kind==='VP8 '&&bytes[23]===0x9d&&bytes[24]===0x01&&bytes[25]===0x2a){width=view.getUint16(26,true)&0x3fff;height=view.getUint16(28,true)&0x3fff;}
    else if(kind==='VP8L'&&bytes[20]===0x2f){const bits=view.getUint32(21,true);width=(bits&0x3fff)+1;height=(bits>>>14&0x3fff)+1;}
    else return invalid();
  }else if(bytes.length>=4&&bytes[0]===255&&bytes[1]===216){
    mime='image/jpeg';let at=2;
    while(at+4<=bytes.length){
      if(bytes[at++]!==255)return invalid();while(bytes[at]===255)at++;
      const marker=bytes[at++];if(marker===0xd9||marker===0xda)break;if(marker===1||marker>=0xd0&&marker<=0xd7)continue;
      if(at+2>bytes.length)return invalid();const length=view.getUint16(at);if(length<2||at+length>bytes.length)return invalid();
      if([0xc0,0xc1,0xc2,0xc3,0xc5,0xc6,0xc7,0xc9,0xca,0xcb,0xcd,0xce,0xcf].includes(marker)){if(length<8)return invalid();height=view.getUint16(at+3);width=view.getUint16(at+5);break;}at+=length;
    }
  }else return invalid();
  if(!width||!height)return invalid();
  if(width>32768||height>32768||width*height>40_000_000)throw new ComicInputError('image_limit');
  return Object.freeze({mime,width,height});
}

/** Reads only inert metadata; never parses a DTD, external entity, or markup into DOM. */
export function comicInfo(bytes:Uint8Array):Readonly<{title?:string;author?:string;direction?:'ltr'|'rtl'}> {
  if(bytes.length>128*1024)throw new ComicInputError('archive_limit');
  let xml:string;try{xml=new TextDecoder('utf-8',{fatal:true}).decode(bytes);}catch{throw new ComicInputError('archive_invalid');}
  if(/<!DOCTYPE|<!ENTITY/i.test(xml)||!/<ComicInfo(?:\s|>)/.test(xml))throw new ComicInputError('archive_invalid');
  const named:Record<string,string>={'&amp;':'&','&lt;':'<','&gt;':'>','&quot;':'"','&apos;':"'"};
  // XML 1.0 §4.1 character references; only §2.2 Char values decode.
  const legalChar=(code:number)=>code===0x9||code===0xa||code===0xd||(code>=0x20&&code<=0xd7ff)||(code>=0xe000&&code<=0xfffd)||(code>=0x10000&&code<=0x10ffff);
  const reference=(ref:string)=>{
    if(named[ref])return named[ref]!;
    const hex=ref[2]==='x',code=Number.parseInt(ref.slice(hex?3:2,-1),hex?16:10);
    return legalChar(code)?String.fromCodePoint(code):ref;
  };
  const value=(name:string):string|undefined=>{
    const match=xml.match(new RegExp(`<${name}(?:\\s[^>]*)?>([^<]{0,2048})</${name}>`));
    return match?.[1].replace(/&(?:amp|lt|gt|quot|apos);|&#(?:x[0-9a-fA-F]+|[0-9]+);/g,reference).trim()||undefined;
  };
  const title=value('Title')??value('Series'),author=value('Writer'),manga=value('Manga');
  // Anansi ComicInfo: Manga=Yes alone keeps the default; only YesAndRightToLeft defines RTL.
  return Object.freeze({...title?{title}:{},...author?{author}:{},...manga?{direction:/^YesAndRightToLeft$/i.test(manga)?'rtl' as const:'ltr' as const}:{}});
}
