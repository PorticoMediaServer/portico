' Live TV's rules: the guide's time grid, which toolbar entries and filter chips show, what a
' programme's red dot means, how a recording is described, and the programme's side sheet the
' guide and the live player both open. tests/rules.brs checks the pure ones.

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

' What the guide says when it has no channels: "" when it has some, "filter" when a source, group
' or Favorites leaves none (its row offers every channel again), "none" when there are none at all.
function guideEmpty(count as integer, filter as object) as string
    if count > 0 then return ""
    if filter.source <> "" or filter.group <> "" or filter.favorites then return "filter"
    return "none"
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

' "Today", "Tomorrow", "Yesterday", or "November 17", by the TV's own day.
function dayText(seconds as integer) as string
    t = createObject("roDateTime")
    t.fromSeconds(seconds)
    t.toLocalTime()
    now = createObject("roDateTime")
    now.toLocalTime()
    months = ["January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"]
    return dayWord(int(t.asSeconds() / 86400) - int(now.asSeconds() / 86400), months[t.getMonth() - 1] + " " + t.getDayOfMonth().toStr())
end function

' A guide time in seconds.
function secondsOf(iso as string) as integer
    t = createObject("roDateTime")
    t.fromISO8601String(iso)
    return t.asSeconds()
end function

' "S1 E3   Homecoming", or what the guide has of it.
function episodeLine(p as object) as string
    code = ""
    if p.season <> invalid and p.episode <> invalid then code = "S" + p.season.toStr() + " E" + p.episode.toStr()
    if p.episodeTitle <> invalid and p.episodeTitle <> "" then code = (code + "   " + p.episodeTitle).trim()
    return code
end function

' A programme's side sheet without its rows (tv.html frame 6): the channel's badge, name and number
' with Live while it is on, the title, its episode, times, time left and rating, how far in, the
' description, and Later on with the next two programmes of `row` (the channel's guide row).
function programmeSheet(row as object, p as dynamic, now as integer) as object
    model = {mark: row.channel, eyebrow: row.channel.name + "   " + row.channel.number, title: row.channel.name, status: "", live: false, rows: []}
    if p = invalid then return model
    s = secondsOf(p.startsAt)
    e = secondsOf(p.endsAt)
    model.live = (s <= now and now < e)
    if p.title <> invalid then model.title = p.title
    status = guideTime(s, false) + " to " + guideTime(e, true)
    minsLeft = int((e - now) / 60)
    if model.live and minsLeft > 0 then status = status + "   " + durationText(minsLeft * 60000) + " left"
    if p.rating <> invalid and p.rating <> "" then status = status + "   " + p.rating
    episode = episodeLine(p)
    if episode <> "" then status = episode + chr(10) + status
    model.status = status
    if model.live then model.progress = (now - s) / (e - s)
    if p.summary <> invalid then model.body = p.summary
    lines = []
    for each other in row.programmes
        starts = secondsOf(other.startsAt)
        if starts >= e and lines.count() < 2 and other.title <> invalid then lines.push({time: guideTime(starts, false), title: other.title})
    end for
    model.later = {heading: "Later on " + row.channel.name, lines: lines}
    return model
end function

' Record series (Record series…): which channels a rule for this programme follows, with how many
' airings it would record now (`rule`, the server's preview). A guide that names no series matches
' by title on this channel only, so there is nothing to choose there.
function seriesSheet(rule as object, channelName as string, title as string) as object
    status = ""
    if rule.preview <> invalid then status = countText(rule.preview.matches, "airing in the guide now", "airings in the guide now")
    rows = [{id: "rule:this", text: "On " + channelName, icon: "repeat"}]
    if rule.byTitle <> true then rows.push({id: "rule:any", text: "On any channel", icon: "repeat"})
    return {eyebrow: "Record series", title: title, status: status, rows: rows}
end function

' The question before a booking goes (the guide's sheet and the live player's Recording ask the same):
' one still to come is cancelled, one recording now is stopped.
function recordingConfirm(title as string, booking as object) as object
    if booking.state = "recording" then return {title: "Stop recording " + title + "?", body: "What has been recorded so far is kept.", verb: "Stop recording"}
    return {title: "Cancel recording " + title + "?", body: "It won’t be recorded. A series rule, if there is one, stays in place.", verb: "Cancel recording"}
end function

' The route that cancels a booking, or stops one recording now.
function recordingCancelPath(booking as object) as string
    path = "/v1/dvr/recordings/" + booking.id
    if booking.state = "recording" then path = path + "?action=stop"
    return path
end function

' A channel's Favorites row in a programme's sheet.
function favoriteRow(channel as object) as object
    if channel.favorite = true then return {id: "favorite", text: "Remove channel from Favorites", icon: "heartFilled"}
    return {id: "favorite", text: "Add channel to Favorites", icon: "heart"}
end function

' What the server said when it would not record.
function notRecordedSheet(message as string) as object
    return {title: "This program wasn’t recorded", body: message, rows: [{id: "ok", text: "OK"}]}
end function

' A reminder shows two minutes before its programme starts on every open app, or when an app opens
' later while the programme is still on, and never once it has ended (definition 11, rule 62). From
' the profile's reminders ([{programme, title, channel, starts, ends}]) and those already shown:
' the one to show now, and how many seconds until the next comes due (-1 for none).
function reminderDue(reminders as object, now as integer, shown as object) as object
    out = {due: invalid, wait: -1}
    for each r in reminders
        if shown[r.programme] = invalid and now < r.ends
            if now >= r.starts - 120
                if out.due = invalid then out.due = r
            else if out.wait < 0 or r.starts - 120 - now < out.wait
                out.wait = r.starts - 120 - now
            end if
        end if
    end for
    return out
end function

' The note's words: the catalogue's guide.reminder before the start; once it has started (an app
' opened late) the catalogue has none, so it says it is on now.
function reminderWords(r as object, now as integer) as string
    if now < r.starts then return r.title + " starts in 2 minutes on " + r.channel
    return r.title + " is on now on " + r.channel
end function
