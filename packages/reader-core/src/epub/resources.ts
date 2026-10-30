import type {ReaderResource,ReaderSource} from '../ports.ts';
import {ReaderError,throwIfAborted} from '../errors.ts';
import {safeArchivePath,type EpubAssets} from './archive.ts';
import {abortScope} from '../abort.ts';

/** One immutable publication index; no HTTP client, URL or credentials. */
export function resourceAssets(source:ReaderSource):EpubAssets {
  const resources=source.resources;if(!resources)throw new ReaderError('invalid_source');
  const id=source.id,revision=source.revision,size=source.size,life=new AbortController();let total=0,closed=false;
  const current=()=>{if(source.id!==id||source.revision!==revision||source.size!==size||source.resources!==resources)throw new ReaderError('invalid_source');};
  if(resources.entries.length>4096)throw new ReaderError('limit_exceeded');
  const entries=new Map<string,ReaderResource>();
  for(const entry of resources.entries) {
    const path=safeArchivePath(entry.path);
    if(entries.has(path)||!Number.isSafeInteger(entry.size)||entry.size<0||entry.size>16*1024*1024||(total+=entry.size)>256*1024*1024)
      throw new ReaderError('limit_exceeded');
    entries.set(path,Object.freeze({path,mime:entry.mime,size:entry.size}));
  }
  return {entries:Object.freeze([...entries.values()]),
    async read(path,callerSignal) {
      const scope=abortScope([callerSignal,life.signal]),signal=scope.signal;
      try {throwIfAborted(signal);current();
      const entry=entries.get(safeArchivePath(path));if(!entry)return new Uint8Array();
      const bytes=await resources.read(path,signal);throwIfAborted(signal);current();
      if(!(bytes instanceof Uint8Array)||bytes.length!==entry.size)throw new ReaderError('invalid_book');return bytes;
      } finally {scope.dispose();}
    },dispose(){if(closed)return;closed=true;life.abort();entries.clear();}};
}
