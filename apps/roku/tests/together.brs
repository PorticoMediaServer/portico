' Checks of Watch Together's pure rules (source/Together.brs). Run with tools/test.sh; no server is
' called. The rooms are shaped as GET /v1/groups/{id} answers (server-next/internal/api/groups.go).

sub main()
    m.failed = 0
    m.passed = 0

    ' a code as typed: capitals, letters and digits, a space or dash ignored, eight at most
    same("tidy", [tidyCode("kq7m 2xhe"), tidyCode("KQ7M-2XHE"), tidyCode("kq7m2xhe99"), tidyCode("é!")], ["KQ7M2XHE", "KQ7M2XHE", "KQ7M2XHE", ""])
    same("shown", [codeShown("kq7m2xhe"), codeShown("kq7"), codeShown("KQ7M2")], ["KQ7M 2XHE", "KQ7", "KQ7M 2"])
    same("ready", [yes(codeReady("kq7")), yes(codeReady("kq 7m"))], ["no", "yes"])
    same("when", [whenSeconds("2026-11-18T12:00:05.123456789Z").toStr(), whenSeconds("2026-11-18T12:00:05Z").toStr(), whenSeconds(invalid).toStr()], ["1795003205", "1795003205", "0"])

    playing = room(true, invalid, 2, 1)
    paused = room(false, invalid, 3, 0)
    waiting = room(true, "2026-11-18T12:00:10Z", 2, 1)

    ' where the group is: moving on while it plays, still while paused or waiting for everyone
    same("place", [whole(groupPlaceMs(playing.clock, 1500)), whole(groupPlaceMs(paused.clock, 1500)), whole(groupPlaceMs(waiting.clock, 1500))], ["61500", "60000", "60000"])

    ' keeping in step without a change of speed (inStep 750, jump 3000)
    same("step", [keepInStep(true, 500, 750, 3000, 9000), keepInStep(true, 1200, 750, 3000, 1000), keepInStep(true, -1200, 750, 3000, 4000), keepInStep(true, 3000, 750, 3000, 0), keepInStep(false, 800, 750, 3000, 0)], ["", "", "seek", "seek", "seek"])

    same("report", [reportState(true, "playing", false), reportState(false, "buffering", false), reportState(false, "paused", true), reportState(false, "playing", false), reportState(false, "paused", false)], ["buffering", "buffering", "ready", "playing", "paused"])

    ' who may control: the room's own button, or none for a member when only the host controls
    same("transport", [transportFor(playing).action, transportFor(paused).action, transportFor(waiting).action, transportFor(waiting).text], ["pause", "play", "goAhead", "Play anyway"])
    member = room(true, invalid, 2, 1)
    member.you = {host: false, mayControl: false, memberId: "m2"}
    same("member transport", [yes(transportFor(member) = invalid)], ["yes"])
    empty = room(true, invalid, 1, 0)
    empty.clock.entryId = invalid
    same("nothing on", [yes(transportFor(empty) = invalid), yes(roomEntry(empty) = invalid)], ["yes", "yes"])

    same("readiness", [readinessText(playing.clock), readinessText(paused.clock), readinessText({ready: 1, waitingFor: 3})], ["2 of 3 ready   waiting for one person", "Everyone is ready.", "1 of 4 ready   waiting for 3 people"])

    away = room(true, invalid, 3, 0)
    away.host = {present: false}
    away.you = {host: false, mayControl: false, memberId: "m2"}
    same("state", [roomStateText(playing, false).text, roomStateText(paused, false).text, roomStateText(waiting, false).text, roomStateText(away, false).text, roomStateText(playing, true).text], ["Playing", "Paused", "Waiting for everyone", "Host reconnecting", "Reconnecting…"])
    same("pill", [pillText(playing, false).text, pillText(paused, false).text, pillText(away, false).text, pillText(playing, true).text], ["Waiting for Sam…", "In step", "Host reconnecting", "Reconnecting…"])

    same("names", [memberName({name: "Demo", you: true}), memberName({username: "alex"}), memberName({private: true, name: "x"})], ["Demo (you)", "alex", "Private viewer"])
    same("member states", [memberState("in_step").text, memberState("catching_up").tone, memberState("away").tone, memberState("buffering").text], ["In step", "wait", "away", "Buffering"])

    ' the host hands over only to someone else who is in step
    same("make host", [yes(mayMakeHost(playing, playing.members[1])), yes(mayMakeHost(playing, playing.members[0])), yes(mayMakeHost(playing, playing.members[2])), yes(mayMakeHost(member, member.members[1]))], ["yes", "no", "no", "no"])
    alone = room(false, invalid, 1, 0)
    alone.members = [alone.members[0]]
    same("leave asks", [yes(leaveAsks(playing)), yes(leaveAsks(alone)), yes(leaveAsks(member))], ["yes", "no", "no"])

    same("ended", [endedText("ended_by_host"), endedText("no_host"), endedText("everyone_left"), endedText("restart")], ["The host ended the group.", "The host was away too long, so the group ended.", "Everyone left.", "The group is over."])
    same("host away", [hostAwayText(102, 600), hostAwayText(0, 492), hostAwayText(-5, -1)], ["The host’s connection dropped. The group pauses in 1:42 unless they return.", "The host’s connection dropped. The group ends in 8:12 unless they return.", "The host’s connection dropped. The group ends in 0:00 unless they return."])

    ' the queue is sent whole: the entries left, by their entryId
    same("queue remove", [formatJson(queueWithout(playing, "e1"))], ["{""entries"":[{""entryId"":""e2""}]}"])
    same("after", [entryAfter(playing, "e1").entryId, yes(entryAfter(playing, "e2") = invalid), roomEntry(playing).itemId], ["e2", "yes", "i1"])

    ' what a newer reading changes for a player following the group
    moved = room(false, invalid, 3, 0)
    moved.clock.entryId = "e2"
    finished = room(true, invalid, 3, 0)
    finished.ended = {reason: "ended_by_host"}
    same("change", [changeText(roomChange(invalid, playing)), changeText(roomChange(playing, playing)), changeText(roomChange(playing, paused)), changeText(roomChange(playing, moved)), changeText(roomChange(playing, waiting)), changeText(roomChange(playing, finished))], ["entry playing", "", "playing", "entry playing", "playing", "ended"])

    same("place text", [placeText(1900400, 3186000), placeText(5000, invalid), placeText(3725000, 7200000)], ["31:40 of 53:06", "0:05", "1:02:05 of 2:00:00"])

    print "PASSED " + m.passed.toStr() + " FAILED " + m.failed.toStr()
