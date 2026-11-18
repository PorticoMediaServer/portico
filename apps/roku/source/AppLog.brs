' The app's own log (definition area 12, rules 70 and 71; the app-log rules of 12 Nov): a ring of
' recent lines kept in memory, sent only with a report the viewer chooses to send, its box off
' every time. A line is time, level, area and a short message: the app and system versions, the
' screens opened, playback facts by id, and network failures as status, code and request id.
' Everything is cleaned as it is written, so nothing below can ever be in the ring:
'   passwords, PINs, two-step, recovery, setup, pairing, move and invitation codes, join links,
'   Portico sign-in notes, API keys, tokens, cookies, Authorization headers, addresses (a media
'   request is its route and ids, never its address, and a path's grant is removed), the server's
'   address or IPs, file paths on the device, anything typed, and other people's names.
' The Roku keeps no copy of the last run: it has no private storage that lasts and is its own
' (the registry holds 16 KB in all, and cachefs: is the system's to clear).

' The words of a line with every kind of private thing replaced by what it was.
function appLogClean(text as string) as string
    out = text
    ' an address of any kind: a link (a join link too), a file or device path, a host, an IP
    out = createObject("roRegex", "[a-z][a-z0-9+.-]*://\S*", "i").replaceAll(out, "[address]")
    out = createObject("roRegex", "\b(pkg|tmp|cachefs|common|ext\d+):/\S*", "i").replaceAll(out, "[path]")
    out = createObject("roRegex", "\b\d{1,3}(\.\d{1,3}){3}(:\d+)?\b", "").replaceAll(out, "[address]")
    out = createObject("roRegex", "\b[0-9a-f]{0,4}(:[0-9a-f]{0,4}){3,7}\b", "i").replaceAll(out, "[address]")
    out = createObject("roRegex", "\b[a-z0-9-]+(\.[a-z0-9-]+)*\.[a-z]{2,}(:\d+)?\b", "i").replaceAll(out, "[address]")
    ' a media path's grant, and whatever a path asks (a search's words are typed)
    out = createObject("roRegex", "/v1/media/[^/\s]+", "i").replaceAll(out, "/v1/media/[grant]")
    out = createObject("roRegex", "\?\S*", "").replaceAll(out, "")
    ' anything that names a secret, with the word after it; a note's words, to the end of the line
    for each word in ["password", "passcode", "pin", "code", "secret", "token", "bearer", "authorization", "cookie", "key"]
        out = createObject("roRegex", "\b" + word + "\b\s*[:=]?\s*\S+", "i").replaceAll(out, word + " [hidden]")
    end for
    out = createObject("roRegex", "\bnote\b.*", "i").replaceAll(out, "note [hidden]")
    ' codes as they are read out (ABCD-1234) and long runs of digits
    out = createObject("roRegex", "\b[A-Z0-9]{3,8}(-[A-Z0-9]{3,8})+\b", "").replaceAll(out, "[code]")
    out = createObject("roRegex", "\b\d{6,}\b", "").replaceAll(out, "[code]")
    ' long opaque words (a token or key) that are not Portico ids (itm_..., lst_..., prf_...)
    out = createObject("roRegex", "\b(?![a-z]{2,5}_)[A-Za-z0-9_+=.-]{24,}", "").replaceAll(out, "[hidden]")
    return out
end function

function appLogLine(at as string, level as string, area as string, message as string) as string
    return at + " " + level + " " + area + " " + appLogClean(message)
end function

' The ring with a line added at its end, its oldest lines dropped to keep it within `most` characters.
function appLogKeep(lines as object, line as string, most as integer) as object
    out = []
    out.append(lines)
    out.push(line)
    total = 0
    for each kept in out
        total = total + len(kept) + 1
    end for
    while total > most and out.count() > 1
        total = total - len(out[0]) - 1
        out.shift()
    end while
    return out
end function

' Adds a line from anywhere in the app (the ring is m.global.appLog, about 256 KB).
sub logApp(level as string, area as string, message as string)
    if m.global = invalid or not m.global.hasField("appLog") then return
    now = createObject("roDateTime")
    m.global.appLog = appLogKeep(m.global.appLog, appLogLine(now.toISOString(), level, area, message), 262144)
end sub
