' How an item from the server is worded and pictured. Pure functions: no nodes, no requests.

function episodeCode(item as object) as string
    if item.parentIndex = invalid or item.index = invalid then return ""
    return "S" + item.parentIndex.toStr() + " E" + item.index.toStr()
end function

' "1 h 4 min", "52 min", "1 h"
function durationText(ms as dynamic) as string
    if ms = invalid then return ""
    minutes = int(ms / 60000 + 0.5)
    if minutes < 1 then minutes = 1
    h = int(minutes / 60)
    mins = minutes mod 60
    if h = 0 then return mins.toStr() + " min"
    if mins = 0 then return h.toStr() + " h"
    return h.toStr() + " h " + mins.toStr() + " min"
end function

' 0 to 1 while a title is part played, otherwise -1.
function progressOf(item as object) as float
    if item.state = invalid or item.state.positionMs = invalid or item.durationMs = invalid then return -1
    if item.durationMs <= 0 or item.state.positionMs <= 0 then return -1
    p = item.state.positionMs / item.durationMs
    if p > 1 then p = 1
    return p
end function

function timeLeftText(item as object) as string
    if progressOf(item) < 0 then return ""
    return durationText(item.durationMs - item.state.positionMs) + " left"
end function

' Reading (books, comics, magazines) is not on TV (definition, area 10: decision of 21 Oct), so a
' title that is a publication is left out of rows on this app.
function isReading(item as object) as boolean
    return item.publicationType <> invalid or item.kind = "book" or item.kind = "comic"
end function

function withoutReading(items as dynamic) as object
    out = []
    if items = invalid then return out
    for each item in items
        if not isReading(item) then out.push(item)
    end for
    return out
end function

' A book, comic, audiobook or album cover is always shown whole (ruling of 13 Nov).
function isCover(kind as string) as boolean
    return kind = "book" or kind = "audiobook" or kind = "comic" or kind = "album" or kind = "series" or kind = "track" or kind = "magazine"
end function

function pick(images as dynamic, names as object) as string
    if images = invalid then return ""
    for each name in names
        if images[name] <> invalid then return images[name]
    end for
    return ""
end function

' The picture path for a card of the given shape.
function cardArt(item as object, shape as string) as string
    if shape = "landscape" and not isCover(item.kind) then return pick(item.images, ["still", "backdrop", "thumb", "poster"])
    return pick(item.images, ["poster", "cover", "still", "backdrop"])
end function

' What a card shows: {id, kind, art, whole, title, sub, progress, badge, overlay}.
function cardFor(item as object, shape as string) as object
    card = {id: item.id, kind: item.kind, art: cardArt(item, shape), whole: isCover(item.kind), title: item.title, sub: "", progress: progressOf(item), badge: "", overlay: shape = "landscape"}
    if item.kind = "episode"
        if item.rootTitle <> invalid then card.title = item.rootTitle
        code = episodeCode(item)
        if code <> "" then card.sub = code + "   " + item.title else card.sub = item.title
    else if item.year <> invalid
        card.sub = item.year.toStr()
    end if
    return card
end function

function tagNames(item as object, kind as string, most as integer) as string
    out = ""
    if item.tags = invalid then return out
    n = 0
    for each tag in item.tags
        if tag.kind = kind and n < most
            if out <> "" then out = out + ", "
            out = out + tag.name
            n = n + 1
        end if
    end for
    return out
end function

function countText(n as integer, one as string, many as string) as string
    if n = 1 then return "1 " + one
    return n.toStr() + " " + many
end function

' A summary as one paragraph: a break in it would otherwise leave a line holding only the "…".
function oneParagraph(text as string) as string
    return createObject("roRegex", "\s*[\r\n]+\s*", "").replaceAll(text, " ")
end function

