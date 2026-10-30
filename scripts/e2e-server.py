#!/usr/bin/env python3
"""Exercise a real Portico process using disposable loopback-only state.

Usage: python3 scripts/e2e-server.py --scratch /absolute/disposable/directory
Requires Go, ffmpeg and ffprobe. Never uses Hosted or real credentials. The
scratch directory contains secrets and must not be published. Each invocation
creates its own run directory and stops its process tree, including on failure.
"""
import argparse
import concurrent.futures
import contextlib
import copy
import hashlib
import hmac
import http.server
import json
import os
from pathlib import Path
import queue
import secrets
import shutil
import signal
import socket
import subprocess
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[1]


def port():
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0))
        return s.getsockname()[1]


def wait(fn, timeout=90):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        value = fn()
        if value:
            return value
        time.sleep(.3)
    raise AssertionError('Timed out waiting for a condition')


def run_command(argv, **kw):
    p = subprocess.run(argv, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=120, **kw)
    if p.returncode:
        raise RuntimeError(f'{argv[0]} failed: {p.stdout.decode(errors="replace")[-3000:]}')
    return p.stdout


def generate(folder):
    folder.mkdir()
    ffmpeg = shutil.which('ffmpeg')
    base = folder / 'base.mp4'
    run_command([ffmpeg, '-hide_banner', '-loglevel', 'error', '-f', 'lavfi', '-i',
                 'testsrc2=size=320x180:rate=24', '-f', 'lavfi', '-i', 'sine=frequency=440:sample_rate=48000',
                 '-t', '12', '-c:v', 'libx264', '-threads', '1', '-preset', 'ultrafast', '-pix_fmt', 'yuv420p',
                 '-g', '48', '-c:a', 'aac', '-movflags', '+faststart', str(base)])
    files = {}
    def video(relative, extra=None):
        target = folder / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        if extra:
            run_command([ffmpeg, '-hide_banner', '-loglevel', 'error', '-i', str(base), *extra, str(target)])
        else:
            shutil.copyfile(base, target)
        files[relative] = str(target)
        return target
    video('Movies/Arrival (2016)/Arrival (2016) - 1080p.mp4')
    video('Movies/Arrival (2016)/Arrival (2016) - 720p.mkv', ['-c', 'copy'])
    video('Movies/Blade.Runner.1982.1080p.BluRay.x264-GROUP.mp4')
    video('Movies/The Matrxi.mp4')
    video('Movies/Moon.mp4')
    video('Movies/Kingdom of Heaven (2005)/Kingdom of Heaven (2005) {edition-Directors Cut}.mp4')
    video('Movies/Multipart (2020)/Multipart (2020) cd1.mp4')
    video('Movies/Multipart (2020)/Multipart (2020) cd2.mp4')
    video('Movies/Arrival (2016)/extras/Behind the Scenes.mp4')
    video('Movies/sample.mp4')
    video('Shows/Example Show/Season 1/Example Show S01E01.mp4')
    video('Shows/Example Show/Season 2/E01.mp4')
    video('Shows/Example Show/Season 1/Example Show S02E02.mp4')
    video('Shows/Loose Show S01E01.mp4')
    video('Anime/Example Anime/[Group] Example Anime - 001 [1080p].mp4')
    srt = '1\n00:00:00,200 --> 00:00:03,000\nHello, café!\n\n2\n00:00:04,000 --> 00:00:06,000\nSecond cue.\n'
    movie = folder / 'Movies/Arrival (2016)/Arrival (2016) - 1080p'
    for suffix, encoding in [('en.srt', 'utf-8'), ('fr.srt', 'utf-16'), ('de.srt', 'cp1252')]:
        Path(str(movie)+'.'+suffix).write_bytes(srt.encode(encoding))
    ass = folder / 'fixture.ass'
    ass.write_text('[Script Info]\nScriptType: v4.00+\nPlayResX: 320\nPlayResY: 180\n[V4+ Styles]\nFormat: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\nStyle: Default,DejaVu Sans,20,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,1,0,2,10,10,10,1\n[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\nDialogue: 0,0:00:00.20,0:00:05.00,Default,,0,0,0,,Styled subtitle\n')
    shutil.copyfile(ass, str(movie)+'.es.ass')
    video('Movies/Embedded (2021).mkv', ['-i', str(Path(str(movie)+'.en.srt')), '-map', '0', '-map', '1', '-c', 'copy', '-c:s', 'srt', '-metadata:s:s:0', 'language=eng'])
    video('Movies/Styled (2022).mkv', ['-i', str(ass), '-map', '0', '-map', '1', '-c', 'copy', '-c:s', 'ass'])
    video('Movies/Ten Bit (2023).mkv', ['-c:v', 'libx265', '-threads', '1', '-x265-params', 'pools=1:frame-threads=1:log-level=error', '-preset', 'ultrafast', '-pix_fmt', 'yuv420p10le', '-c:a', 'aac', '-ac', '6'])
    load=folder/'Movies/Load (2025).mp4'
    run_command([ffmpeg,'-hide_banner','-loglevel','error','-stream_loop','-1','-i',str(base),'-t','180','-vf','scale=1280:720','-c:v','libx264','-threads','1','-preset','ultrafast','-g','48','-c:a','copy',str(load)])
    for disc, track, title in [(1,1,'First'), (2,1,'Second')]:
        target = folder / f'Music/Example Artist/Example Album/Disc {disc}/{track:02d} {title}.flac'
        target.parent.mkdir(parents=True, exist_ok=True)
        run_command([ffmpeg, '-hide_banner', '-loglevel', 'error', '-i', str(base), '-vn', '-c:a', 'flac',
                     '-metadata', 'artist=Example Artist', '-metadata', 'album=Example Album', '-metadata', 'album_artist=Example Artist',
                     '-metadata', f'title={title}', '-metadata', f'track={track}', '-metadata', f'disc={disc}', str(target)])
    target = folder / 'Music/Compilations/Collection/01 Guest.flac'
    target.parent.mkdir(parents=True,exist_ok=True)
    run_command([ffmpeg, '-hide_banner', '-loglevel', 'error', '-i', str(base), '-vn', '-c:a', 'flac', '-metadata', 'artist=Guest Artist', '-metadata', 'album_artist=Various Artists', '-metadata', 'album=Collection', '-metadata', 'title=Guest', '-metadata', 'compilation=1', str(target)])
    chapters = folder / 'chapters.txt'
    chapters.write_text(';FFMETADATA1\ntitle=Example Book\nalbum=Example Book\nartist=Example Author\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=0\nEND=6000\ntitle=Chapter One\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=6000\nEND=12000\ntitle=Chapter Two\n')
    target = folder / 'Books/Example Author/Example Book/Example Book.m4b'
    target.parent.mkdir(parents=True,exist_ok=True)
    run_command([ffmpeg, '-hide_banner', '-loglevel', 'error', '-i', str(base), '-i', str(chapters), '-map_metadata', '1', '-map_chapters', '1', '-vn', '-c:a', 'copy', str(target)])
    (folder / 'Movies/Broken (2024).mkv').write_bytes(b'not a matroska file\x00')
    (folder / 'Movies/Hostile (2024).strm').write_text('http://169.254.169.254/latest/meta-data/\n')
    # Local NFO facts make restriction and grouping assertions independent of providers.
    Path(str(movie)+'.nfo').write_text('<movie><title>Arrival</title><year>2016</year><mpaa>PG-13</mpaa><genre>Science Fiction</genre><actor><name>Example Actor</name></actor></movie>')
    (folder / 'Movies/Moon.nfo').write_text('<movie><title>Moon</title><year>2009</year><mpaa>R</mpaa><genre>Science Fiction</genre></movie>')
    return files


