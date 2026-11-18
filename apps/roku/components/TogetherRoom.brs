sub init()
    m.beat = m.top.findNode("beat")
    m.beat.observeField("fire", "onBeat")
    m.id = ""          ' the group's id while in one
    m.seq = 0          ' every request answered with the room is numbered as it is sent
    m.taken = 0        ' the newest answer taken: an older one arriving late is dropped, so a
                       ' slow report cannot put the clock back after a Pause
    m.sentKey = ""     ' the entry and state last reported
end sub

sub onAsk()
    ask = m.top.ask
    d = ask.do
    if d = "create"
        enter("POST", "/v1/groups", {name: ask.name, displayName: ask.displayName, control: ask.control}, d)
    else if d = "join"
        enter("POST", "/v1/groups/join", {code: tidyCode(ask.code), displayName: ask.displayName}, d)
    else if d = "open"
        enter("GET", "/v1/groups/" + ask.id, invalid, d)
    else if m.id = ""
        m.top.answer = {do: d, ok: false, message: "That didn’t reach the group. Try again."}
    else if d = "play" or d = "pause" or d = "goAhead"
        act("POST", "/playback", {action: d}, d)
    else if d = "seek"
        act("POST", "/playback", {action: "seek", positionMs: int(ask.positionMs)}, d)
    else if d = "watchEntry"
        act("POST", "/playback", {action: "watch", entryId: ask.entryId}, d)
    else if d = "remove"
        act("PUT", "/queue", queueWithout(m.top.room, ask.entryId), d)
    else if d = "newCode"
        act("PATCH", "", {newCode: {}}, d)
    else if d = "makeHost"
        act("PATCH", "", {host: ask.memberId}, d)
    else if d = "becomeHost"
        act("PATCH", "", {becomeHost: true}, d)
    else if d = "leave"
        api("DELETE", "/v1/groups/" + m.id + "/me", invalid, "onLeft", {id: m.id})
    else if d = "end"
        api("DELETE", "/v1/groups/" + m.id, invalid, "onEndedByMe", {id: m.id})
    else if d = "forget"
        stopHere("")
    end if
end sub

' Starting, joining or opening a group: this TV is then where the member watches from.
sub enter(method as string, path as string, body as dynamic, d as string)
    stopHere("")
    m.seq = m.seq + 1
    api(method, path, body, "onEntered", {seq: m.seq, sent: UpTime(0), do: d})
end sub

sub onEntered(event as object)
    result = event.getData()
    ctx = event.getRoSGNode().ctx
    if ctx.seq <> m.seq then return      ' another group was asked for since
    if not result.ok
        m.top.answer = {do: ctx.do, ok: false, code: result.error.code, message: result.error.message}
        return
    end if
    m.id = result.data.id
    take(result.data, ctx)
    m.top.answer = {do: ctx.do, ok: true}
    if m.top.phase <> "live" then return
    ' opening a membership held on another device moves it here
    m.sentKey = ""
    m.top.report = {state: firstState(), positionMs: 0}
    send(true)
end sub

' What a member reports before any player has said anything: waiting for a title to load, or ready.
function firstState() as string
    if roomEntry(m.top.room) <> invalid then return "buffering"
    return "ready"
end function

sub act(method as string, suffix as string, body as dynamic, d as string)
    m.seq = m.seq + 1
    ctx = {seq: m.seq, sent: UpTime(0), do: d, id: m.id}
    api(method, "/v1/groups/" + m.id + suffix, body, "onActed", ctx)
end sub

sub onActed(event as object)
    result = event.getData()
    ctx = event.getRoSGNode().ctx
    if ctx.id <> m.id then return
    if not result.ok
        failed(result)
        m.top.answer = {do: ctx.do, ok: false, code: result.error.code, message: result.error.message}
        return
    end if
    take(result.data, ctx)
    m.top.answer = {do: ctx.do, ok: true}
end sub