' The art a page stands on: the title's backdrop; with only a poster, "poster|<path>" (the poster is
' shown whole at the right); with no picture at all, "cover|<n>", its generated cover's tint.
function pageArt(item as object) as string
    art = pick(item.images, ["backdrop", "still", "thumb"])
    if art <> "" then return art
    poster = pick(item.images, ["poster", "cover"])
    if poster <> "" then return "poster|" + poster
    title = item.title
    if item.kind = "episode" and item.rootTitle <> invalid then title = item.rootTitle
    return "cover|" + coverTint(title).toStr()
end function

' What the hero shows for an item: {eyebrow, title, year, facts, tag, progress, left, synopsis, art}.
function heroFor(item as object) as object
    h = {eyebrow: "", title: item.title, year: "", facts: tagNames(item, "genre", 2), tag: "", progress: progressOf(item), left: timeLeftText(item), synopsis: "", art: pageArt(item)}
    if item.summary <> invalid then h.synopsis = oneParagraph(item.summary)
    if item.year <> invalid then h.year = item.year.toStr()
    if item.kind = "episode"
        if item.rootTitle <> invalid then h.title = item.rootTitle
        code = episodeCode(item)
        if code <> "" then h.eyebrow = code + "   " + item.title else h.eyebrow = item.title
    else if item.kind = "show" and item.counts <> invalid
        h.tag = countText(item.counts.children, "season", "seasons")
    else if item.kind = "movie" and h.progress < 0
        h.tag = durationText(item.durationMs)
    end if
    if item.contentRating <> invalid and h.tag = "" then h.tag = item.contentRating
    return h
end function

' Which of the eight generated-cover tints a title gets (0 to 7): the same rule as every other
' Portico app (packages/design generated-cover.ts), FNV-1a over the title's UTF-16 code units.
function coverTint(title as string) as integer
    ' the 32-bit hash is kept as two 16-bit halves, so no step leaves whole-number range
    hi = &h811C
    lo = &h9DC5
    for i = 1 to len(title)
        code = asc(mid(title, i, 1))
        units = [code]
        if code > 65535
            code = code - 65536
            units = [55296 + int(code / 1024), 56320 + (code mod 1024)]
        end if
        for each unit in units
            lo = (lo or unit) - (lo and unit)          ' exclusive or with the code unit
            ' times the FNV prime 16777619 = 256 * 65536 + 403, modulo 2^32
            low = lo * 403
            high = hi * 403 + lo * 256 + int(low / 65536)
            lo = low mod 65536
            hi = high mod 65536
        end for
    end for
    ' (hi * 65536 + lo) mod 8 is lo mod 8
    return lo mod 8
end function

' The side sheet for a title: {eyebrow, title, status, rows}. `inContinue` adds the Continue
' Watching row's own action.
function titleMenu(item as object, inContinue as boolean) as object
    title = item.title
    eyebrow = ""
    if item.kind = "episode"
        if item.rootTitle <> invalid then title = item.rootTitle
        eyebrow = episodeCode(item)
    end if
    status = ""
    if progressOf(item) >= 0 then status = "In progress      " + timeLeftText(item)
    rows = []
    playable = (item.kind = "movie" or item.kind = "episode")
    if playable
        if progressOf(item) >= 0 then rows.push({id: "play", text: "Resume", icon: "play"}) else rows.push({id: "play", text: "Play", icon: "play"})
    end if
    ' films and shows are watched (the mock-ups' word); books and music are played
    word = "played"
    if item.kind = "movie" or item.kind = "episode" or item.kind = "show" or item.kind = "season" then word = "watched"
    if item.state <> invalid and item.state.watched = true
        rows.push({id: "unplayed", text: "Mark as un" + word, icon: "refresh"})
    else
        rows.push({id: "played", text: "Mark as " + word, icon: "check"})
    end if
    if inContinue then rows.push({id: "dismiss", text: "Remove from Continue Watching", icon: "close"})
    if item.state <> invalid and item.state.inList = true
        rows.push({id: "unlist", text: "Remove from My List", icon: "close"})
    else
        rows.push({id: "list", text: "Add to My List", icon: "plus"})
    end if
    rows.push({id: "details", text: "Details", icon: "info", gapBefore: true})
    return {eyebrow: eyebrow, title: title, status: status, rows: rows}
end function

' The state change a menu row stands for, or invalid when the row is not a state change.
function titleMenuChange(item as object, which as string) as dynamic
    if which = "played" then return {watched: true}
    if which = "unplayed" then return {watched: false}
    if which = "list" then return {inList: true}
    if which = "unlist" then return {inList: false}
    if which = "dismiss" then return {continueDismissed: true}
    return invalid
end function

' "TC" for Timothée Chalamet: the first letters of the first and last names.
function initialsOf(name as string) as string
    parts = name.trim().split(" ")
    out = ucase(left(parts[0], 1))
    if parts.count() > 1 then out = out + ucase(left(parts[parts.count() - 1], 1))
    return out
end function

' A cast or crew member as a card: {id, kind: "person", art, title, sub, badge (initials), ...}.
function personCard(credit as object) as object
    part = ""
    if credit.character <> invalid and credit.character <> ""
        part = credit.character
    else if credit.role <> invalid
        part = ucase(left(credit.role, 1)) + mid(credit.role, 2)
    end if
    art = ""
    if credit.image <> invalid then art = credit.image
    return {id: credit.personId, kind: "person", art: art, whole: false, title: credit.name, sub: part, progress: -1, badge: initialsOf(credit.name), overlay: false}
