#!/usr/bin/env python3
"""Exercise actual media bodies and a bounded ten-client API/playback smoke.

Credentials are read only from a private local file. Grant URLs and sessions
remain in memory; reports contain counts, modes and failures, never secrets.
Run after seed_state.py. This is service qualification, not device acceptance.
"""
import argparse, concurrent.futures, json, os, shutil, subprocess, tempfile, time, urllib.request, urllib.error, uuid
from pathlib import Path
from seed_state import (Client, Observer, PLAYBACK_PROTOCOL, private_json, sign_in,
                        select_profile, items, Refusal, stop_session)


from playback_capabilities import capabilities


def request(client, method, path, body=None, headers=None, ok=(200,201,204), timeout=60):
    h={'Accept':'application/json','Content-Type':'application/json',
       'X-Portico-Installation-Id':client.installation,'Authorization':'Bearer '+client.token,
       **PLAYBACK_PROTOCOL}
    h.update(headers or {})
    req=urllib.request.Request(client.base+path,json.dumps(body).encode() if body is not None else None,h,method=method)
    try:r=client.opener.open(req,timeout=timeout)
    except urllib.error.HTTPError as e:r=e
    with r:
        data=r.read(4*1024*1024)
        doc=json.loads(data) if data else None
        if r.status not in ok:
            code=doc.get('error',{}).get('code') if isinstance(doc,dict) else 'http'
            raise Refusal(code or 'http',r.status)
        return doc


def connect(base,cfg,strict=False):
    client=Client(base)
    if strict:
        signed=client.call('POST','/v1/direct/sign-in',{'username':cfg['owner']['username'],'password':cfg['owner']['password']},ok=(200,),timeout=5)
        client.token=signed.get('session',signed)['accessToken']
    else:
        signed,client.token=sign_in(client,cfg['owner']['username'],cfg['owner']['password'])
    client.device=signed.get('session',signed)['deviceId']
    primary=next(p for p in signed['profiles'] if p.get('primary'))
    if strict:
        selected=client.call('POST','/v1/direct/profiles/'+primary['id']+'/select',{'installationId':client.installation,'trust':False},ok=(200,),timeout=5)
        client.token=selected['session']['accessToken']
    else:
        client.token=select_profile(client,primary['id'])
    client.call('PUT','/v1/me/devices/current/capabilities',capabilities(),ok=(204,))
    return client


def start(client,item,converted=False,extra=None):
    body={'itemId':item,'startFrom':'beginning'}
    body.update(extra or {})
    if 'startPositionMs' in body:body.pop('startFrom',None)
    if 'channelId' in body:
        body.pop('itemId',None)
        body['startFrom']='live'
    if converted: body['quality']={'mode':'limit','maxVideoBitrateKbps':180,'maxHeight':360,'maxAudioBitrateKbps':32}
    key={'Idempotency-Key':str(uuid.uuid4())}
    deadline=time.monotonic()+90
    while True:
        session=request(client,'POST','/v1/playback/sessions',body,key,ok=(201,202))
        if (session.get('presentation') or {}).get('url') and not session.get('retryAfterMs'):return session
        if time.monotonic()>=deadline:
            stop(client,session)
            raise RuntimeError('playback_preparation_timeout')
        time.sleep(1)


def stop(client,session,position_ms=None):
    if session:stop_session(client,session['id'],position_ms)


def decode(ffmpeg,url,log,seconds=2,seek_ms=0):
    with log.open('wb') as stream:
        args=[ffmpeg,'-hide_banner','-loglevel','error','-nostdin','-threads','1','-rw_timeout','20000000']
        if seek_ms:args+=['-ss',str(seek_ms/1000)]
        args+=['-i',url,'-t',str(seconds),'-f','null','-']
        r=subprocess.run(args,stdout=subprocess.DEVNULL,stderr=stream,timeout=60)
    return r.returncode==0


