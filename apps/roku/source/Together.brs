' Watch Together's rules, as the server (server-next/internal/api/groups.go) and the other apps
' (packages/client-core room-session.ts) have them. No nodes and no requests here, so
' tests/together.brs checks every one of them.

' An invitation code as typed: letters and digits only, in capitals, at most eight. A space or a
' dash in the middle is ignored, as the host may read it out in two halves.
function tidyCode(text as string) as string
    out = ""
    for i = 1 to len(text)
        ch = ucase(mid(text, i, 1))
        if len(out) < 8 and instr(1, "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789", ch) > 0 then out = out + ch
    end for
    return out
end function

' A code the way the host reads it out: two halves of four ("KQ7M 2XHE").
function codeShown(code as string) as string
    tidy = tidyCode(code)
    if len(tidy) <= 4 then return tidy
    return left(tidy, 4) + " " + mid(tidy, 5)
end function

' Join turns on once four characters are typed (tv-more frame 12).
function codeReady(code as string) as boolean
    return len(tidyCode(code)) >= 4
end function

' Seconds since 1970 of a time the server sends. Its times carry fractions of a second, which
' Roku's own reading of a date gives up on, so they are dropped first.
function whenSeconds(iso as dynamic) as integer
    if iso = invalid or iso = "" then return 0
    text = iso
    dot = instr(1, text, ".")
    if dot > 0 then text = left(text, dot - 1) + "Z"
    t = createObject("roDateTime")
    t.fromISO8601String(text)
    return t.asSeconds()
end function

' The entry the group is on now, or invalid when nothing is chosen.
function roomEntry(room as dynamic) as dynamic
    if room = invalid or room.clock = invalid or room.clock.entryId = invalid then return invalid
    for each entry in room.queue
        if entry.entryId = room.clock.entryId then return entry
    end for
    return invalid
end function

' The entry after this one in the group's queue, or invalid at its end.
function entryAfter(room as object, entryId as string) as dynamic
    found = false
    for each entry in room.queue
        if found then return entry
        if entry.entryId = entryId then found = true
    end for
    return invalid
end function

' Whether the group's clock is moving: playing, and not waiting for everyone to be ready.
function roomMoving(clock as object) as boolean
    return clock.playing = true and clock.waitUntil = invalid
end function

' Where the group is, in milliseconds: its place when the room was read, moved on by the time
' since (`sinceMs`) while it plays.
function groupPlaceMs(clock as object, sinceMs as float) as float
    place = clock.positionMs
    if roomMoving(clock) then place = place + sinceMs
    return place
end function