end function

' The names in a title's credits with one role, in order, at most `most` of them.
function creditNames(credits as dynamic, role as string, most as integer) as string
    out = ""
    n = 0
    if credits = invalid then return out
    for each credit in credits
        if credit.role = role and n < most
            if out <> "" then out = out + ", "
            out = out + credit.name
            n = n + 1
        end if
    end for
    return out
end function

' "March 1, 2024" from "2024-03-01" or a full timestamp.
function dateText(iso as dynamic) as string
    if iso = invalid or len(iso) < 10 then return ""
    months = ["January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"]
    month = mid(iso, 6, 2).toInt()
    if month < 1 or month > 12 then return ""
    return months[month - 1] + " " + mid(iso, 9, 2).toInt().toStr() + ", " + left(iso, 4)
end function

' A title in a library's grid: its picture with its name and year under it.
function gridCard(item as object) as object
    card = cardFor(item, "poster")
    card.under = true
    card.oneLine = true
    card.artHeight = 300
    if item.kind = "collection" and item.counts <> invalid and item.counts.children > 0 then card.sub = countText(item.counts.children, "title", "titles")
    return card
end function

' A library's views, sorts and counts, in words.
function viewName(kind as string) as string
    names = {movie: "Movies", show: "Shows", collection: "Collections", artist: "Artists", album: "Albums", track: "Songs", audiobook: "Audiobooks", season: "Seasons", episode: "Episodes", playlist: "Playlists"}
    if names[kind] <> invalid then return names[kind]
    return ucase(left(kind, 1)) + mid(kind, 2)
end function

function sortName(id as string) as string
    names = {title: "Title A–Z", added: "Recently added", year: "Year", release: "Release date", duration: "Duration", communityRating: "Rating", criticRating: "Critics' rating", plays: "Most played", playCount: "Most played", userRating: "Your rating", lastPlayed: "Last played", resolution: "Resolution", latest: "Latest", artist: "Artist", random: "Random"}
    if names[id] <> invalid then return names[id]
    return ucase(left(id, 1)) + mid(id, 2)
end function

' The quick filters a library's toolbar offers: only those its own `filters` list names, in this order.
' Each is {id, text, param}: `param` is what the item list is asked with when the chip is on.
function quickFilters(filters as dynamic) as object
    named = {}
    if filters <> invalid
        for each f in filters
            named[f.id] = true
        end for
    end if
    out = []
    for each q in [{id: "watched", text: "Unplayed", param: "watched=false"}, {id: "inProgress", text: "In progress", param: "inProgress=true"}, {id: "favorite", text: "Favorites", param: "favorite=true"}, {id: "inList", text: "My List", param: "inList=true"}]
        if named[q.id] = true then out.push(q)
    end for
    return out
end function

' The filters of a library's Filters panel: those its own `filters` list names whose choices the
' server lists (GET /v1/facets), in the order the other apps show them. Each is {id, label}; the
' id is both the filter's parameter and its facet.
function filterSpecs(filters as dynamic) as object
    named = {}
    if filters <> invalid
        for each f in filters
            named[f.id] = true
        end for
    end if
    out = []
    for each spec in [{id: "genre", label: "Genre"}, {id: "year", label: "Year"}, {id: "contentRating", label: "Content rating"}, {id: "studio", label: "Studio"}, {id: "network", label: "Network"}, {id: "label", label: "Tag"}, {id: "resolution", label: "Resolution"}]
        if named[spec.id] = true then out.push(spec)
    end for
    return out
end function

' What the item list is asked with for the chosen values ({filter id: [{value, label}]}): each
' filter once, its values joined so that any of them passes.
function filterParams(chosen as object, specs as object) as string
    q = ""
    for each spec in specs
        values = chosen[spec.id]
        if values <> invalid and values.count() > 0
            joined = ""
            for each v in values
                if joined <> "" then joined = joined + ","
                joined = joined + v.value.encodeUriComponent()
            end for
            q = q + "&" + spec.id + "=" + joined
        end if
    end for
    return q
end function

' A filter heading's value in the panel: what is chosen under it.
function filterSummary(values as dynamic) as string
    if values = invalid or values.count() = 0 then return "Any"
    if values.count() = 1 then return values[0].label
    return values.count().toStr() + " chosen"
end function

' Why a read failed, in one sentence for a page or a section that could not load.
function whyFailed(result as object) as string
    if result.status = 0 then return "The server didn't answer. You're still signed in."
    return "The server couldn't send it. Try again in a moment."
