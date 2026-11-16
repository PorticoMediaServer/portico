' What this Roku can play, as the capability document the server plans from (API.md, "Device
' capability document"). rokuProfile() is the same table as every other Portico client keeps for
' Roku (packages/client-core client-profile.ts rokuDeclaredProfile); deviceProfile() settles its
' options, and what the screen and sound output carry right now, by asking the device.

function videoEntry(codec as string, fields as object) as object
    fields.codec = codec
    fields.evidence = "declared"
    return fields
end function

' A list's own copy: one list is never put in two places of the document.
function copyOf(list as object) as object
    out = []
    out.append(list)
    return out
end function

function soundEntry(codec as string, channels as integer) as object
    return {codec: codec, maxChannels: channels, evidence: "declared"}
end function

' options: {uhd, av1, dolbyVision, osVersion, model}
function rokuProfile(options as object) as object
    uhd = options.uhd <> false
    width = 1920
    height = 1080
    if uhd
        width = 3840
        height = 2160
    end if
    dolby = uhd and options.dolbyVision = true
    av1 = uhd and options.av1 = true
    hdr = ["sdr"]
    if uhd
        hdr = ["sdr", "hdr10", "hlg"]
        if av1 then hdr.push("hdr10plus")
    end if
    ranges = []
    ranges.append(hdr)
    if dolby then ranges.push("dolby_vision")

    h264 = {bitDepths: [8], maxWidth: width, maxHeight: height, maxLevel: 42, maxFrameRate: 60, dynamicRanges: ["sdr"], dolbyVisionProfiles: [], codecTags: []}
    if uhd then h264.maxLevel = 51
    if dolby
        h264.bitDepths = [8, 10]
        h264.dynamicRanges = ["sdr", "dolby_vision"]
        h264.dolbyVisionProfiles = [9]
        h264.codecTags = ["avc1", "dva1", "dvav"]
    end if
    video = [videoEntry("h264", h264)]
    names = ["h264"]
    if uhd
        hevc = {bitDepths: [8, 10], maxWidth: width, maxHeight: height, maxLevel: 153, maxFrameRate: 60, dynamicRanges: copyOf(ranges), dolbyVisionProfiles: [], codecTags: []}
        if dolby
            hevc.dolbyVisionProfiles = [5]
            hevc.codecTags = ["dvh1", "dvhe", "hev1", "hvc1"]
        end if
        video.push(videoEntry("hevc", hevc))
        vp9Ranges = []
        for each r in hdr
            if r <> "hdr10plus" then vp9Ranges.push(r)
        end for
        video.push(videoEntry("vp9", {bitDepths: [8, 10], maxWidth: width, maxHeight: height, maxFrameRate: 60, dynamicRanges: vp9Ranges}))
        names.push("hevc")
        names.push("vp9")
        if av1
            video.push(videoEntry("av1", {bitDepths: [8, 10], maxWidth: width, maxHeight: height, maxFrameRate: 60, dynamicRanges: copyOf(hdr)}))
            names.push("av1")
        end if
    end if

    client = {family: "roku", form: "tv", platform: "roku", os: "roku", app: "portico-roku"}
    if options.osVersion <> invalid then client.osVersion = options.osVersion
    if options.model <> invalid then client.model = options.model
    eac3 = soundEntry("eac3", 8)
    eac3.objectAudio = ["atmos"]
    dts = soundEntry("dts", 8)
    dts.variants = ["dts_core"]
    return {
        version: 2, engineSetting: "automatic", client: client,
        display: {width: width, height: height, dynamicRanges: ranges, maxFrameRate: 60},
        audioOutput: {route: "hdmi", maxChannels: 2},
        engines: {native: {
            name: "roku-video", evidence: "declared", video: video,
            audio: [soundEntry("aac", 6), soundEntry("mp3", 2), soundEntry("flac", 2), soundEntry("pcm", 2), soundEntry("alac", 2), soundEntry("opus", 2), soundEntry("vorbis", 2)],
            audioPassthrough: [soundEntry("ac3", 6), eac3, dts],
            transports: [
                {transport: "direct", containers: ["mp4", "m4v", "mov", "mkv", "ts", "m2ts"], video: names, audio: ["aac", "mp3", "ac3", "eac3", "flac", "pcm", "alac", "opus", "vorbis", "dts"]},
                {transport: "direct", containers: ["mp3", "m4a", "aac", "flac", "wav", "ogg"], video: [], audio: ["aac", "mp3", "flac", "pcm", "alac", "opus", "vorbis"]},
                {transport: "hls_ts", containers: ["mpegts"], video: copyOf(names), audio: ["aac", "mp3", "ac3", "eac3"]},
                {transport: "hls_fmp4", containers: ["fmp4"], video: copyOf(names), audio: ["aac", "ac3", "eac3"]}
            ],
            subtitleRendering: ["text"]
        }}
    }
end function

' The table with its options settled on this device, and narrowed to what it reports right now:
' the screen's dynamic ranges, and the surround formats its sound output takes.
function deviceProfile() as object
    info = createObject("roDeviceInfo")
    can = function(info as object, codec as string, profile as string, level as string) as boolean
        ask = {Codec: codec, Container: "mp4"}
        if profile <> "" then ask.Profile = profile
        if level <> "" then ask.Level = level
        answer = info.canDecodeVideo(ask)
        return answer <> invalid and answer.result = true
    end function
    shows = info.getDisplayProperties()
    options = {
        uhd: can(info, "hevc", "main 10", "5.1"),
        av1: can(info, "av1", "", ""),
        dolbyVision: shows <> invalid and shows.DolbyVision = true,
        osVersion: info.getOSVersion().major + "." + info.getOSVersion().minor + "." + info.getOSVersion().revision,
        model: info.getModel()
    }
    doc = rokuProfile(options)
    doc.engines.native.evidence = "mixed"

    ' the screen: only the ranges it shows now
    onScreen = {sdr: true, hdr10: shows <> invalid and shows.Hdr10 = true, hdr10plus: shows <> invalid and shows.Hdr10Plus = true, hlg: shows <> invalid and shows.HLG = true, dolby_vision: shows <> invalid and shows.DolbyVision = true}
    kept = []
    for each r in doc.display.dynamicRanges
        if onScreen[r] = true then kept.push(r)
    end for
    doc.display.dynamicRanges = kept

    ' surround handed on undecoded: only what the output takes
    passed = []
    for each entry in doc.engines.native.audioPassthrough
        answer = info.canDecodeAudio({Codec: entry.codec})
        if answer <> invalid and answer.result = true then passed.push(entry)
    end for
    doc.engines.native.audioPassthrough = passed
    return doc
end function