def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--base',default='http://localhost:49300')
    p.add_argument('--credentials',type=Path,default=Path('/home/justin/projects/Portico/.private/walks/credentials.json'))
    p.add_argument('--report',type=Path,required=True)
    p.add_argument('--ffmpeg',default=shutil.which('ffmpeg'))
    p.add_argument('--manifest',type=Path,default=Path(__file__).with_name('manifest.json'))
    p.add_argument('--libraries',type=Path,help='Optional known library declarations with id/kind, during a global catalogue fence')
    p.add_argument('--concurrency-seconds',type=int,default=600)
    p.add_argument('--clients',type=int,default=10)
    args=p.parse_args(); cfg=private_json(args.credentials)
    args.report.parent.mkdir(parents=True,exist_ok=True)
    scratch=Path(tempfile.mkdtemp(prefix='playback-smoke-',dir=args.report.parent))
    report={'checks':[],'concurrency':{},'errors':[]};client=None
    try:
        client=connect(args.base,cfg)
        libraries=(json.loads(args.libraries.read_text()) if args.libraries else items(client.read('/v1/libraries')))
        films=[];episodes=[];songs=[];books=[]
        from seed_state import library_items
        for lib in libraries:
            if lib.get('kind') not in ('movie','tv','anime','music','audiobook'):continue
            values=library_items(client,lib,maximum=3000)
            for entry in values:
                kind=entry.get('kind')
                if kind=='movie':films.append(entry)
                elif kind=='episode':episodes.append(entry)
                elif kind=='song':songs.append(entry)
                elif kind in ('audiobook_file','part'):books.append(entry)
        manifest=json.loads(args.manifest.read_text())
        variants={}
        for entry in manifest['entries']:
            if entry['kind']=='movie':variants.setdefault(entry['title'],[]).append(entry)
        light_titles={title for title,versions in variants.items()
                      if all('transcode_source:light' in version.get('features',[]) for version in versions)}
        report['inventory']={'films':len(films),'episodes':len(episodes),'songs':len(songs),'audiobookParts':len(books),
                             'filmTitles': [entry.get('title') for entry in films[:8]]}
        films=[entry for entry in films if entry.get('title') in light_titles]
        if not films:raise RuntimeError('No qualified light film source')
        episode=None;episode_extra={}
        for entry in episodes[:60]:
            opts=client.read('/v1/items/'+entry['id']+'/playback-options')
            version=next((v for v in opts.get('versions',[]) if v.get('subtitles')),None)
            if version:
                sub=next((t for t in version['subtitles'] if t.get('source')!='embedded'),version['subtitles'][0])
                episode=entry;episode_extra={'versionId':version['id'],'subtitles':{'trackId':sub['id']}}
                break
        audiobook=None;book_extra={}
        for entry in books[:46]:
            opts=client.read('/v1/items/'+entry['id']+'/playback-options')
            chapters=opts.get('chapters') or []
            if len(chapters)>=2:
                audiobook=entry;book_extra={'startPositionMs':chapters[1]['startMs']};break
        selected=[('film',films[0],{}),('episode',episode,episode_extra),
                  ('song',songs[0] if songs else None,{}),('audiobook',audiobook,book_extra)]
        guide=client.read('/v1/guide/channels?limit=100').get('directory',{})
        live=next((channel for channel in guide.get('channels',[])
                   if channel.get('name')=='Harbour Workshop'),None)
        selected.append(('live',live,{'channelId':live['id'],'channelGeneration':live['generation']} if live else {}))
        for kind,entry,extra in selected:
            if not entry:
                report['errors'].append({'step':kind,'error':'missing_item'});continue
            for converted in [False,True]:
                s=None
                try:
                    s=start(client,entry['id'],converted,extra)
                    presentation=s['presentation'];mode=presentation['mode']
                    url=client.base+presentation['url']
                    passed=decode(args.ffmpeg,url,scratch/(kind+str(converted)+'.log'),seek_ms=(extra.get('startPositionMs',0) if mode=='direct' else 0))
                    actions={k:v.get('action') for k,v in (presentation.get('decision') or {}).items() if isinstance(v,dict)}
                    rendered=(presentation.get('audioRender') or {}).get('mode')
                    did_convert='transcode' in actions.values() or rendered=='converted'
                    subtitle_bytes=0
                    for track in presentation.get('subtitles') or []:
                        req=urllib.request.Request(client.base+track['url'],headers={'Authorization':'Bearer '+client.token,'X-Portico-Installation-Id':client.installation})
                        with client.opener.open(req,timeout=30) as response:subtitle_bytes+=len(response.read(1024*1024))
                    if kind=='episode' and not presentation.get('subtitle'):raise RuntimeError('Selected subtitles missing')
                    if converted and not did_convert:raise RuntimeError('Conversion not applied')
                    if not converted and kind!='live' and mode!='direct':raise RuntimeError('Original route was not direct')
                    if not converted and kind=='live' and did_convert:raise RuntimeError('Live original unexpectedly converted')
                    report['checks'].append({'kind':kind,'requested':'conversion' if converted else 'original','mode':mode,'actions':actions,'audioRender':rendered,'subtitleBytes':subtitle_bytes,'chapterStartMs':extra.get('startPositionMs'),'decoded':passed})
                    if not passed:report['errors'].append({'step':kind,'error':'decode_failed','converted':converted})
                except Exception as e:
                    report['errors'].append({'step':kind,'converted':converted,'error':str(e) if type(e) is RuntimeError else getattr(e,'code',type(e).__name__),'status':getattr(e,'status',None)})
                finally:stop(client,s)
        if args.concurrency_seconds and films:
            def worker(index):
                c=None;s=None;observer=None;requests=0;failures=[];decoder=None;log=None
                last_position=0;begun=time.monotonic()
                try:
                    c=connect(args.base,cfg,strict=True);s=start(c,films[index%len(films)]['id'])
                    duration_ms=max(1000,int(films[index%len(films)].get('durationMs') or 10000))
                    # One owner per session: this device claims before its first
                    # reading, exactly as the checked-in client does.
                    observer=Observer(c,s['id'],int(s['presentation']['generation']))
                    every=max(.25,min(5,int(observer.claim().get('everyMs') or 1000)/1000));requests+=1
                    log=(scratch/f'client-{index}.log').open('wb')
                    decoder=subprocess.Popen([args.ffmpeg,'-hide_banner','-loglevel','error','-nostdin','-threads','1',
                        '-stream_loop','-1','-re','-i',c.base+s['presentation']['url'],'-t',str(args.concurrency_seconds),'-f','null','-'],
                        stdout=subprocess.DEVNULL,stderr=log)
                    begun=time.monotonic()
                    while time.monotonic()-begun<args.concurrency_seconds:
                        c.call('GET','/v1/libraries',timeout=15);requests+=1
                        c.call('GET','/v1/home',timeout=15);requests+=1
                        position=int((time.monotonic()-begun)*1000)%duration_ms
                        # The decoder is genuinely consuming the stream; the
                        # loop restart is a real seek, not a forced state.
                        observer.observe('playing',position,rate=1.0,seeked=position<last_position)
                        last_position=position;requests+=1
                        if decoder.poll() is not None:
                            failures.append('decoder_ended_early');break
                        time.sleep(min(every,max(.01,args.concurrency_seconds-(time.monotonic()-begun))))
                except Exception as e:failures.append(getattr(e,'code',type(e).__name__))
                finally:
                    if decoder and decoder.poll() is None:
                        decoder.terminate()
                        try:decoder.wait(timeout=5)
                        except subprocess.TimeoutExpired:decoder.kill();decoder.wait()
                    if log:log.close()
                    if c:
                        try:stop(c,s,position_ms=last_position)
                        finally:c.call('DELETE','/v1/devices/'+c.device,ok=(204,))
                return {'client':index,'requests':requests,'durationSeconds':round(time.monotonic()-begun,2),'errors':failures}
            with concurrent.futures.ThreadPoolExecutor(max_workers=args.clients) as pool:
                results=list(pool.map(worker,range(args.clients)))
            report['concurrency']={'clients':args.clients,'targetSeconds':args.concurrency_seconds,'results':results,
                'passed':all(not r['errors'] and r['durationSeconds']>=args.concurrency_seconds for r in results)}
    except Exception as e:
        report['errors'].append({'step':'setup','error':str(e) if type(e) is RuntimeError else getattr(e,'code',type(e).__name__),'status':getattr(e,'status',None)})
    finally:
        if client:
            try:client.call('DELETE','/v1/devices/'+client.device,ok=(204,))
            except Exception:report['errors'].append({'step':'cleanup','error':'device_retirement_failed'})
        if report['errors'] or report['concurrency'].get('passed') is False:
            report['diagnosticDirectory']=str(scratch)
        else:
            shutil.rmtree(scratch)
        args.report.write_text(json.dumps(report,indent=2)+'\n')
    print(json.dumps({'checks':len(report['checks']),'errors':len(report['errors']),'concurrency':report['concurrency'].get('passed')}))
    return 1 if report['errors'] or report['concurrency'].get('passed') is False else 0


if __name__=='__main__':raise SystemExit(main())