' What the player does to stay with the group: "seek" to the group's place, or "" to leave it.
' Within inStepMs it is left alone; at jumpMs or more it jumps. Between the two the other apps
' change speed a little; Roku's player cannot, so a drift that lasts 4 seconds (or any drift past
' inStepMs while the group is paused) is put right by a jump instead.
function keepInStep(moving as boolean, driftMs as float, inStepMs as integer, jumpMs as integer, offForMs as float) as string
    gap = abs(driftMs)
    if gap >= jumpMs then return "seek"
    if gap >= inStepMs and (not moving or offForMs >= 4000) then return "seek"
    return ""
end function

' What this member tells the group (PUT /v1/groups/{id}/me): ready is loaded and waiting at the
' group's place while it waits for everyone.
function reportState(loading as boolean, playerState as string, waiting as boolean) as string
    if loading or playerState = "buffering" then return "buffering"
    if waiting then return "ready"
    if playerState = "playing" then return "playing"
    return "paused"
end function

' The room's own transport button: its words and the action it sends, or invalid when this member
' may not control playback (the room says "The host controls playback" instead).
function transportFor(room as object) as dynamic
    if room.you.mayControl <> true or roomEntry(room) = invalid then return invalid
    if room.clock.waitUntil <> invalid then return {text: "Play anyway", action: "goAhead", icon: "play"}
    if room.clock.playing = true then return {text: "Pause for everyone", action: "pause", icon: "pause"}
    return {text: "Play for everyone", action: "play", icon: "play"}
end function

' The readiness line under what is on.
function readinessText(clock as object) as string
    if clock.waitingFor = invalid or clock.waitingFor <= 0 then return "Everyone is ready."
    total = clock.ready + clock.waitingFor
    people = "waiting for one person"
    if clock.waitingFor > 1 then people = "waiting for " + clock.waitingFor.toStr() + " people"
    return clock.ready.toStr() + " of " + total.toStr() + " ready   " + people
end function

' What the card says the group is doing, and whether that is a wait (amber) or not (green).
function roomStateText(room as object, reconnecting as boolean) as object
    if reconnecting then return {text: "Reconnecting…", wait: true}
    if room.host.present <> true and room.you.host <> true then return {text: "Host reconnecting", wait: true}
    if room.clock.waitUntil <> invalid then return {text: "Waiting for everyone", wait: true}
    if room.clock.playing = true then return {text: "Playing", wait: false}
    return {text: "Paused", wait: false}
end function

' A member's name as the others see it, with "(you)" on your own.
function memberName(member as object) as string
    name = "Private viewer"
    if member.private <> true
        if member.name <> invalid and member.name <> ""
            name = member.name
        else if member.username <> invalid
            name = member.username
        end if
    end if
    if member.you = true then return name + " (you)"
    return name
end function

' A member's state on the right of their row, and its tone: "good" (in step), "wait" or "away".
function memberState(status as dynamic) as object
    if status = "in_step" then return {text: "In step", tone: "good"}
    if status = "buffering" then return {text: "Buffering", tone: "wait"}
    if status = "catching_up" then return {text: "Catching up", tone: "wait"}
    return {text: "Away", tone: "away"}
end function

' The pill in the player: whether everyone is in step, or who the group is waiting for.
function pillText(room as object, reconnecting as boolean) as object
    if reconnecting then return {text: "Reconnecting…", wait: true}
    if room.host.present <> true and room.you.host <> true then return {text: "Host reconnecting", wait: true}
    if room.clock.waitingFor <> invalid and room.clock.waitingFor > 0
        for each member in room.members
            if member.you <> true and member.status <> "in_step" then return {text: "Waiting for " + memberName(member) + "…", wait: true}
        end for
    end if
    return {text: "In step", wait: false}
end function

' Whether the host may hand the group to this member: someone else who is in step (frame 14).
function mayMakeHost(room as object, member as object) as boolean
    return room.you.host = true and member.you <> true and member.status = "in_step"
end function

' Leaving as the host while others are here asks first (frame 16); anyone else leaves at once.
function leaveAsks(room as object) as boolean
    return room.you.host = true and room.members.count() > 1
end function

' The one sentence under "This group has ended" (frame 17).
function endedText(reason as dynamic) as string
    if reason = "ended_by_host" then return "The host ended the group."
    if reason = "no_host" then return "The host was away too long, so the group ended."
    if reason = "everyone_left" then return "Everyone left."
    return "The group is over."
end function

' The amber line while the host is away (frame 15): the time to the pause, then to the end.
' `pausesIn` and `endsIn` are seconds from now.
function hostAwayText(pausesIn as integer, endsIn as integer) as string
    if pausesIn > 0 then return "The host’s connection dropped. The group pauses in " + minutesText(pausesIn) + " unless they return."
    if endsIn < 0 then endsIn = 0
    return "The host’s connection dropped. The group ends in " + minutesText(endsIn) + " unless they return."
end function

function minutesText(seconds as integer) as string
    secs = seconds mod 60
    text = secs.toStr()
    if secs < 10 then text = "0" + text
    return int(seconds / 60).toStr() + ":" + text
end function

' The whole queue to send (PUT /v1/groups/{id}/queue) with a title added at its end: the entries
' already there by their entryId, the new one by its itemId.
function queueWith(room as object, itemId as string) as object
    entries = []
    for each entry in room.queue
        entries.push({entryId: entry.entryId})
    end for
    entries.push({itemId: itemId})
    return {entries: entries}
end function

' The whole queue to send with one entry taken out.
function queueWithout(room as object, entryId as string) as object
    entries = []
    for each entry in room.queue
        if entry.entryId <> entryId then entries.push({entryId: entry.entryId})
    end for
    return {entries: entries}
end function

' What a newer reading of the room changes for a player following it: the group ended, a different
' title is on, or it was paused or played.
function roomChange(before as dynamic, after as object) as object
    change = {ended: after.ended <> invalid, entry: false, playing: false}
    if before = invalid
        change.entry = true
        change.playing = true
        return change
    end if
    change.entry = (before.clock.entryId <> after.clock.entryId)
    change.playing = (roomMoving(before.clock) <> roomMoving(after.clock))
    return change
end function

' Where the group is in what is on: "31:40 of 53:06", or the place alone when the length is not known.
function placeText(placeMs as float, durationMs as dynamic) as string
    text = clockOf(int(placeMs / 1000))
    if durationMs = invalid or durationMs <= 0 then return text
    return text + " of " + clockOf(int(durationMs / 1000))
end function

function clockOf(seconds as integer) as string
    if seconds < 0 then seconds = 0
    h = int(seconds / 3600)
    rest = minutesText(seconds mod 3600)
    if h = 0 then return rest
    if len(rest) < 5 then rest = "0" + rest
    return h.toStr() + ":" + rest
end function