end sub

' A room as a member reads it: three people (you the host, Alex in step, Sam catching up), two
' titles queued, the first one on at one minute.
function room(playing as boolean, waitUntil as dynamic, ready as integer, waitingFor as integer) as object
    clock = {entryId: "e1", positionMs: 60000, at: "2026-11-18T12:00:00.5Z", playing: playing, ready: ready, waitingFor: waitingFor}
    if waitUntil <> invalid then clock.waitUntil = waitUntil
    return {id: "g1", name: "Movie night", control: "host", clock: clock, host: {present: true},
        you: {memberId: "m1", host: true, mayControl: true, mayBecomeHost: false},
        members: [{id: "m1", name: "Demo", host: true, you: true, status: "in_step"}, {id: "m2", name: "Alex", host: false, you: false, status: "in_step"}, {id: "m3", name: "Sam", host: false, you: false, status: "catching_up"}],
        queue: [{entryId: "e1", itemId: "i1", title: "The Last of Us", kind: "episode", available: true}, {entryId: "e2", itemId: "i2", title: "Inside Out", kind: "movie", available: true}],
        inStepMs: 750, jumpMs: 3000, reportEveryMs: 5000}
end function

function changeText(change as object) as string
    words = []
    if change.ended then words.push("ended")
    if change.entry then words.push("entry")
    if change.playing then words.push("playing")
    return words.join(" ")
end function

function yes(value as boolean) as string
    if value then return "yes"
    return "no"
end function

function whole(value as float) as string
    return int(value).toStr()
end function

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
        print got
    end if
end sub