' Every answer that carries the room: the newest one sent wins.
sub take(room as object, ctx as object)
    if ctx.seq < m.taken then return
    m.taken = ctx.seq
    ' the server read its clock about halfway through the trip
    room.receivedAt = (ctx.sent + UpTime(0)) / 2
    m.top.room = room
    if room.ended <> invalid
        m.beat.control = "stop"
        m.top.phase = "ended"
        m.id = ""
        return
    end if
    m.top.phase = "live"
    every = room.reportEveryMs / 1000
    if every < 1 then every = 1
    if m.beat.duration <> every or m.beat.control <> "start"
        m.beat.duration = every
        m.beat.control = "start"
    end if
end sub

' A request that did not land. Signed out, Watch Together turned off or the group moved to another
' device: this TV is out of it. Gone from the server: it has ended. Anything else is a moment out
' of reach, and the next report tries again.
sub failed(result as object)
    code = result.error.code
    if result.status = 401 or code = "watch_together_off" or code = "in_group_elsewhere"
        stopHere("")
    else if result.status = 404 or result.status = 410
        ' ended: the server keeps an ended group readable for two minutes, with why it ended
        if m.ending = true then return
        m.ending = true
        m.beat.control = "stop"
        api("GET", "/v1/groups/" + m.id, invalid, "onEndedRead", {id: m.id})
    else if result.status = 0 or result.status >= 500
        m.top.phase = "reconnecting"
    end if
end sub

sub onEndedRead(event as object)
    result = event.getData()
    if event.getRoSGNode().ctx.id <> m.id then return
    room = m.top.room
    if result.ok and result.data.ended <> invalid then room = result.data
    if room.ended = invalid then room.ended = {reason: ""}
    m.top.room = room
    stopHere("ended")
end sub

sub stopHere(phase as string)
    m.ending = false
    m.beat.control = "stop"
    m.id = ""
    m.sentKey = ""
    if phase = "" then m.top.room = {}
    m.top.phase = phase
end sub

' The steady report, on the server's cadence.
sub onBeat()
    send(false)
end sub

' The player's state changed: a new state or a new title is reported at once.
sub onReport()
    if m.id = "" then return
    entry = ""
    if m.top.room.clock.entryId <> invalid then entry = m.top.room.clock.entryId
    if entry + "/" + m.top.report.state <> m.sentKey then send(false)
end sub

sub send(here as boolean)
    if m.id = "" then return
    report = m.top.report
    body = {state: report.state, positionMs: int(report.positionMs)}
    entry = ""
    if m.top.room.clock.entryId <> invalid
        entry = m.top.room.clock.entryId
        body.entryId = entry
    end if
    if here then body.here = true
    m.sentKey = entry + "/" + report.state
    m.seq = m.seq + 1
    api("PUT", "/v1/groups/" + m.id + "/me", body, "onReported", {seq: m.seq, sent: UpTime(0), do: "report", id: m.id})
end sub

sub onReported(event as object)
    result = event.getData()
    ctx = event.getRoSGNode().ctx
    if ctx.id <> m.id then return
    if result.ok then take(result.data, ctx) else failed(result)
end sub

sub onLeft(event as object)
    result = event.getData()
    if event.getRoSGNode().ctx.id <> m.id then return
    if result.ok then stopHere("")
    message = ""
    if not result.ok then message = "That didn’t reach the group. Try again."
    m.top.answer = {do: "leave", ok: result.ok, message: message}
end sub

' Ended by this host: the room says so at once, without waiting for the server's next word.
sub onEndedByMe(event as object)
    result = event.getData()
    if event.getRoSGNode().ctx.id <> m.id then return
    if result.ok
        room = m.top.room
        room.ended = {reason: "ended_by_host"}
        m.top.room = room
        stopHere("ended")
    end if
    message = ""
    if not result.ok then message = "The group couldn’t be ended."
    m.top.answer = {do: "end", ok: result.ok, message: message}
end sub
