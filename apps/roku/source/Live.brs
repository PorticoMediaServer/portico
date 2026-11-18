' Live TV's pure rules: the guide's time grid, which toolbar entries and filter chips show, what a
' programme's red dot means, and how a recording is described. tests/rules.brs checks them.

' The guide's first half hour: the half hour before now, today; the same half hour a day on, tomorrow.
function guideStart(now as integer, day as integer) as integer
    return now - (now mod 1800) + day * 86400
end function

' Where a programme from `s` to `e` (seconds) sits on a grid whose first half hour is `from`, on
' the guide frame `g`: its left edge and width in px, and whether it began before the grid.
' Invalid when nothing of it shows: it ended before the grid, starts past the screen, or is a sliver.
function guideSpan(s as integer, e as integer, from as integer, g as object) as dynamic
    if e <= from then return invalid
    shown = s
    if shown < from then shown = from
    x = g.gridX + (shown - from) / 60 * g.perMinute
    width = (e - shown) / 60 * g.perMinute - 10
    if x >= 1920 or width <= 20 then return invalid
    return {x: x, width: width, earlier: s < from}
end function

' The toolbar beside the page title, left to right: the channel source when there is more than
' one to choose from, the day, Now, and Recordings for a profile that may record or has recordings.
function liveToolbar(sourceCount as integer, recordings as boolean) as object
    out = []
    if sourceCount > 1 then out.push("source")
    out.push("day")
    out.push("now")
    if recordings then out.push("recordings")
    return out
end function

' The channel groups to offer as chips, from the sources the guide shows (one chosen, or all):
' each group once with its channels counted across them, the biggest first (then by name). At
' most `most` are chips; the rest wait behind a "More groups" chip, as an IPTV source can have hundreds.
function liveGroups(sources as object, sourceId as string, most as integer) as object
    counts = {}
    names = []
    for each source in sources
        if sourceId = "" or source.id = sourceId
            for each group in source.groups
                if counts[group.name] = invalid
                    counts[group.name] = 0
                    names.push(group.name)
                end if
                counts[group.name] = counts[group.name] + group.count
            end for
        end if
    end for
    ' a plain insertion sort: a source names tens of groups, rarely hundreds
    sorted = []
    for each name in names
        at = sorted.count()
        for i = 0 to sorted.count() - 1
            other = sorted[i]
            if counts[name] > counts[other] or (counts[name] = counts[other] and lcase(name) < lcase(other))
                at = i
                exit for
            end if
        end for
        sorted.push("")
        for i = sorted.count() - 1 to at + 1 step -1
            sorted[i] = sorted[i - 1]
        end for
        sorted[at] = name
    end for
    shown = []
    more = []
    for each name in sorted
        if shown.count() < most or sorted.count() <= most + 1 then shown.push(name) else more.push(name)
    end for
    return {shown: shown, more: more}
end function

' The key a booking is found by: its channel and the second its programme starts.
function bookingKey(channelId as string, starts as integer) as string
    return channelId + "|" + starts.toStr()
end function

' The bookings a programme's red dot and its sheet read, by bookingKey, from the profile's
' recordings ([{channelId, starts, id, state, mayCancel, mayChange}]): only one still to record or
' recording now. One whose channel is gone has nothing in the guide to mark.
function liveBookings(recordings as object) as object
    out = {}
    for each r in recordings
        if r.channelId <> "" and (r.state = "scheduled" or r.state = "recording") then out[bookingKey(r.channelId, r.starts)] = r
    end for
    return out
end function

' A recording can be played once some of it has been recorded into a title.
function recordingPlayable(r as object) as boolean
    if r.itemId = invalid or r.itemId = "" then return false
    return r.state = "recorded" or r.state = "partial" or r.state = "recording"
end function

' What the Recordings page says about a recording beside its title: only what is out of the
' ordinary (a booking waiting to record says nothing).
function recordingState(r as object) as string
    if r.state = "scheduled" and r.conflict = true then return "Tuner conflict"
    words = {recording: "Recording now", partial: "Partly recorded", failed: "Failed", skipped: "Skipped", cancelled: "Cancelled"}
    if words[r.state] <> invalid then return words[r.state]
    return ""
end function

' A recording's size on disk as people read it: "640 MB", "1.2 GB" (named for recordings: Present.brs gets a file's own sizeText).
function recordingSizeText(bytes as dynamic) as string
    if bytes = invalid or bytes < 1000000 then return "under 1 MB"
    if bytes < 1000000000 then return int(bytes / 1000000).toStr() + " MB"
    tenths = int(bytes / 100000000 + 0.5)
    text = int(tenths / 10).toStr()
    if tenths mod 10 <> 0 then text = text + "." + (tenths mod 10).toStr()
    return text + " GB"
end function

' A day as the Recordings page names it, `days` from today: Today, Tomorrow, Yesterday, else its date.
function dayWord(days as integer, date as string) as string
    if days = 0 then return "Today"
    if days = 1 then return "Tomorrow"
    if days = -1 then return "Yesterday"
    return date
end function
