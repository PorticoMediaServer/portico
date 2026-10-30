import subprocess, tempfile, pathlib, json, base64, struct
# Run with the repository's qualified FFmpeg to refresh the short alignment fixtures.
out={}
with tempfile.TemporaryDirectory() as tmp:
 for family,codec,container,ext in [('mp3','libmp3lame','mp3','mp3'),('aac','aac','mp4','m4a'),('opus','libopus','ogg','opus'),('vorbis','libvorbis','ogg','ogg'),('flac','flac','flac','flac')]:
  rate=48000 if family=='opus' else 44100
  path=pathlib.Path(tmp)/('probe.'+ext)
  subprocess.run(['ffmpeg','-v','error','-y','-f','lavfi','-i',f'aevalsrc=0.3*sin(2*PI*(317*n/{rate}+0.003*n*n/{rate}))+0.1*sin(2*PI*997*n/{rate}):s={rate}', '-t','0.18','-ac','2','-c:a',codec,str(path)],check=True)
  raw=subprocess.check_output(['ffmpeg','-v','error','-flags2','+skip_manual','-i',str(path),'-ac','2','-f','f32le','pipe:1'])
  samples=struct.unpack('<'+'f'*(len(raw)//4),raw)[::2]
  anchor=4096
  out[family]={'container':container,'data':base64.b64encode(path.read_bytes()).decode(),'anchor':anchor,'reference':[round(x,7) for x in samples[anchor:anchor+128]],'rawFrames':len(samples)}
p=pathlib.Path(__file__).resolve().parents[2]/'web/src/platform/playback/audio/decoder-probes.ts'
p.write_text('/** Short synthetic chirps for runtime decoder alignment checks. No library media is used. */\nexport const decoderProbes = '+json.dumps(out,separators=(',',':'))+' as const;\n')
