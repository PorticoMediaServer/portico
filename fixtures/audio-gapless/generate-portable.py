#!/usr/bin/env python3
"""Extra synthetic decoder cases. Never rewrites the original conformance corpus.
Run: python3 generate-portable.py --out /tmp/portico-audio-extra
Optional long MP3: add --long-hours 2 (device acceptance, not a unit-test prerequisite).
HE-AAC needs a qualified external encoder; this script does not label AAC-LC as HE-AAC.
"""
import argparse
import json
from pathlib import Path
import subprocess


def run(*args):
    return subprocess.check_output(args)


def generate(out, name, codec, extension, rate=44100, frames=22051, offset=0, extra=()):
    path = out / (name + '.' + extension)
    signal = f'0.5*sin(2*PI*441*(n+{offset})/{rate})'
    run('ffmpeg', '-v', 'error', '-y', '-f', 'lavfi', '-i', f'aevalsrc={signal}|{signal}:s={rate}',
        '-af', f'atrim=end_sample={frames}', '-c:a', codec, *extra, str(path))
    facts = json.loads(run('ffprobe', '-v', 'error', '-flags2', '+skip_manual', '-select_streams', 'a:0',
        '-show_packets', '-show_frames', '-show_streams', '-of', 'json', str(path)))
    rows = facts.get('packets_and_frames', [])
    packets = facts.get('packets', [x for x in rows if x.get('type') == 'packet'])
    decoded = facts.get('frames', [x for x in rows if x.get('type') == 'frame'])
    raw = sum(int(x['nb_samples']) for x in decoded)
    start = sum(int(s.get('skip_samples', 0)) for p in packets[:1] for s in p.get('side_data_list', []))
    end = sum(int(s.get('discard_padding', 0)) for p in packets for s in p.get('side_data_list', []))
    # MP4's edit-list duration includes the final partial packet; raw decode includes its padding.
    if codec == 'aac':
        end = max(end, raw-start-frames)
    data = dict(file=path.name, container={'m4a':'mp4','ogg':'ogg'}.get(extension,extension),
        codec={'libmp3lame':'mp3','libvorbis':'vorbis'}.get(codec,codec), sampleRate=rate,channels=2,
        durationFrames=raw-start-end,rawFrames=raw,trim=dict(startFrames=start,endFrames=end,source='mp4-editlist' if codec=='aac' else 'measured'),
        signal=dict(amplitude=0.5,frequency=441,offset=offset),tolerance=0.05,edgeFrames=64,edgeTolerance=0.1)
    (out/(name+'.json')).write_text(json.dumps(data,indent=1)+'\n')
    return data


def main():
    args=argparse.ArgumentParser()
    args.add_argument('--out',type=Path,required=True)
    args.add_argument('--long-hours',type=float,default=0)
    opts=args.parse_args()
    out=opts.out.resolve()
    if out == Path(__file__).resolve().parent:
        args.error('Use a separate output directory to preserve the original corpus.')
    if not 0<=opts.long_hours<=12:
        args.error('Long fixtures must be between zero and twelve hours.')
    out.mkdir(parents=True,exist_ok=True)
    generate(out,'aac-editlist','aac','m4a')
    generate(out,'mp3-no-lame','libmp3lame','mp3',extra=('-write_xing','0'))
    generate(out,'vorbis','libvorbis','ogg')
    generate(out,'mixed-output-01','flac','flac',frames=10001)
    generate(out,'mixed-output-02','flac','flac',frames=11003,offset=10001)
    d=generate(out,'mp3-long-padding','libmp3lame','mp3')
    # Crop the declared tail to exercise padding that spans several packets. Keep the
    # Xing/LAME tag's checksum valid so strict platform readers see the same trim.
    path=out/'mp3-long-padding.mp3';data=bytearray(path.read_bytes())
    tag=data.find(b'Lavc',0,4096)
    if tag<0:
        tag=data.find(b'LAME',0,4096)
    if tag<0 or tag+36>len(data):
        raise RuntimeError('The encoder did not write a bounded gapless tag.')
    start=10+sum((data[6+i]&127)<<(21-7*i) for i in range(4)) if data[:3]==b'ID3' else 0
    def crc16(content):
        value=0
        for byte in content:
            value^=byte
            for _ in range(8):
                value=(value>>1)^0xa001 if value&1 else value>>1
        return value
    crc_at=tag+34
    if crc16(data[start:crc_at])!=int.from_bytes(data[crc_at:crc_at+2],'big'):
        raise RuntimeError('The encoder gapless tag checksum is not recognized.')
    padding=2500;data[tag+22]=(data[tag+22]&240)|(padding>>8);data[tag+23]=padding&255
    data[crc_at:crc_at+2]=crc16(data[start:crc_at]).to_bytes(2,'big');path.write_bytes(data)
    facts=json.loads(run('ffprobe','-v','error','-flags2','+skip_manual','-select_streams','a:0','-show_packets','-of','json',str(path)))
    end=sum(int(sd.get('discard_padding',0)) for pkt in facts['packets'] for sd in pkt.get('side_data_list',[]))
    if end<=1152:
        raise RuntimeError('The multi-packet padding fixture was not recognized.')
    d['trim']['endFrames']=end;d['durationFrames']=d['rawFrames']-d['trim']['startFrames']-end
    (out/'mp3-long-padding.json').write_text(json.dumps(d,indent=1)+'\n')
    if opts.long_hours:
        # Encode the long source, then obtain packet counts without a multi-hour frame decode.
        # The bounded unit cases above stay separate from this intentionally larger device fixture.
        path=out/'long.mp3'
        run('ffmpeg','-v','error','-y','-f','lavfi','-i','sine=frequency=441:sample_rate=44100',
            '-t',str(opts.long_hours*3600),'-c:a','libmp3lame','-b:a','64k',str(path))
        (out/'long.json').write_text(json.dumps(dict(file=path.name,seekSeconds=[1800,opts.long_hours*3600-30],
            acceptance='Compare each deep seek with the same frame range of a full decode; no prefix download through the optional web engine.'))+'\n')


if __name__=='__main__':
    main()