class API:
    def __init__(self, base, token='', installation=None):
        self.base, self.token = base, token
        self.installation = installation or str(uuid.uuid4())
        self.timings = []
    def call(self, method, path, body=None, expected=(200,), headers=None, raw=False):
        hs = {'X-Portico-Installation-Id': self.installation}
        if self.token:
            hs['Authorization'] = 'Bearer '+self.token
        if body is not None:
            hs['Content-Type'] = 'application/merge-patch+json' if method == 'PATCH' else 'application/json'
        hs.update(headers or {})
        data = json.dumps(body).encode() if body is not None else None
        url=urllib.parse.urljoin(self.base,path)
        assert urllib.parse.urlsplit(url).netloc==urllib.parse.urlsplit(self.base).netloc, 'Refusing non-local request'
        req = urllib.request.Request(url, data, hs, method=method)
        start = time.monotonic()
        try:
            r = urllib.request.build_opener(urllib.request.ProxyHandler({})).open(req, timeout=30)
        except urllib.error.HTTPError as e:
            r = e
        with r:
            value, status, rh = r.read(), r.code, dict(r.headers)
        self.timings.append({'method':method, 'path':path.split('?')[0], 'ms':round((time.monotonic()-start)*1000, 2), 'status':status})
        result = value if raw else (json.loads(value) if value else None)
        if status not in expected:
            # Do not print successful auth envelopes or media URLs (bearer grants).
            raise AssertionError(f'{method} {path.split("?")[0]} returned {status}: {str(result)[:1500]}')
        return result, rh
    def get(self, path):
        return self.call('GET', path)[0]
    def post(self, path, body=None, expected=(200,), headers=None):
        return self.call('POST', path, body, expected, headers)[0]


def capabilities():
    return {'version':2, 'client':{'family':'web','form':'browser'}, 'display':{'dynamicRanges':['sdr']},
            'audioOutput':{'maxChannels':6}, 'engines':{'native':{'name':'hls.js','evidence':'declared',
            'video':[{'codec':'h264','bitDepths':[8],'maxWidth':3840,'maxHeight':2160,'maxFrameRate':60,'dynamicRanges':['sdr']}],
            'audio':[{'codec':'aac','maxChannels':6},{'codec':'mp3','maxChannels':2}],
            'transports':[{'transport':'direct','containers':['mp4','webm'],'video':['h264'],'audio':['aac','mp3']},
                          {'transport':'hls_ts','containers':['mpegts'],'video':['h264'],'audio':['aac','mp3']},
                          {'transport':'hls_fmp4','containers':['fmp4'],'video':['h264'],'audio':['aac','mp3']}],
            'subtitleRendering':['text'],'embeddedAudioSwitching':True}}}


