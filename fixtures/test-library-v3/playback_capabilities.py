"""Declared capabilities of the FFmpeg-backed synthetic API player."""

def capabilities():
    return {'version':2,'client':{'family':'web','form':'browser'},
        'display':{'dynamicRanges':['sdr']},'audioOutput':{'maxChannels':8},
        'engines':{'native':{'name':'ffmpeg','evidence':'declared',
            'video':[{'codec':'h264','bitDepths':[8],'maxWidth':3840,'maxHeight':2160,'maxFrameRate':60,'dynamicRanges':['sdr']}],
            'audio':[{'codec':c,'maxChannels':8} for c in ['aac','mp3','flac','alac','opus','ac3','eac3','dts']],
            'transports':[
                {'transport':'direct','containers':['mp4','mkv','matroska','mp3','flac','ogg','mpegts','webm','m4a','m4b'], 'video':['h264'],'audio':['aac','mp3','flac','alac','opus','ac3','eac3','dts']},
                {'transport':'hls_ts','containers':['mpegts'],'video':['h264'],'audio':['aac','mp3']},
                {'transport':'hls_fmp4','containers':['fmp4'],'video':['h264'],'audio':['aac','mp3']}],
            'subtitleRendering':['text','ass'],'embeddedAudioSwitching':True}}}
