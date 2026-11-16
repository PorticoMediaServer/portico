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

' What the hero shows for an item: {eyebrow, title, year, facts, tag, progress, left, synopsis, art}.
function heroFor(item as object) as object
    h = {eyebrow: "", title: item.title, year: "", facts: tagNames(item, "genre", 2), tag: "", progress: progressOf(item), left: timeLeftText(item), synopsis: "", art: pick(item.images, ["backdrop", "still"])}
    if item.summary <> invalid then h.synopsis = item.summary
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
    hash = &h811C9DC5&
    mask = &hFFFFFFFF&
    for i = 1 to len(title)
        code = asc(mid(title, i, 1))
        units = [code]
        if code > 65535
            code = code - 65536
            units = [55296 + int(code / 1024), 56320 + (code mod 1024)]
        end if
        for each unit in units
            u& = unit
            x& = (hash or u&) - (hash and u&)   ' exclusive or
            hash = (x& * 16777619&) and mask
        end for
    end for
    return hash mod 8
end function