class Exercise:
    def __init__(self, directory, binary):
        self.directory, self.binary = directory, binary
        self.results = []
        self.limitations=['PGS generation: installed FFmpeg has a decoder but no PGS encoder; no source subtitle sample was supplied.', 'Canonical people grouping: local NFO supplies name-only credits; those do not establish a provider-backed person identity.']
        self.api = None
    def check(self, name, fn):
        start = time.monotonic()
        result = fn()
        self.results.append({'name':name,'seconds':round(time.monotonic()-start,3),'result':result})
        print(f'PASS {name} ({self.results[-1]["seconds"]:.3f}s)', flush=True)
        self.save()
        return result
    def save(self):
        (self.directory/'results.json').write_text(json.dumps({'checks':self.results,'limitations':self.limitations,'requests':self.api.timings if self.api else []}, indent=2))
    def start(self):
        env = {k:v for k,v in os.environ.items() if not k.startswith('PORTICO_')}
        self.base = f'http://127.0.0.1:{port()}'
        env.update(PORTICO_DATA_DIR=str(self.directory/'state'), PORTICO_BIND=self.base.split('//')[1],
                   PORTICO_DISCOVERY='0', PORTICO_FFMPEG=shutil.which('ffmpeg'), PORTICO_FFPROBE=shutil.which('ffprobe'))
        self.log = open(self.directory/'server.log','wb')
        self.process = subprocess.Popen([str(self.binary)], cwd=ROOT/'server', env=env, stdout=self.log, stderr=subprocess.STDOUT, start_new_session=True)
        self.api = API(self.base)
        def ready():
            if self.process.poll() is not None:
                raise RuntimeError('Server exited; inspect scratch server.log')
            try:
                return self.api.get('/v1/system')
            except (OSError, AssertionError):
                return False
        wait(ready, 120)
    def stop(self):
        if hasattr(self,'process'):
            with contextlib.suppress(ProcessLookupError):
                os.killpg(self.process.pid,signal.SIGTERM)
            try:
                self.process.wait(timeout=45)
            except subprocess.TimeoutExpired:
                os.killpg(self.process.pid,signal.SIGKILL)
                self.process.wait()
            self.log.close()
    def setup(self):
        a=self.api
        bootstrap=a.post('/v1/setup/browser',{},headers={'Origin':self.base})
        self.password=secrets.token_urlsafe(24)
        (self.directory/'credentials.json').write_text(json.dumps({'owner':self.password}))
        out=a.post('/v1/setup',{'setupToken':bootstrap['setupToken'],'username':'owner','password':self.password,'name':'Local test','authMode':'local','interactive':True},(201,))
        a.token=out['accessToken']
        state=a.get('/v1/setup/state')
        a.post('/v1/setup/finish',{'revision':state['revision']})
        signed=a.post('/v1/direct/sign-in',{'username':'owner','password':self.password})
        a.token=signed['session']['accessToken']
        self.owner=signed
        a.call('PUT','/v1/me/devices/current/capabilities',capabilities(),(204,))
        # Disable unrelated background network activity immediately after setup.
        cfg=a.get('/v1/admin/console/settings')
        (self.directory/'settings.json').write_text(json.dumps(cfg,indent=2))
        values=cfg['data']['requested']
        values.update({'updates.feedUrl':'','lanDiscoveryEnabled':False,'library.missingTitles':'never'})
        a.call('PATCH','/v1/admin/console/settings',{'expectedRevision':cfg['data']['revision'],'idempotencyKey':str(uuid.uuid4()),'values':values})
        assert a.get('/v1/setup/state')['ready']
        return 'Direct Sign-In; no Hosted claim'
    def scan(self):
        a=self.api
        self.libs={}
        self.scan_timings={}
        scan_stop=threading.Event()
        scan_errors=queue.Queue()
        scan_reader=API(self.base,a.token,a.installation)
        def read_during_scan():
            try:
                while not scan_stop.wait(.1):
                    scan_reader.get('/v1/search?q=Arrival')
                    library=self.libs.get('Movies')
                    if library:
                        scan_reader.post('/v1/libraries/'+library+'/browse',{'pivot':'movies','limit':10})
            except Exception as error:
                scan_errors.put(error)
        scan_thread=threading.Thread(target=read_during_scan,daemon=True)
        scan_thread.start()
        for name,kind in [('Movies','movie'),('Shows','tv'),('Anime','anime'),('Music','music'),('Books','audiobook')]:
            started=time.monotonic()
            l=a.post('/v1/libraries',{'name':name,'kind':kind,'path':str(self.directory/'media'/name),'metadataAgent':'local'},(201,))
            self.libs[name]=l['id']
            j=a.post('/v1/libraries/'+l['id']+'/scans',expected=(202,))
            def done():
                current=a.get('/v1/admin/ingestion/jobs/'+j['id'])
                if current['status'] in ['failed','cancelled']:
                    raise AssertionError(f'Scan {name}: {current}')
                return current if current['status'] in ['complete','complete_with_warnings'] else False
            result=wait(done,120)
            self.scan_timings[name]=round(time.monotonic()-started,3)
            issues=a.get('/v1/admin/libraries/'+l['id']+'/scans/'+j['id']+'/issues')
            (self.directory/(name+'-issues.json')).write_text(json.dumps(issues,indent=2))
            (self.directory/(name+'-scan.json')).write_text(json.dumps(result,indent=2))
        scan_stop.set()
        scan_thread.join(timeout=30)
        if not scan_errors.empty():
            raise scan_errors.get()
        a.timings.extend(scan_reader.timings)
        assert scan_reader.timings, 'No browse/search reads overlapped scanning'
        pages={}
        for name,l in self.libs.items():
            def published():
                page=a.post('/v1/libraries/'+l+'/browse',{'pivot':{'Movies':'movies','Shows':'shows','Anime':'shows','Music':'albums','Books':'books'}[name],'limit':100})
                return page if len(page['entries'])=={'Movies':12,'Shows':2,'Anime':1,'Music':2,'Books':1}[name] else False
            pages[name]=wait(published)
        (self.directory/'browse.json').write_text(json.dumps(pages,indent=2))
        return {'titles':{n:len(p.get('entries',[])) for n,p in pages.items()},'secondsByLibrary':self.scan_timings}
    def recognition(self):
        a=self.api
        pages=json.loads((self.directory/'browse.json').read_text())
        issues=json.loads((self.directory/'Movies-issues.json').read_text())
        assert any(i['path']=='sample.mp4' and i['reason']=='ignored_name' for i in issues['items'])
        assert any(i['path']=='Broken (2024).mkv' for i in issues['items'])
        self.movies={e['title']:e['id'] for e in pages['Movies']['entries']}
        assert set(self.movies)=={'Arrival','Blade Runner','Broken','Embedded','Hostile','Kingdom of Heaven','The Matrxi','Moon','Multipart','Styled','Ten Bit','Load'}, self.movies
        details={title:a.get('/v1/items/'+id+'/detail') for title,id in self.movies.items()}
        (self.directory/'details.json').write_text(json.dumps(details,indent=2))
        assert len(details['Arrival']['files'])==2
        assert len(details['Arrival']['extras'])==1
        assert details['Kingdom of Heaven']['files'][0]['edition']=='Directors Cut'
        assert len(details['Multipart']['files'])==2
        shows={e['title']:e['id'] for e in pages['Shows']['entries']}
        assert set(shows)=={'Example Show','Loose Show'}
        seasons=a.get('/v1/shows/'+shows['Example Show']+'/seasons')
        episodes={season['number']:a.get('/v1/seasons/'+season['id']+'/episodes') for season in seasons['seasons']}
        assert sorted(seasons['seasons'],key=lambda x:x['number'])[0]['number']==1
        assert {n:len(p['episodes']) for n,p in episodes.items()}=={1:1,2:2}
        assert any(e['title'] for e in episodes[2]['episodes'])
        (self.directory/'shows.json').write_text(json.dumps({'seasons':seasons,'episodes':episodes},indent=2))
        music={e['title']:a.get('/v1/albums/'+e['id']+'/songs') for e in pages['Music']['entries']}
        books={e['title']:{'files':a.get('/v1/books/'+e['id']+'/files'),'chapters':a.get('/v1/books/'+e['id']+'/chapters')} for e in pages['Books']['entries']}
        attention=a.get('/v1/admin/metadata/attention?limit=50')
        (self.directory/'recognition.json').write_text(json.dumps({'music':music,'books':books,'attention':attention},indent=2))
        assert len(pages['Anime']['entries'])==1
        anime=a.get('/v1/shows/'+pages['Anime']['entries'][0]['id']+'/episodes')
        assert len(anime['episodes'])==1
        assert len(music['Example Album']['songs'])==2
        assert [e['song']['discNumber'] for e in music['Example Album']['songs']]==[1,2]
        assert music['Collection']['songs'][0]['song']['albumArtist']=='Various Artists'
        assert len(books['Example Book']['chapters']['chapters'])==2
        assert details['Ten Bit']['files'][0]['video']['bitDepth']==10
        assert details['Ten Bit']['files'][0]['audio'][0]['channels']==6
        assert {t['language'] for t in details['Arrival']['files'][0]['subtitles']}=={'en','fr','de','es'}
        return {'films':len(details),'shows':len(shows),'albums':len(music),'books':len(books)}

    def browse_search(self):
        a=self.api
        self.listings=[]
        output={}
        for name,l in self.libs.items():
            pivot={'Movies':'movies','Shows':'shows','Anime':'shows','Music':'albums','Books':'books'}[name]
            caps=a.get('/v1/libraries/'+l+'/browse-capabilities?pivot='+pivot)
            output[name+'-capabilities']=caps
            for field in ['title','year','added']:
                for direction in ['asc','desc']:
                    body={'pivot':pivot,'sort':[{'field':field,'direction':direction}],'limit':100}
                    result=a.post('/v1/libraries/'+l+'/browse',body)
                    self.listings.append(('POST','/v1/libraries/'+l+'/browse',body))
                    output[name+'-'+field+'-'+direction]=result
        for filter in [{'field':'genre','operator':'contains','value':'Science Fiction'}, {'field':'year','operator':'at-least','value':2000}]:
            body={'pivot':'movies','query':filter,'sort':[{'field':'year','direction':'desc'}],'limit':100}
            result=a.post('/v1/libraries/'+self.libs['Movies']+'/browse',body)
            self.listings.append(('POST','/v1/libraries/'+self.libs['Movies']+'/browse',body))
            output[str(filter)]=result
        pages=json.loads((self.directory/'browse.json').read_text())
        collection=a.post('/v1/libraries/'+self.libs['Movies']+'/collections',{'name':'Local selection'},(201,))
        a.post('/v1/collections/'+collection['id']+'/items:batch',{'addItemIds':[self.movies['Arrival'],self.movies['Moon']],'removeItemIds':[]})
        self.collection=collection['id']
        people=a.get('/v1/people?q=Example&limit=10')
        (self.directory/'people.json').write_text(json.dumps(people,indent=2))
        grouped=[('Movies','movies','genre','Science Fiction'),('Shows','episodes','memberOf',pages['Shows']['entries'][0]['id']),('Music','songs','memberOf',next(e['id'] for e in pages['Music']['entries'] if e['title']=='Example Album'))]
        grouped.append(('Movies','movies','collection',collection['id']))
        if people['people']:
            grouped.append(('Movies','movies','person',people['people'][0]['id']))
        for name,pivot,field,value in grouped:
            for sort in ['title','year','duration','fileSize']:
                body={'pivot':pivot,'query':{'field':field,'operator':'contains' if field=='genre' else 'equals','value':value},'sort':[{'field':sort,'direction':'desc'}],'limit':100}
                path='/v1/libraries/'+self.libs[name]+'/browse'
                output[str(body)]=a.post(path,body)
                self.listings.append(('POST',path,body))
        for query in ['Arrival','Example','Moon','Ten']:
            for sort in ['title','releaseYear','dateAdded']:
                path='/v1/search?'+urllib.parse.urlencode({'q':query,'sort':sort,'direction':'desc'})
                output[path]=a.get(path)
                self.listings.append(('GET',path,None))
        for name,l in self.libs.items():
            for suffix in (['/content?view=browse&limit=30','/categories','/collections'] if name=='Movies' else ['/content?view=browse&limit=30']):
                path='/v1/libraries/'+l+suffix
                output[path]=a.get(path)
                self.listings.append(('GET',path,None))
        (self.directory/'queries.json').write_text(json.dumps(output,indent=2))
        return {'queries':len(self.listings)}

    def fetch_presentation(self, session, api=None):
        a=api or self.api
        media=API(self.base)
        url=urllib.parse.urljoin(self.base,session['presentation']['url'])
        def fetch():
            value,hs=media.call('GET',url,expected=(200,206,503),headers={'Range':'bytes=0-2097151'},raw=True)
            return (value,hs) if value and 'Retry-After' not in hs else False
        data,hs=wait(fetch,60)
        if data.startswith(b'#EXTM3U'):
            # Resolve every level against its parent, retaining the signed query
            # embedded in the published child references.
            for depth in range(3):
                lines=data.decode().splitlines()
                for line in lines:
                    if line.startswith('#EXT-X-MAP:'):
                        init=line.split('URI="',1)[1].split('"',1)[0]
                        value,_=media.call('GET',urllib.parse.urljoin(url,init),raw=True)
                        assert value
                paths=[l for l in lines if l and not l.startswith('#')]
                assert paths, 'Playlist has no media'
                url=urllib.parse.urljoin(url,paths[0])
                data,hs=media.call('GET',url,expected=(200,206),raw=True)
                if not data.startswith(b'#EXTM3U'):
                    break
            assert len(data)>100, 'Empty segment'
        else:
            assert len(data)>100, 'Empty direct media'
        a.timings.extend(media.timings)
        return len(data)

    def start_session(self, item, extra=None, api=None):
        a=api or self.api
        body={'itemId':item,'startFrom':'beginning','quality':{'mode':'original'}}
        body.update(extra or {})
        v=a.post('/v1/playback/sessions',body,(201,202),{'Idempotency-Key':str(uuid.uuid4())})
        if v.get('presentation',{}).get('url'):
            return v
        return wait(lambda:a.get('/v1/playback/sessions/'+v['id']) if v.get('id') else False)

    def playback(self):
        a=self.api
        options={title:a.get('/v1/items/'+self.movies[title]+'/playback-options') for title in ['Arrival','Embedded','Styled','Ten Bit']}
        (self.directory/'options.json').write_text(json.dumps(options,indent=2))
        results=[]
        arrival=options['Arrival']['versions']
        direct=next(v for v in arrival if v['container']=='mp4')
        remux=next(v for v in arrival if v['container']=='mkv')
        scenarios=[('direct',self.movies['Arrival'],{'versionId':direct['id']}),
                   ('remux',self.movies['Arrival'],{'versionId':remux['id']}),
                   ('conversion',self.movies['Ten Bit'],{})]
        styled=options['Styled']['versions'][0]
        assert styled['subtitles'], 'Embedded ASS not discovered'
        scenarios.append(('burned subtitles',self.movies['Styled'],{'subtitles':{'trackId':styled['subtitles'][0]['id'],'delivery':'burn'}}))
        sidecar=next(t for t in direct['subtitles'] if t['language']=='en')
        scenarios.append(('original styled subtitles',self.movies['Styled'],{'subtitles':{'trackId':styled['subtitles'][0]['id'],'delivery':'embeddedClient'}}))
        scenarios.append(('delivered subtitles',self.movies['Arrival'],{'versionId':direct['id'],'subtitles':{'trackId':sidecar['id'],'delivery':'sidecar'}}))
        for language,encoding in [('fr','UTF-16'),('de','Windows-1252')]:
            track=next(t for t in direct['subtitles'] if t['language']==language)
            scenarios.append(('delivered subtitles '+encoding,self.movies['Arrival'],{'versionId':direct['id'],'subtitles':{'trackId':track['id'],'delivery':'sidecar'}}))
        for name,item,extra in scenarios:
            session=self.start_session(item,extra)
            try:
                size=self.fetch_presentation(session)
                for sub in session['presentation'].get('subtitles',[]):
                    data,_=a.call('GET',sub['url'],raw=True)
                    assert len(data)>20
                    if name.startswith('delivered subtitles'):
                        assert 'café' in data.decode('utf-8'), 'Subtitle encoding changed the text'
                    if name=='original styled subtitles':
                        assert b'[V4+ Styles]' in data and b'Styled subtitle' in data
                results.append({'scenario':name,'mode':session['presentation']['mode'],'bytes':size,'video':session['presentation']['decision'].get('video')})
                if name=='direct':
                    path='/v1/playback/sessions/'+session['id']
                    changed,_=a.call('PATCH',path,{'quality':{'mode':'limit','maxHeight':120,'maxVideoBitrateKbps':200}},headers={'If-Match':session['revision']})
                    assert changed['presentation']['generation']>session['presentation']['generation']
                    self.fetch_presentation(changed)
                    a.post(path+'/timeline',{'seq':1,'generation':changed['presentation']['generation'],'state':'playing','positionMs':3000,'rate':1},(204,))
                    session=changed
            finally:
                a.call('DELETE','/v1/playback/sessions/'+session['id'],{'positionMs':3000},(204,))
        (self.directory/'playback.json').write_text(json.dumps(results,indent=2))
        assert results[0]['mode']=='direct'
        assert results[1]['video']['action'] in ['copy','remux','direct']
        assert results[2]['video']['action']=='transcode'
        assert results[3]['video']['action']=='burn'
        return results

    def capability_profiles(self):
        a=self.api
        caps=capabilities()
        caps['client']={'family':'native','form':'tv','platform':'android'}
        mpv=copy.deepcopy(caps['engines']['native'])
        mpv['name']='mpv'
        mpv['video'].append({'codec':'hevc','bitDepths':[8,10],'maxWidth':3840,'maxHeight':2160,'maxFrameRate':60,'dynamicRanges':['sdr']})
        mpv['transports']=[{'transport':'direct','containers':['mp4','mkv'],'video':['h264','hevc'],'audio':['aac','mp3']}]
        mpv['subtitleRendering']=['text','ass','pgs']
        caps['engines']['mpv']=mpv
        caps['engineSetting']='automatic'
        result=[]
        try:
            a.call('PUT','/v1/me/devices/current/capabilities',caps,(204,))
            session=self.start_session(self.movies['Ten Bit'])
            try:
                assert session['presentation']['engine']=='mpv' and session['presentation']['mode']=='direct'
                self.fetch_presentation(session)
                result.append('Automatic uses mpv for direct HEVC 10-bit with six-channel audio')
            finally:
                a.call('DELETE','/v1/playback/sessions/'+session['id'],expected=(204,))
            # A native player declaring an MKV/HEVC decoder avoids conversion too.
            caps['engines']['native']=mpv
            caps['engines']['native']['name']='native decoder'
            caps['engineSetting']='native'
            a.call('PUT','/v1/me/devices/current/capabilities',caps,(204,))
            session=self.start_session(self.movies['Ten Bit'])
            try:
                assert session['presentation']['engine']=='native' and session['presentation']['mode']=='direct'
                self.fetch_presentation(session)
                result.append('Native setting honors declared MKV/HEVC support')
            finally:
                a.call('DELETE','/v1/playback/sessions/'+session['id'],expected=(204,))
            caps=capabilities()
            caps['audioDecode']=[{'codec':'aac','containers':['mp4','m4b','adts'],'maxSampleRate':96000,'maxChannels':8},{'codec':'flac','containers':['flac'],'maxSampleRate':96000,'maxChannels':8,'maxBitDepth':24}]
            a.call('PUT','/v1/me/devices/current/capabilities',caps,(204,))
            recognized=json.loads((self.directory/'recognition.json').read_text())
            for item in [recognized['music']['Example Album']['songs'][0]['id'],recognized['books']['Example Book']['files']['files'][0]['id']]:
                def measured():
                    v=self.start_session(item)
                    if v['presentation']['audioRender']['mode']=='direct':
                        return v
                    a.call('DELETE','/v1/playback/sessions/'+v['id'],expected=(204,))
                    return False
                session=wait(measured,30)
                try:
                    assert session['kind']=='audio' and session['presentation']['audioRender']['mode']=='direct'
                    audio=session['presentation']['audioRender']
                    grant=copy.deepcopy(session)
                    grant['presentation']['url']=audio['url']
                    self.fetch_presentation(grant)
                    assert audio['durationFrames']>0 and audio['sampleRate']>0
                finally:
                    a.call('DELETE','/v1/playback/sessions/'+session['id'],expected=(204,))
            result.append('Tagged FLAC and chaptered M4B supply measurable direct audio bytes')
        finally:
            a.call('PUT','/v1/me/devices/current/capabilities',capabilities(),(204,))
        return result

    def personal(self):
        a=self.api
        item=self.movies['Arrival']
        state=a.get('/v1/items/'+item+'/detail')['personal']
        marked,_=a.call('PUT','/v1/items/'+item+'/personal-state',{'operationId':str(uuid.uuid4()),'expectedRevision':state['revision'],'watched':True})
        assert marked['personal']['watched']
        undone,_=a.call('PUT','/v1/items/'+item+'/personal-state',{'operationId':str(uuid.uuid4()),'expectedRevision':marked['personal']['revision'],'watched':False})
        assert not undone['personal']['watched']
        pages=json.loads((self.directory/'browse.json').read_text())
        show=next(e['id'] for e in pages['Shows']['entries'] if e['title']=='Example Show')
        path='/v1/containers/show/'+show+'/personal-state'
        before=a.get(path)
        marked,_=a.call('PUT',path,{'expectedRevision':before['revision'],'watched':True})
        assert marked['watched'] and marked['undoToken']
        undone=a.post(path+'/undo',{'expectedRevision':marked['revision'],'undoToken':marked['undoToken']})
        assert undone['watched']==before['watched']
        return 'Title watched/unwatched and show mark/undo read back'

    def restrictions(self):
        a=self.api
        password=secrets.token_urlsafe(24)
        member=a.post('/v1/direct/members',{'username':'member','password':password,'name':'Test Member','allowedLibraries':list(self.libs.values()),'disabled':False,'expectedRevision':0},(201,))
        credentials=json.loads((self.directory/'credentials.json').read_text())
        credentials['member']=password
        (self.directory/'credentials.json').write_text(json.dumps(credentials))
        self.member=API(self.base)
        signed=self.member.post('/v1/direct/sign-in',{'username':'member','password':password})
        self.member.token=signed['session']['accessToken']
        profile=self.member.post('/v1/direct/profiles',{'name':'Restricted','art':''},(201,))
        (self.directory/'profile.json').write_text(json.dumps(profile,indent=2))
        profile_id=next(p['id'] for p in profile['profiles'] if p['name']=='Restricted')
        restrictions=self.member.get('/v1/direct/profiles/'+profile_id+'/restrictions')
        body={'expectedRevision':restrictions['revision'],'ratingSystem':'mpaa','maximumAgeRating':'PG-13','allowUnrated':True,'blockedLabels':[],'allowDownloads':False,'allowLiveTv':False,'allowDvr':False,'allowWatchTogether':False}
        self.member.call('PUT','/v1/direct/profiles/'+profile_id+'/restrictions',body)
        selected=self.member.post('/v1/direct/profiles/'+profile_id+'/select',{'pin':'','installationId':self.member.installation,'trustToken':'','trust':False})
        self.restricted=API(self.base,selected['session']['accessToken'],self.member.installation)
        hidden=self.movies['Moon']
        for method,path,body in self.listings:
            data,_=self.restricted.call(method,path,body)
            assert hidden not in json.dumps(data), 'Restricted title leaked from '+path
        for path in ['/v1/items/'+hidden,'/v1/items/'+hidden+'/detail','/v1/items/'+hidden+'/playback-options']:
            self.restricted.call('GET',path,expected=(404,))
        self.restricted.call('GET','/v1/items/'+self.movies['Arrival']+'/detail')
        self.restricted.call('GET','/v1/admin/console/settings',expected=(403,))
        self.restricted.call('PUT','/v1/me/devices/current/capabilities',capabilities(),(204,))
        self.restricted.post('/v1/playback/sessions',{'itemId':hidden},(404,),{'Idempotency-Key':str(uuid.uuid4())})
        a.timings.extend(self.member.timings+self.restricted.timings)
        return {'restrictedListings':len(self.listings),'hiddenTitle':'Moon','allowedTitle':'Arrival'}

    def credentials_mcp(self):
        a=self.api
        self.keys={}
        paths={'read':('GET','/v1/libraries',None,(200,)),
               'playback':('PUT','/v1/me/devices/current/capabilities',capabilities(),(204,)),
               'library':('POST','/v1/libraries/paths-changed',{'paths':[{'path':str(self.directory/'media/Movies/Moon.mp4'),'change':'changed'}]},(202,)),
               'admin':('GET','/v1/admin/console/settings',None,(200,))}
        for scope in paths:
            key=a.post('/v1/admin/api-keys',{'operationId':str(uuid.uuid4()),'name':scope+' test','scopes':[scope]},(201,))
            self.keys[scope]=key['secret']
            api=API(self.base,key['secret'])
            for required,(method,path,body,status) in paths.items():
                api.call(method,path,body,expected=status if required==scope or scope=='admin' else (403,))
            api.call('GET','/v1/admin/api-keys',expected=(403,))
            a.timings.extend(api.timings)
        (self.directory/'keys.json').write_text(json.dumps(self.keys))
        api=API(self.base,self.keys['read'])
        def rpc(method,params,id=1):
            out=api.post('/v1/mcp',{'jsonrpc':'2.0','id':id,'method':method,'params':params})
            assert 'error' not in out, out
            return out['result']
        init=rpc('initialize',{'protocolVersion':'2025-06-18','capabilities':{},'clientInfo':{'name':'local-test','version':'1'}})
        assert init['protocolVersion']=='2025-06-18'
        tools=rpc('tools/list',{})['tools']
        names={t['name'] for t in tools}
        assert 'list_libraries' in names and 'change_settings' not in names
        result=rpc('tools/call',{'name':'list_libraries','arguments':{}})
        assert not result.get('isError') and self.libs['Movies'] in json.dumps(result)
        a.timings.extend(api.timings)
        return {'scopes':list(self.keys),'mcpTools':len(tools)}

    def webhook(self):
        messages=queue.Queue()
        class Listener(http.server.BaseHTTPRequestHandler):
            def do_POST(handler):
                body=handler.rfile.read(int(handler.headers['Content-Length']))
                messages.put((dict(handler.headers),body))
                handler.send_response(204)
                handler.end_headers()
            def log_message(*args):
                pass
        listener=http.server.ThreadingHTTPServer(('127.0.0.1',0),Listener)
        thread=threading.Thread(target=listener.serve_forever,daemon=True)
        thread.start()
        a=self.api
        secret=secrets.token_urlsafe(32)
        try:
            hook=a.post('/v1/admin/webhooks',{'name':'Local listener','url':f'http://127.0.0.1:{listener.server_port}/event','events':['scan.finished'],'enabled':True,'secret':secret},(201,))
            id=hook['webhook']['id']
            a.post('/v1/admin/webhooks/'+id+'/test',expected=(202,))
            headers,body=messages.get(timeout=30)
            parts=dict(v.split('=',1) for v in headers['Portico-Signature'].split(','))
            digest=hmac.new(secret.encode(),parts['t'].encode()+b'.'+body,hashlib.sha256).hexdigest()
            assert hmac.compare_digest(digest,parts['v1'])
            assert abs(time.time()-int(parts['t']))<60
            self.scan_again(self.libs['Shows'])
            real_headers,real_body=messages.get(timeout=30)
            parts=dict(v.split('=',1) for v in real_headers['Portico-Signature'].split(','))
            digest=hmac.new(secret.encode(),parts['t'].encode()+b'.'+real_body,hashlib.sha256).hexdigest()
            assert real_headers['Portico-Event']=='scan.finished' and hmac.compare_digest(digest,parts['v1'])
            def delivered():
                page=a.get('/v1/admin/webhooks/'+id+'/deliveries')
                return page if any(d['state']=='delivered' for d in page['items']) else False
            delivery=wait(delivered,30)
            (self.directory/'webhook.json').write_text(json.dumps({'event':headers['Portico-Event'],'delivery':delivery},indent=2))
            a.call('DELETE','/v1/admin/webhooks/'+id,expected=(204,))
            return 'Local HTTP delivery and HMAC-SHA256 verified'
        finally:
            listener.shutdown()
            listener.server_close()
            thread.join(timeout=3)

    def events(self):
        a=self.api
        subscribed=threading.Event()
        found=queue.Queue()
        item=self.movies['Arrival']
        # Start after existing events, then hold a real streaming response open.
        page=a.get('/v1/events?waitMs=0')
        cursor=page['nextAfter']
        def reader():
            try:
                request=urllib.request.Request(self.base+'/v1/events',headers={'Authorization':'Bearer '+a.token,'Last-Event-ID':str(cursor),'Accept':'text/event-stream'})
                with urllib.request.urlopen(request,timeout=15) as response:
                    subscribed.set()
                    while True:
                        line=response.readline()
                        if not line:
                            break
                        if line.startswith(b'data:'):
                            value=json.loads(line[5:])
                            if 'personal' in json.dumps(value):
                                found.put(value)
                                return
            except Exception as exc:
                found.put(exc)
        thread=threading.Thread(target=reader,daemon=True)
        thread.start()
        assert subscribed.wait(10), 'Event stream did not open'
        state=a.get('/v1/items/'+item+'/detail')['personal']
        a.call('PUT','/v1/items/'+item+'/personal-state',{'operationId':str(uuid.uuid4()),'expectedRevision':state['revision'],'favorite':True})
        value=found.get(timeout=20)
        if isinstance(value,Exception):
            raise value
        thread.join(timeout=3)
        (self.directory/'event.json').write_text(json.dumps(value,indent=2))
        return 'Personal change delivered over SSE'

    def scan_again(self, library, mode='full'):
        a=self.api
        job=a.post('/v1/libraries/'+library+'/scans?mode='+mode,expected=(202,))
        def done():
            current=a.get('/v1/admin/ingestion/jobs/'+job['id'])
            if current['status'] in ['failed','cancelled']:
                raise AssertionError('Scan failed: '+str(current))
            return current if current['status'] in ['complete','complete_with_warnings'] else False
        return wait(done,120)

    def files_and_missing(self):
        a=self.api
        library=self.libs['Movies']
        cfg=a.get('/v1/admin/libraries/'+library+'/settings')
        settings=cfg['settings']
        settings['allowMediaDeletion']=True
        a.call('PUT','/v1/admin/libraries/'+library+'/settings',{'expectedRevision':cfg['revision'],'operationId':str(uuid.uuid4()),'settings':settings})
        item=self.movies['Arrival']
        detail=a.get('/v1/items/'+item+'/detail')
        file=next(f for f in detail['files'] if f['container']=='mkv')
        path='/v1/admin/items/'+item+'/files/'+file['id']
        preview=a.post(path+'/delete/preview',{})
        result,_=a.call('DELETE',path,{'confirmation':preview['confirmation'],'previewKey':preview['previewKey'],'operationId':str(uuid.uuid4())})
        assert result['removed']==1 and not Path(file['path']).exists()
        remaining=a.get('/v1/items/'+item+'/detail')
        assert len(remaining['files'])==1 and remaining['item']['id']==item
        missing=self.movies['Moon']
        prior=a.get('/v1/items/'+missing+'/detail')['personal']
        a.call('PUT','/v1/items/'+missing+'/personal-state',{'operationId':str(uuid.uuid4()),'expectedRevision':prior['revision'],'favorite':True})
        media=self.directory/'media/Movies/Moon.mp4'
        hidden=self.directory/'Moon-away.mp4'
        media.rename(hidden)
        try:
            report=a.post('/v1/libraries/paths-changed',{'paths':[{'path':str(media),'change':'removed'},{'path':str(self.directory/'outside'),'change':'added'}]},(202,))
            assert report['jobs']
            self.scan_again(library)
            def gone():
                d=a.get('/v1/items/'+missing+'/detail')
                return d if d.get('missingSince') and d['item']['missing'] and not d['item']['available'] else False
            gone=wait(gone)
            assert gone['item']['missing'] and not gone['item']['available']
            assert any(i['id']==missing for i in a.get('/v1/admin/missing-titles?libraryId='+library)['items'])
        finally:
            hidden.rename(media)
        a.post('/v1/libraries/paths-changed',{'paths':[{'path':str(media),'change':'added'}]},(202,))
        self.scan_again(library)
        def restored():
            d=a.get('/v1/items/'+missing+'/detail')
            return d if not d.get('missingSince') and d['item']['available'] else False
        restored=wait(restored)
        assert restored['item']['id']==missing and restored['personal']['favorite']
        (self.directory/'file-lifecycle.json').write_text(json.dumps({'delete':result,'pathReport':report,'missingId':missing,'restored':restored['item']['id']},indent=2))
        return 'Version deleted on disk; missing title and state restored with same identity'

    def unreachable_root(self):
        a=self.api
        root=self.directory/'media/Movies'
        away=self.directory/'media/Movies-away'
        root.rename(away)
        try:
            job=a.post('/v1/libraries/'+self.libs['Movies']+'/scans?mode=full',expected=(202,))
            def done():
                current=a.get('/v1/admin/ingestion/jobs/'+job['id'])
                return current if current['status'] in ['failed','complete','complete_with_warnings'] else False
            result=wait(done,30)
            assert result['status']=='failed' or result['warnings']>0, result
            assert not a.get('/v1/admin/missing-titles?libraryId='+self.libs['Movies'])['items']
            for item in self.movies.values():
                assert not a.get('/v1/items/'+item+'/detail').get('missingSince')
            (self.directory/'unreachable-root.json').write_text(json.dumps(result,indent=2))
            return 'Unavailable root reports failure without marking titles missing'
        finally:
            away.rename(root)

    def reserve(self):
        a=self.api
        doc=a.get('/v1/admin/console/settings')['data']
        saved=copy.deepcopy(doc['requested'])
        values=copy.deepcopy(saved)
        values.update({'maxConcurrentSessions':1,'maxSoftwareSessions':1,'maxHardwareSessions':1})
        a.call('PATCH','/v1/admin/console/settings',{'expectedRevision':doc['revision'],'idempotencyKey':str(uuid.uuid4()),'values':values})
        first=None
        try:
            first=self.start_session(self.movies['Load'],{'quality':{'mode':'limit','maxHeight':480,'maxVideoBitrateKbps':1000}})
            self.fetch_presentation(first)
            second=API(self.base)
            auth=second.post('/v1/direct/sign-in',{'username':'owner','password':self.password})
            second.token=auth['session']['accessToken']
            second.call('PUT','/v1/me/devices/current/capabilities',capabilities(),(204,))
            limited=second.post('/v1/playback/sessions',{'itemId':self.movies['Ten Bit'],'quality':{'mode':'original'}},(429,),{'Idempotency-Key':str(uuid.uuid4())})
            assert limited['error']['code']=='conversion_capacity'
            def conversion_running():
                # Read only our supervised process tree, never other lanes.
                pending=[self.process.pid]
                while pending:
                    pid=pending.pop()
                    try:
                        cmd=Path(f'/proc/{pid}/comm').read_text().strip()
                        if cmd=='ffmpeg':
                            return True
                        for children in Path(f'/proc/{pid}/task').glob('*/children'):
                            pending.extend(int(p) for p in children.read_text().split())
                    except (OSError,ValueError):
                        pass
                return False
            assert conversion_running(), 'Reserve probe needs a live FFmpeg conversion'
            def owner_read(_):
                client=API(self.base,a.token,a.installation)
                start=time.monotonic()
                result=client.get('/v1/admin/console/settings')
                return time.monotonic()-start
            with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
                timings=list(pool.map(owner_read,range(16)))
            assert max(timings)<2, timings
            a.timings.extend(second.timings)
            return {'conversionSlots':1,'liveFFmpeg':True,'extraConversion':'refused','ownerMaxMs':round(max(timings)*1000,2)}
        finally:
            if first:
                a.call('DELETE','/v1/playback/sessions/'+first['id'],expected=(204,))
            doc=a.get('/v1/admin/console/settings')['data']
            a.call('PATCH','/v1/admin/console/settings',{'expectedRevision':doc['revision'],'idempotencyKey':str(uuid.uuid4()),'values':saved})

    def backup_restore(self):
        a=self.api
        a.post('/v1/admin/backups',{'operationId':str(uuid.uuid4())},(202,))
        def finished():
            page=a.get('/v1/admin/backups')
            return page if page['backups'] and not page.get('running') else False
        page=wait(finished,60)
        backup=page['backups'][0]
        before=a.get('/v1/admin/console/settings')['data']
        values=copy.deepcopy(before['requested'])
        values['name']='After backup'
        a.call('PATCH','/v1/admin/console/settings',{'expectedRevision':before['revision'],'idempotencyKey':str(uuid.uuid4()),'values':values})
        assert a.get('/v1/system')['name']=='After backup'
        a.post('/v1/admin/backups/restore',{'operationId':str(uuid.uuid4()),'source':{'backupId':backup['id']}},(202,))
        # Restoration retires sessions and the supervisor replaces the worker.
        def restarted():
            try:
                out=a.call('GET','/v1/admin/console/settings',expected=(200,401,503))[0]
                return out if out and out.get('error',{}).get('code')=='unauthorized' else False
            except (OSError,AssertionError):
                return False
        wait(restarted,90)
        def sign_in():
            try:
                auth=a.post('/v1/direct/sign-in',{'username':'owner','password':self.password})
                a.token=auth['session']['accessToken']
                return auth
            except (OSError,AssertionError):
                return False
        wait(sign_in,30)
        page=a.get('/v1/admin/backups')
        assert page['lastRestore']['outcome']=='restored', page
        assert a.get('/v1/system')['name']==before['requested']['name']
        assert self.libs['Movies'] in json.dumps(a.get('/v1/libraries'))
        assert self.movies['Arrival'] in json.dumps(a.get('/v1/libraries/'+self.libs['Movies']+'/content?view=browse'))
        API(self.base,self.keys['read']).get('/v1/libraries')
        (self.directory/'restore.json').write_text(json.dumps(page,indent=2))
        return 'Backup restored, worker restarted, old session retired, settings/catalogue/API key read back'

    def remove_library(self):
        a=self.api
        library=self.libs['Books']
        cfg=a.get('/v1/admin/libraries/'+library+'/settings')
        result,_=a.call('DELETE','/v1/admin/libraries/'+library,{'confirmation':'Books','expectedRevision':cfg['revision']},(202,))
        assert result['removing']
        assert library not in json.dumps(a.get('/v1/libraries'))
        assert (self.directory/'media/Books/Example Author/Example Book/Example Book.m4b').exists()
        return 'Library hidden immediately; media retained'

    def run(self):
        self.check('generate varied media',lambda:len(generate(self.directory/'media')))
        self.check('start production server',self.start)
        self.check('first-run setup and sign-in',self.setup)
        self.check('scan local libraries',self.scan)
        self.check('recognition and details',self.recognition)
        self.check('browse and search',self.browse_search)
        self.check('playback bytes, tracks, quality and progress',self.playback)
        self.check('player engines and listening media',self.capability_profiles)
        self.check('personal watched state and undo',self.personal)
        self.check('member and restricted profile',self.restrictions)
        self.check('API scopes and MCP',self.credentials_mcp)
        self.check('signed webhook delivery',self.webhook)
        self.check('event stream change',self.events)
        self.check('file deletion, missing titles and paths changed',self.files_and_missing)
        self.check('unreachable root retains titles',self.unreachable_root)
        self.check('owner reserve at conversion capacity',self.reserve)
        self.check('backup restore with automatic restart',self.backup_restore)
        self.check('library removal',self.remove_library)
        for name,suffix in [('browse','/browse'),('search','/v1/search')]:
            values=sorted(r['ms'] for r in self.api.timings if r['status']==200 and r['path'].endswith(suffix))
            print(f'TIMING {name}: n={len(values)} median={values[len(values)//2]:.2f}ms max={max(values):.2f}ms',flush=True)
        print(f'PASS all {len(self.results)} scenario groups',flush=True)
        for limitation in self.limitations:
            print('NOT EXERCISED '+limitation,flush=True)


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--scratch', type=Path, required=True)
    parser.add_argument('--binary',type=Path)
    args=parser.parse_args()
    scratch=args.scratch.resolve()
    if scratch == ROOT or ROOT in scratch.parents:
        parser.error('Scratch must be outside the repository (contains secrets and media)')
    scratch.mkdir(parents=True,exist_ok=True,mode=0o700)
    os.umask(0o077)
    directory=scratch/('run-'+time.strftime('%Y%m%d-%H%M%S')+'-'+secrets.token_hex(3))
    directory.mkdir(mode=0o700)
    binary=args.binary.resolve() if args.binary else scratch/'portico-server'
    if not args.binary:
        run_command(['go','build','-o',str(binary),'./cmd/server'],cwd=ROOT/'server',env={**os.environ,'GOFLAGS':'-p=2'})
    exercise=Exercise(directory,binary)
    print(f'Evidence: {directory}',flush=True)
    try:
        exercise.run()
    finally:
        exercise.stop()
        exercise.save()

if __name__=='__main__':
    main()