end function

' Results that share a title and a year with another in the same row (the same film in two
' libraries) are told apart by their library's name: the second line of each of them, "" for the rest.
function copyNames(items as object, libraryNames as object) as object
    seen = {}
    for each item in items
        key = item.title + "|" + yearOf(item)
        if seen[key] = invalid then seen[key] = 0
        seen[key] = seen[key] + 1
    end for
    out = []
    for each item in items
        name = ""
        if seen[item.title + "|" + yearOf(item)] > 1 and item.libraryId <> invalid and libraryNames[item.libraryId] <> invalid then name = libraryNames[item.libraryId]
        out.push(name)
    end for
    return out
end function

function yearOf(item as object) as string
    if item.year = invalid then return ""
    return item.year.toStr()
end function

' A codec or a subtitle format as people write it ("Dolby Digital Plus", "HEVC", "SRT"); one this
' does not know is the server's name in capitals (client-core playback-info, and the track names).
function formatName(codec as dynamic) as string
    if codec = invalid then return ""
    names = {h264: "H.264", avc: "H.264", hevc: "HEVC", h265: "HEVC", av1: "AV1", vp9: "VP9", mpeg2video: "MPEG-2", vc1: "VC-1", aac: "AAC", ac3: "Dolby Digital", eac3: "Dolby Digital Plus", dts: "DTS", truehd: "Dolby TrueHD", flac: "FLAC", mp3: "MP3", alac: "ALAC", opus: "Opus", vorbis: "Vorbis", pcm: "PCM", subrip: "SRT", srt: "SRT", ass: "ASS", ssa: "SSA", webvtt: "WebVTT", vtt: "WebVTT", mov_text: "Text", hdmv_pgs_subtitle: "PGS", pgs: "PGS", dvd_subtitle: "VobSub", vobsub: "VobSub", idx: "VobSub"}
    if names[lcase(codec)] <> invalid then return names[lcase(codec)]
    return ucase(codec)
end function

' Playback info under More (client-core playbackInfoRows, in the same words): whether the file plays
' as it is or is converted and why, the picture and the sound from the file to this TV, the container
' and the bitrate. A fact nobody knows has no row. facts: {decision: {video, audio, subtitles} (each
' {action, reasons, to, channels}), delivery, chosenQuality, source: {container, videoCodec,
' audioCodec, width, height}, bitrate ("4.5 Mbps"), hdr: {source, delivered, toneMapped}}. Rows are {id, label, value}.
function playbackInfoRows(facts as object) as object
    rows = []
    d = facts.decision
    streams = []
    if d <> invalid then streams = [d.video, d.audio, d.subtitles]
    converting = (facts.delivery = "converted")
    repackaged = false
    if d <> invalid
        converting = false
        for each x in streams
            if isConverting(x) then converting = true
        end for
        for each x in streams
            if not converting and x <> invalid and x.action = "copy" then repackaged = true
        end for
    end if
    playing = "Original quality: the file plays as it is."
    if repackaged then playing = "Original picture and sound, repackaged for this device."
    if converting then playing = "Converting on the server."
    rows.push({id: "delivery", label: "Playing", value: playing})
    if converting or repackaged
        reasons = {container_unsupported: "This device can’t open the file’s container.", video_codec_unsupported: "This device can’t play the video format.", audio_codec_unsupported: "This device can’t play the audio format.", height_above_request: "The picture is larger than the quality in use.", bitrate_above_request: "The file’s bitrate is above the quality in use.", admin_cap_remote_bitrate: "The server owner limits quality away from home.", hdr_tone_mapping_required: "HDR is converted for this screen.", subtitle_image_needs_burn: "The subtitles are images and are drawn into the picture.", subtitle_burn_in: "The subtitles are images and are drawn into the picture.", stream_normalized: "The stream’s timing is repaired as it plays.", alternate_audio_fixed_output_policy: "The audio track you chose needs converting."}
        words = []
        seen = {}
        if facts.chosenQuality <> invalid and facts.chosenQuality <> "" then words.push("You chose " + facts.chosenQuality + ".")
        for each x in streams
            if x <> invalid and x.reasons <> invalid
                for each code in x.reasons
                    said = reasons[code]
                    if said <> invalid and seen[said] <> true
                        seen[said] = true
                        words.push(said)
                    end if
                end for
            end if
        end for
        why = words.join(" ")
        if why = "" and converting then why = "The original can’t play on this device as it is."
        if why <> "" then rows.push({id: "why", label: "Why", value: why})
    end if
    s = facts.source
    if s = invalid then s = {}
    parts = []
    if formatName(s.videoCodec) <> "" then parts.push(formatName(s.videoCodec))
    if s.width <> invalid and s.height <> invalid and s.width > 0 and s.height > 0 then parts.push(s.width.toStr() + "×" + s.height.toStr())
    from = parts.join(" · ")
    if d <> invalid and isConverting(d.video) and formatName(d.video.to) <> "" and formatName(d.video.to) <> from
        if from = "" then from = "Unknown"
        from = from + " → " + formatName(d.video.to)
    end if
    if from <> "" then rows.push({id: "video", label: "Video", value: from})
    hdr = facts.hdr
    if hdr <> invalid and hdr.source <> invalid and hdr.source <> ""
        value = hdr.source
        if hdr.delivered <> invalid and hdr.delivered <> "" then value = hdr.delivered
        if hdr.toneMapped = true then value = hdr.source + ", converted to standard range"
        rows.push({id: "hdr", label: "HDR", value: value})
    end if
    audio = formatName(s.audioCodec)
    if d <> invalid and isConverting(d.audio)
        parts = []
        if formatName(d.audio.to) <> "" then parts.push(formatName(d.audio.to))
        if d.audio.channels <> invalid and d.audio.channels > 0 then parts.push(countText(d.audio.channels, "channel", "channels"))
        into = parts.join(" · ")
        if into <> "" and into <> audio
            if audio = "" then audio = "Unknown"
            audio = audio + " → " + into
        end if
    end if
    if audio <> "" then rows.push({id: "audio", label: "Audio", value: audio})
    if s.container <> invalid and s.container <> "" then rows.push({id: "container", label: "Container", value: ucase(s.container)})
    if facts.bitrate <> invalid and facts.bitrate <> "" then rows.push({id: "bitrate", label: "Bitrate", value: facts.bitrate})
    return rows
