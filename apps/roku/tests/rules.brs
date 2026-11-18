' Checks of the app's pure rules against what the other Portico apps and a reference QR library
' answer. Run with tools/test.sh (the brs interpreter); nothing here runs on a Roku.
' The expected values were produced once from packages/client-core (serverAddressCandidates),
' packages/design (generatedCoverTint) and the Python qrcode library (byte mode, level M, mask 0).

sub main()
    m.failed = 0
    m.passed = 0

    same("address " + "192.168.1.20", addressCandidates("192.168.1.20"), ["http://192.168.1.20:32500", "https://192.168.1.20:32500", "https://192.168.1.20", "http://192.168.1.20"])
    same("address " + "nas", addressCandidates("nas"), ["http://nas:32500", "https://nas:32500", "https://nas", "http://nas"])
    same("address " + "media.example.com", addressCandidates("media.example.com"), ["https://media.example.com", "https://media.example.com:32500", "http://media.example.com:32500"])
    same("address " + "nas.local:8443", addressCandidates("nas.local:8443"), ["http://nas.local:8443", "https://nas.local:8443"])
    same("address " + "demo.getportico.tv", addressCandidates("demo.getportico.tv"), ["https://demo.getportico.tv", "https://demo.getportico.tv:32500", "http://demo.getportico.tv:32500"])
    same("address " + "https://demo.getportico.tv/", addressCandidates("https://demo.getportico.tv/"), ["https://demo.getportico.tv"])
    same("address " + "http://10.0.0.5:32500", addressCandidates("http://10.0.0.5:32500"), ["http://10.0.0.5:32500"])
    same("address " + "10.1.2.3:80", addressCandidates("10.1.2.3:80"), ["http://10.1.2.3:80", "https://10.1.2.3:80"])
    same("address " + "8.8.8.8", addressCandidates("8.8.8.8"), ["https://8.8.8.8", "https://8.8.8.8:32500", "http://8.8.8.8:32500"])
    same("address " + "bad address", addressCandidates("bad address"), [])
    same("address " + "ftp://x", addressCandidates("ftp://x"), [])
    same("address " + "user@host", addressCandidates("user@host"), [])
    same("address " + "", addressCandidates(""), [])

    same("tint " + "Persuasion", [coverTint("Persuasion").toStr()], ["6"])
    same("tint " + "The Lost World", [coverTint("The Lost World").toStr()], ["2"])
    same("tint " + "Little Nemo in Slumberland", [coverTint("Little Nemo in Slumberland").toStr()], ["2"])
    same("tint " + "Breaking Bad", [coverTint("Breaking Bad").toStr()], ["3"])
    same("tint " + "俺だけレベルアップな件", [coverTint("俺だけレベルアップな件").toStr()], ["6"])
    same("tint " + "😀 Emoji", [coverTint("😀 Emoji").toStr()], ["6"])
    ' Customize Home: a held row dropped up, down, at the end, and past a row this TV never lists
    same("layout up", movedLayout(["a", "b", "c", "d"], [0, 1, 2, 3], 3, 1), ["a", "d", "b", "c"])
    same("layout down", movedLayout(["a", "b", "c", "d"], [0, 1, 2, 3], 0, 2), ["b", "c", "a", "d"])
    same("layout to the end", movedLayout(["a", "b", "c", "d"], [0, 1, 2, 3], 1, 3), ["a", "c", "d", "b"])
    same("layout unlisted", movedLayout(["a", "reading", "b", "c"], [0, 2, 3], 0, 1), ["reading", "b", "a", "c"])
    same("layout no move", movedLayout(["a", "b"], [0, 1], 1, 1), ["a", "b"])

    same("qr " + "A", qrMatrix("A"), ["111111100111001111111", "100000101011101000001", "101110100010001011101", "101110100100001011101", "101110101100101011101", "100000100010101000001", "111111101010101111111", "000000000001100000000", "101010100011000010010", "100111000100001000110", "010000101110100010001", "101010000110001000100", "101001101010101010101", "000000001111010101010", "111111100001011101111", "100000100101110111000", "101110101101011101101", "101110100000001000110", "101110101100100010001", "100000100110001000110", "111111101000101010111"])
    same("qr " + "https://demo.getportico.tv/device#code=7", qrMatrix("https://demo.getportico.tv/device#code=7KQM-4TXP"), ["111111100101010101110011001111111", "100000101100001110111111001000001", "101110100111111001101011001011101", "101110100101011111101110101011101", "101110101010001010101001101011101", "100000100010101110011011101000001", "111111101010101010101010101111111", "000000000111010101110111100000000", "101010100011110011001101100010010", "011001001010110011001100011100011", "101001101101101010101000010100101", "111110001011011111111100100010000", "011100110001101010101001101101011", "101001010100001110111110001101001", "000000100110110111011000010111011", "101101001101011011101111101000010", "010010100100011111110101111000000", "100011001110101010001010111100011", "001000101111000001001010011000111", "110000000000101010000101101110001", "001100101111111100000001111001001", "001110010011000100010111110001011", "100110101000100001000000011011011", "011110000101110101010101010000010", "101111101010101011000100111110001", "000000001100110001101111100011011", "111111100110011010100011101010111", "100000100000011011101100100010000", "101110101011111010111010111111000", "101110100110100111111100000010101", "101110101100110110011011100011001", "100000100011011011001100011010010", "111111101111011101011101110010011"])
    same("qr " + "https://web.getportico.tv/device#code=7K", qrMatrix("https://web.getportico.tv/device#code=7KQM-4TXP&and-a-much-longer-address-to-reach-the-higher-versions-of-the-code-0123456789-0123456789-0123456789-0123456789-0123456789"), ["11111110001110110110111010100110111010111010001111111", "10000010101110011110110000010110111001111111001000001", "10111010001110010111010010111111010101110001001011101", "10111010001100110111100010011000000100010110101011101", "10111010110010101011000011111110111101111110001011101", "10000010011000101010100110001010001100100010001000001", "11111110101010101010101010101010101010101010101111111", "00000000001000000010110110001000111010111000000000000", "10101010001011010100011111111110101010011101000010010", "00111100010100100011101100011101000000010001010010001", "11010011111011101110111000011100000100000000110011111", "00001001100001110110011000001110110110011001010011010", "10010011011111110010101100010110101111111010011011000", "10110001101111011100011011001101010011000000000101001", "11110011100100100010101000011011110110001111101011001", "10101000011011001110101001000001100110011101001111001", "11010011010000110010001010011001101110011101111110011", "10110100001101110001001000101101110001001000000010011", "11010110001000100000010000011001110101011000110101111", "10101100001101111011011100000011111010101100000111010", "11100111011110100111010111011101111111101111001010000", "11000100111101001100110000110000000111010100000011101", "11111010001001101000101011110001000011000101000100111", "00111001111100011000101011011100100010001000110001001", "10001111101111100001011011111001111010101111111111001", "11001000111011010111011010001001100110010100100010011", "01111010100001010101001110101101000011011100101010111", "01011000101000010110011010001000100110011001100010010", "01011111101011011011011111111000111010111111111111011", "00101000010000111111111000101100100001010000000000111", "10011110010011001001110001010101110000011000001101111", "10010101011100100111000101110010110110111110001111000", "00110011101101001010011011001000111111111010000101000", "11000100000110100101101011011010110000011001111000101", "10100110010100111001111000000010110000011111101011101", "10100101010111101011010110000010100110011110111100011", "10101111110001101110101111111101111011011010010101011", "11111000000011011100001011111000010001011101100100111", "11011110100101101010101001000001010011010000001111111", "01111101100010100111000100000011110011001100011010010", "10111111110101011110101011110000110110101000001000011", "11101100000000001011100011100001000011001101111001101", "11011111010011000010011000100000000101011101111101111", "01100001010000110000000000100101110011001000011101000", "00010011111101101101101011111100110010101000111110011", "00000000100001001010001010001000100000001100100010011", "11111110011011001100010010101001000010001101101010111", "10000010000100010001111110001000110010101100100010001", "10111010100100010100010011111100100011111001111111010", "10111010011110110110110001010101110100000100100110011", "10111010101010111101001010011101100000010000111111000", "10000010000100100010001011000100110111111101110100010", "11111110111101001011111010101110100110101010100110111"])

    ' servers found on the network: the server's own answers (server-next/internal/discovery answer()),
    ' made byte for byte by the shell lane's script, and the same records with compressed names
    same("mdns question", [mdnsQuery().toHexString()], ["000000000001000000000000085F706F727469636F045F746370056C6F63616C00000C0001"])
    found = mdnsServer(hexBytes("000084000000000400000000085F706F727469636F045F746370056C6F63616C00000C000100000078002610506F727469636F2D6131623263336434085F706F727469636F045F746370056C6F63616C0010506F727469636F2D6131623263336434085F706F727469636F045F746370056C6F63616C000021800100000078001E000000007EF410706F727469636F2D6131623263336434056C6F63616C0010506F727469636F2D6131623263336434085F706F727469636F045F746370056C6F63616C000010800100000078005B03763D313269643D7372765F544334624F5F50564D6568506536334D413370766831417A7A7653624B6C6D436B64366D5F7965305049672366703D613162326333643465356636303731383239336134623563366437653866393010706F727469636F2D6131623263336434056C6F63616C0000018001000000780004C0A80114"), len("000084000000000400000000085F706F727469636F045F746370056C6F63616C00000C000100000078002610506F727469636F2D6131623263336434085F706F727469636F045F746370056C6F63616C0010506F727469636F2D6131623263336434085F706F727469636F045F746370056C6F63616C000021800100000078001E000000007EF410706F727469636F2D6131623263336434056C6F63616C0010506F727469636F2D6131623263336434085F706F727469636F045F746370056C6F63616C000010800100000078005B03763D313269643D7372765F544334624F5F50564D6568506536334D413370766831417A7A7653624B6C6D436B64366D5F7965305049672366703D613162326333643465356636303731383239336134623563366437653866393010706F727469636F2D6131623263336434056C6F63616C0000018001000000780004C0A80114") / 2)
    same("mdns answer", [found.id, found.fp, found.ip, found.port.toStr()], ["srv_TC4bO_PVMehPe63MA3pvh1AzzvSbKlmCkd6m_ye0PIg", "a1b2c3d4e5f60718293a4b5c6d7e8f90", "192.168.1.20", "32500"])
    found = mdnsServer(hexBytes("000084000000000400000000085F706F727469636F045F746370056C6F63616C00000C000100000078001310506F727469636F2D6131623263336434C00CC02B00218001000000780019000000007EF410706F727469636F2D6131623263336434C01AC02B0010800100000078005B03763D313269643D7372765F544334624F5F50564D6568506536334D413370766831417A7A7653624B6C6D436B64366D5F7965305049672366703D6131623263336434653566363037313832393361346235633664376538663930C050000180010000007800040A000007"), len("000084000000000400000000085F706F727469636F045F746370056C6F63616C00000C000100000078001310506F727469636F2D6131623263336434C00CC02B00218001000000780019000000007EF410706F727469636F2D6131623263336434C01AC02B0010800100000078005B03763D313269643D7372765F544334624F5F50564D6568506536334D413370766831417A7A7653624B6C6D436B64366D5F7965305049672366703D6131623263336434653566363037313832393361346235633664376538663930C050000180010000007800040A000007") / 2)
    same("mdns compressed names", [found.ip, found.port.toStr(), found.id], ["10.0.0.7", "32500", "srv_TC4bO_PVMehPe63MA3pvh1AzzvSbKlmCkd6m_ye0PIg"])
    same("mdns goodbye, question, short", [type(mdnsServer(hexBytes("000084000000000400000000085F706F727469636F045F746370056C6F63616C00000C000100000000002610506F727469636F2D6131623263336434085F706F727469636F045F746370056C6F63616C0010506F727469636F2D6131623263336434085F706F727469636F045F746370056C6F63616C000021800100000000001E000000007EF410706F727469636F2D6131623263336434056C6F63616C0010506F727469636F2D6131623263336434085F706F727469636F045F746370056C6F63616C000010800100000000005B03763D313269643D7372765F544334624F5F50564D6568506536334D413370766831417A7A7653624B6C6D436B64366D5F7965305049672366703D613162326333643465356636303731383239336134623563366437653866393010706F727469636F2D6131623263336434056C6F63616C0000018001000000000004C0A80114"), len("000084000000000400000000085F706F727469636F045F746370056C6F63616C00000C000100000000002610506F727469636F2D6131623263336434085F706F727469636F045F746370056C6F63616C0010506F727469636F2D6131623263336434085F706F727469636F045F746370056C6F63616C000021800100000000001E000000007EF410706F727469636F2D6131623263336434056C6F63616C0010506F727469636F2D6131623263336434085F706F727469636F045F746370056C6F63616C000010800100000000005B03763D313269643D7372765F544334624F5F50564D6568506536334D413370766831417A7A7653624B6C6D436B64366D5F7965305049672366703D613162326333643465356636303731383239336134623563366437653866393010706F727469636F2D6131623263336434056C6F63616C0000018001000000000004C0A80114") / 2)), type(mdnsServer(mdnsQuery(), mdnsQuery().count())), type(mdnsServer(hexBytes("000084000000000400000000085F706F727469636F045F746370056C6F63616C00000C000100000078002610506F727469636F2D6131623263336434085F706F727469636F045F746370056C6F63616C0010506F727469636F2D6131623263336434085F706F727469636F045F746370056C6F63616C000021800100000078001E000000007EF410706F727469636F2D6131623263336434056C6F63616C0010506F727469636F2D6131623263336434085F706F727469636F045F746370056C6F63616C000010800100000078005B03763D313269643D7372765F544334624F5F50564D6568506536334D413370766831417A7A7653624B6C6D436B64366D5F7965305049672366703D613162326333643465356636303731383239336134623563366437653866393010706F727469636F2D6131623263336434056C6F63616C0000018001000000780004C0A80114"), 40))], ["Invalid", "Invalid", "Invalid"])

    same("duration", [durationText(3840000), durationText(3120000), durationText(3600000), durationText(20000)], ["1 h 4 min", "52 min", "1 h", "1 min"])
    same("episode", [episodeCode({parentIndex: 1, index: 3}), episodeCode({index: 3})], ["S1 E3", ""])
    same("time left", [timeLeftText({durationMs: 7200000, state: {positionMs: 3360000}}), timeLeftText({durationMs: 7200000, state: {}})], ["1 h 4 min left", ""])
    ' a time of day by the TV's own clock: the seconds that read 4:02 PM, 12:30 AM, noon and 9:45 AM there
    clock = createObject("roDateTime")
    clock.fromSeconds(0)
    clock.toLocalTime()
    shift = clock.getHours() * 3600 + clock.getMinutes() * 60
    at = function(h as integer, mins as integer, shift as integer) as integer
        return (h * 3600 + mins * 60 - shift + 86400) mod 86400
    end function
    same("clock", [guideTime(at(16, 2, shift), true), guideTime(at(0, 30, shift), true), guideTime(at(12, 0, shift), true), guideTime(at(9, 45, shift), true), guideTime(at(16, 2, shift), false)], ["4:02 PM", "12:30 AM", "12:00 PM", "9:45 AM", "4:02"])
    same("tries", [triesText(1, "4:02 PM"), triesText(6, "4:02 PM")], ["1 try since 4:02 PM", "6 tries since 4:02 PM"])
    same("reading left out", [withoutReading([{kind: "movie"}, {kind: "book"}, {kind: "series", publicationType: "book"}, {kind: "audiobook"}]).count().toStr()], ["2"])
    kept = keptOrder(["b", "a", "c"], [{id: "a"}, {id: "d"}, {id: "b"}])
    same("kept order", [kept[0].id, kept[1].id, kept[2].id], ["b", "a", "d"])
    ' a library's toolbar offers only the quick filters its own filters list names
    quick = []
    for each q in quickFilters([{id: "genre"}, {id: "inList"}, {id: "watched"}])
        quick.push(q.text)
    end for
    same("quick filters", quick, ["Unplayed", "My List"])
    specs = filterSpecs([{id: "studio"}, {id: "watched"}, {id: "genre"}, {id: "year"}])
    names = []
    for each s in specs
        names.push(s.id)
    end for
    same("filter panel", names, ["genre", "year", "studio", "playState"])
    same("filter params", [filterParams({genre: [{value: "tag_a", label: "Drama"}, {value: "tag_b", label: "Comedy"}], year: [{value: "1990..1999", label: "1990s"}]}, specs), filterParams({}, specs)], ["&genre=tag_a,tag_b&year=1990..1999", ""])
    same("play state params", [filterParams({playState: [{value: "watched=false", label: "Unplayed"}]}, specs)], ["&watched=false"])
    same("filter summary", [filterSummary([]), filterSummary([{label: "1980s"}]), filterSummary([{label: "a"}, {label: "b"}])], ["Any", "1980s", "2 chosen"])
    same("saved view name", [savedViewName("Films", ["Unplayed", "1990s"], "Title A–Z"), savedViewName("Films", [], "Year")], ["Films: Unplayed, 1990s", "Films, Year"])
    same("copies told apart", copyNames([{title: "Dune", year: 2021, libraryId: "a"}, {title: "Dune", year: 2021, libraryId: "b"}, {title: "Alien", year: 1979, libraryId: "a"}], {a: "Films", b: "Films from TMDB"}), ["Films", "Films from TMDB", ""])
    same("top result kind", [topKindName("show"), topKindName("movie"), topKindName("track")], ["TV show", "Film", ""])
    same("search found", [foundText({total: 20, totalIsMinimum: true}), foundText({total: 4}), foundText({})], ["20+", "4", ""])

    ' the capability table against the profiles the server records for Roku (server-next/internal/streaming/profiles)
    recorded = parseJson("{""roku_hd"":{""version"":2,""engineSetting"":""automatic"",""client"":{""family"":""roku"",""form"":""tv"",""platform"":""roku"",""os"":""roku"",""app"":""portico-roku""},""display"":{""width"":1920,""height"":1080,""dynamicRanges"":[""sdr""],""maxFrameRate"":60},""audioOutput"":{""route"":""hdmi"",""maxChannels"":2},""engines"":{""native"":{""name"":""roku-video"",""evidence"":""declared"",""video"":[{""codec"":""h264"",""evidence"":""declared"",""bitDepths"":[8],""maxWidth"":1920,""maxHeight"":1080,""maxLevel"":42,""maxFrameRate"":60,""dynamicRanges"":[""sdr""],""dolbyVisionProfiles"":[],""codecTags"":[]}],""audio"":[{""codec"":""aac"",""maxChannels"":6,""evidence"":""declared""},{""codec"":""mp3"",""maxChannels"":2,""evidence"":""declared""},{""codec"":""flac"",""maxChannels"":2,""evidence"":""declared""},{""codec"":""pcm"",""maxChannels"":2,""evidence"":""declared""},{""codec"":""alac"",""maxChannels"":2,""evidence"":""declared""},{""codec"":""opus"",""maxChannels"":2,""evidence"":""declared""},{""codec"":""vorbis"",""maxChannels"":2,""evidence"":""declared""}],""audioPassthrough"":[{""codec"":""ac3"",""maxChannels"":6,""evidence"":""declared""},{""codec"":""eac3"",""maxChannels"":8,""evidence"":""declared"",""objectAudio"":[""atmos""]},{""codec"":""dts"",""maxChannels"":8,""evidence"":""declared"",""variants"":[""dts_core""]}],""transports"":[{""transport"":""direct"",""containers"":[""mp4"",""m4v"",""mov"",""mkv"",""ts"",""m2ts""],""video"":[""h264""],""audio"":[""aac"",""mp3"",""ac3"",""eac3"",""flac"",""pcm"",""alac"",""opus"",""vorbis"",""dts""]},{""transport"":""direct"",""containers"":[""mp3"",""m4a"",""aac"",""flac"",""wav"",""ogg""],""video"":[],""audio"":[""aac"",""mp3"",""flac"",""pcm"",""alac"",""opus"",""vorbis""]},{""transport"":""hls_ts"",""containers"":[""mpegts""],""video"":[""h264""],""audio"":[""aac"",""mp3"",""ac3"",""eac3""]},{""transport"":""hls_fmp4"",""containers"":[""fmp4""],""video"":[""h264""],""audio"":[""aac"",""ac3"",""eac3""]}],""subtitleRendering"":[""text""],""liveNeedsHls"":true}}},""roku_4k"":{""version"":2,""engineSetting"":""automatic"",""client"":{""family"":""roku"",""form"":""tv"",""platform"":""roku"",""os"":""roku"",""app"":""portico-roku""},""display"":{""width"":3840,""height"":2160,""dynamicRanges"":[""sdr"",""hdr10"",""hlg"",""hdr10plus"",""dolby_vision""],""maxFrameRate"":60},""audioOutput"":{""route"":""hdmi"",""maxChannels"":2},""engines"":{""native"":{""name"":""roku-video"",""evidence"":""declared"",""video"":[{""codec"":""h264"",""evidence"":""declared"",""bitDepths"":[8,10],""maxWidth"":3840,""maxHeight"":2160,""maxLevel"":51,""maxFrameRate"":60,""dynamicRanges"":[""sdr"",""dolby_vision""],""dolbyVisionProfiles"":[9],""codecTags"":[""avc1"",""dva1"",""dvav""]},{""codec"":""hevc"",""evidence"":""declared"",""bitDepths"":[8,10],""maxWidth"":3840,""maxHeight"":2160,""maxLevel"":153,""maxFrameRate"":60,""dynamicRanges"":[""sdr"",""hdr10"",""hlg"",""hdr10plus"",""dolby_vision""],""dolbyVisionProfiles"":[5],""codecTags"":[""dvh1"",""dvhe"",""hev1"",""hvc1""]},{""codec"":""vp9"",""evidence"":""declared"",""bitDepths"":[8,10],""maxWidth"":3840,""maxHeight"":2160,""maxFrameRate"":60,""dynamicRanges"":[""sdr"",""hdr10"",""hlg""]},{""codec"":""av1"",""evidence"":""declared"",""bitDepths"":[8,10],""maxWidth"":3840,""maxHeight"":2160,""maxFrameRate"":60,""dynamicRanges"":[""sdr"",""hdr10"",""hlg"",""hdr10plus""]}],""audio"":[{""codec"":""aac"",""maxChannels"":6,""evidence"":""declared""},{""codec"":""mp3"",""maxChannels"":2,""evidence"":""declared""},{""codec"":""flac"",""maxChannels"":2,""evidence"":""declared""},{""codec"":""pcm"",""maxChannels"":2,""evidence"":""declared""},{""codec"":""alac"",""maxChannels"":2,""evidence"":""declared""},{""codec"":""opus"",""maxChannels"":2,""evidence"":""declared""},{""codec"":""vorbis"",""maxChannels"":2,""evidence"":""declared""}],""audioPassthrough"":[{""codec"":""ac3"",""maxChannels"":6,""evidence"":""declared""},{""codec"":""eac3"",""maxChannels"":8,""evidence"":""declared"",""objectAudio"":[""atmos""]},{""codec"":""dts"",""maxChannels"":8,""evidence"":""declared"",""variants"":[""dts_core""]}],""transports"":[{""transport"":""direct"",""containers"":[""mp4"",""m4v"",""mov"",""mkv"",""ts"",""m2ts""],""video"":[""h264"",""hevc"",""vp9"",""av1""],""audio"":[""aac"",""mp3"",""ac3"",""eac3"",""flac"",""pcm"",""alac"",""opus"",""vorbis"",""dts""]},{""transport"":""direct"",""containers"":[""mp3"",""m4a"",""aac"",""flac"",""wav"",""ogg""],""video"":[],""audio"":[""aac"",""mp3"",""flac"",""pcm"",""alac"",""opus"",""vorbis""]},{""transport"":""hls_ts"",""containers"":[""mpegts""],""video"":[""h264"",""hevc"",""vp9"",""av1""],""audio"":[""aac"",""mp3"",""ac3"",""eac3""]},{""transport"":""hls_fmp4"",""containers"":[""fmp4""],""video"":[""h264"",""hevc"",""vp9"",""av1""],""audio"":[""aac"",""ac3"",""eac3""]}],""subtitleRendering"":[""text""],""liveNeedsHls"":true}}}}")
    same("profile roku_hd", [formatJson(sorted(rokuProfile({uhd: false})))], [formatJson(sorted(recorded.roku_hd))])
    same("profile roku_4k", [formatJson(sorted(rokuProfile({uhd: true, av1: true, dolbyVision: true})))], [formatJson(sorted(recorded.roku_4k))])
    ' Playback info says what client-core's playbackInfoRows says for the same facts (it writes 8.0 Mbps; Roku keeps its quality list's 8 Mbps)
    info = playbackInfoRows({decision: {video: {action: "transcode", reasons: ["video_codec_unsupported"], to: "h264"}, audio: {action: "copy"}}, source: {container: "mkv", videoCodec: "hevc", audioCodec: "eac3", width: 3840, height: 2160}, bitrate: "8 Mbps", chosenQuality: "1080p"})
    got = []
    for each row in info
        got.push(row.label + ": " + row.value)
    end for
    same("playback info", got, ["Playing: Converting on the server.", "Why: You chose 1080p. This device can’t play the video format.", "Video: HEVC · 3840×2160 → H.264", "Audio: Dolby Digital Plus", "Container: MKV", "Bitrate: 8 Mbps"])
    plain = playbackInfoRows({decision: {video: {action: "direct"}, audio: {action: "copy", reasons: ["stream_normalized"]}}, source: {videoCodec: "h264"}})
    same("playback info repackaged", [plain[0].value, plain[1].value, plain.count().toStr()], ["Original picture and sound, repackaged for this device.", "The stream’s timing is repaired as it plays.", "3"])

    ' Settings' two composite choices write what packages/client-core compositePatch writes, and read back the same
    same("playback method", [formatJson(sorted(compositePatch("playbackMethod", "original"))), formatJson(sorted(compositePatch("playbackMethod", "convert"))), compositeValue("playbackMethod", compositePatch("playbackMethod", "original")), compositeValue("playbackMethod", compositePatch("playbackMethod", "convert")), compositeValue("playbackMethod", compositePatch("playbackMethod", "automatic"))], [formatJson(sorted({"delivery.directPlay": "require", "delivery.directStream": "never", "delivery.transcode": "never"})), formatJson(sorted({"delivery.directPlay": "never", "delivery.directStream": "never", "delivery.transcode": "require"})), "original", "convert", "automatic"])
    same("still watching", [compositeValue("stillWatching", compositePatch("stillWatching", "0")), compositeValue("stillWatching", compositePatch("stillWatching", "4")), formatJson(compositePatch("stillWatching", "0"))], ["0", "4", formatJson({"playback.passoutProtection": false})])
    same("appearance looks", [appearanceLooks({"appearance.showBackdrops": false}).showBackdrops, appearanceLooks({}).showBackdrops, appearanceLooks({"appearance.motion": "reduce"}).reduceMotion], [false, true, true])
    same("switch on", [switchIsOn({on: "reduce"}, "follow"), switchIsOn({on: "reduce"}, "reduce"), switchIsOn({}, true), switchIsOn({}, "follow"), switchIsOn({}, invalid)], [false, true, true, false, false])
    same("language moves", [movedLanguage(["en", "fr", "de"], 1, -1).join(","), movedLanguage(["en", "fr"], 1, 1).join(","), withoutLanguage(["en", "fr", "de"], 1).join(","), languagesWords([]), languagesWords(["@appLanguage", "fr"])], ["fr,en,de", "en,fr", "en,de", "None chosen", "Portico’s language, French"])
    found = settingsMatches(settingsPages(), "Subtitle")
    labels = []
    for each match in found[0].rows
        labels.push(match.row.label)
    end for
    same("settings search", [found.count(), settingsPages()[found[0].page].id, labels.join("|"), settingsMatches(settingsPages(), "  ").count(), settingsMatches(settingsPages(), "privacy")[0].rows.count(), settingsPages()[found[1].page].id], [2, "languages", "Show subtitles|Preferred subtitle languages|Size|Background", 0, 0, "region"])

    print "PASSED " + m.passed.toStr() + " FAILED " + m.failed.toStr()
end sub

sub same(name as string, got as dynamic, want as object)
    ok = got <> invalid and got.count() = want.count()
    if ok
        for i = 0 to want.count() - 1
            if got[i] <> want[i] then ok = false
        end for
    end if
    if ok
        m.passed = m.passed + 1
    else
        m.failed = m.failed + 1
        print "FAIL " + name
    end if
end sub

' The same value with every object's keys in one order, so two documents can be compared as text.
function sorted(value as dynamic) as dynamic
    if type(value) = "roAssociativeArray"
        out = createObject("roArray", 0, true)
        names = {}
        lowered = []
        for each key in value.keys()
            names[lcase(key)] = key
            lowered.push(lcase(key))
        end for
        lowered.sort()
        for each key in lowered
            out.push([key, sorted(value[names[key]])])
        end for
        return out
    else if type(value) = "roArray"
        out = []
        for each entry in value
            out.push(sorted(entry))
        end for
        return out
    end if
    return value
end function

function hexBytes(hex as string) as object
    bytes = createObject("roByteArray")
    bytes.fromHexString(hex)
    return bytes
end function