end function

function isConverting(decision as dynamic) as boolean
    return decision <> invalid and (decision.action = "transcode" or decision.action = "burn")
end function


' How many a search group found: "20+" when the server stopped counting at 20.
function foundText(group as object) as string
    if group.total = invalid then return ""
    if group.totalIsMinimum = true then return group.total.toStr() + "+"
    return group.total.toStr()
end function

function kindCount(n as integer, kind as string) as string
    one = {movie: "movie", show: "show", collection: "collection", artist: "artist", album: "album", track: "song", audiobook: "audiobook"}
    if one[kind] = invalid then return countText(n, "title", "titles")
    return countText(n, one[kind], one[kind] + "s")
end function

' An episode in its season's row: {.., under: true (words under the still), corner (its length), played}.
function episodeCard(item as object, upNext as boolean) as object
    card = cardFor(item, "landscape")
    card.overlay = false
    card.under = true
    card.title = item.title
    card.sub = "Episode"
    if item.index <> invalid then card.sub = "Episode " + item.index.toStr()
    if card.sub = card.title then card.sub = ""       ' an episode named only by its number says it once
    card.corner = durationText(item.durationMs)
    card.played = (item.state <> invalid and item.state.watched = true)
    if upNext then card.badge = "Up next"
    return card
end function

' What a season says under its name: "7 of 13 watched", or "13 episodes" when none of it is.
function seasonLine(season as object) as object
    total = 0
    left = 0
    if season.counts <> invalid
        total = season.counts.leaves
        left = season.counts.unwatchedLeaves
    end if
    played = total - left
    if played <= 0 or total = 0 then return {text: countText(total, "episode", "episodes"), progress: -1}
    return {text: played.toStr() + " of " + total.toStr() + " watched", progress: played / total}
end function

' The chapter marks of the book's first version, as the other apps read them (client-core
' book-chapters): its chapter markers, or for a book in several files, its parts.
function bookChapters(book as object) as object
    out = []
    if book.versions = invalid or book.versions.count() = 0 then return out
    version = book.versions[0]
    if version.markers <> invalid
        n = 0
        for each mark in version.markers
            if mark.kind = "chapter" and mark.endMs > mark.startMs
                n = n + 1
                title = mark.title
                if title = invalid or title = "" then title = "Chapter " + n.toStr()
                out.push({title: title, startMs: mark.startMs, endMs: mark.endMs})
            end if
        end for
    end if
    if out.count() = 0 and version.parts <> invalid and version.parts.count() > 1
        for i = 0 to version.parts.count() - 1
            part = version.parts[i]
            out.push({title: "Part " + (i + 1).toStr(), startMs: part.offsetMs, endMs: part.offsetMs + part.durationMs})
        end for
    end if
    return out
end function
