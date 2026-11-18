' Checks of Live TV's pure rules (source/Live.brs) against what the other Portico apps do. Run
' with tools/test.sh (the brs interpreter); nothing here runs on a Roku.

sub main()
    m.failed = 0
    m.passed = 0

    ' Live TV's guide: the grid starts on the half hour before now (a day on for tomorrow), as the
    ' server's own default window does; a programme is placed at 8 px a minute from gridX, less the
    ' 10 px gap, and one that ended, starts past the screen or is a sliver is not drawn
    g = {gridX: 520, perMinute: 8}
    same("guide start", [guideStart(1800 * 1000 + 299, 0).toStr(), guideStart(1800 * 1000 + 299, 1).toStr()], [(1800 * 1000).toStr(), (1800 * 1000 + 86400).toStr()])
    span = guideSpan(-600, 3600, 0, g)
    same("guide span earlier", [span.x.toStr(), span.width.toStr(), span.earlier.toStr()], ["520", "470", "true"])
    span = guideSpan(1800, 2700, 0, g)
    same("guide span later", [span.x.toStr(), span.width.toStr(), span.earlier.toStr()], ["760", "110", "false"])
    same("guide span off", [(guideSpan(-600, 0, 0, g) = invalid).toStr(), (guideSpan(3 * 3600, 4 * 3600, 0, g) = invalid).toStr(), (guideSpan(0, 120, 0, g) = invalid).toStr()], ["true", "true", "true"])
    ' the toolbar offers the channel source only when there is a choice to make
    same("live toolbar", [liveToolbar(1, false).join(","), liveToolbar(2, true).join(",")], ["day,now", "source,day,now,recordings"])
    ' the groups: each once, counted across the sources shown, the biggest first then by name; past
    ' the first few, behind More groups (never a More groups of one)
    sources = [{id: "a", groups: [{name: "News", count: 2}, {name: "Kids", count: 1}, {name: "Sports", count: 3}]}, {id: "b", groups: [{name: "Kids", count: 4}, {name: "Music", count: 1}]}]
    groups = liveGroups(sources, "", 8)
    same("live groups", [groups.shown.join(","), groups.more.count().toStr()], ["Kids,Sports,News,Music", "0"])
    same("live groups of one source", [liveGroups(sources, "a", 8).shown.join(",")], ["Sports,News,Kids"])
    groups = liveGroups(sources, "", 2)
    same("live groups capped", [groups.shown.join(","), groups.more.join(",")], ["Kids,Sports", "News,Music"])
    same("live groups one over", [liveGroups(sources, "", 3).shown.join(",")], ["Kids,Sports,News,Music"])
    ' the red dot: a booking still to record or recording now, found by channel and start; a recording
    ' whose channel is gone marks nothing
    booked = liveBookings([{channelId: "c1", starts: 100, state: "scheduled"}, {channelId: "c1", starts: 200, state: "recorded"}, {channelId: "c2", starts: 100, state: "recording"}, {channelId: "", starts: 300, state: "scheduled"}])
    same("live bookings", [(booked[bookingKey("c1", 100)] <> invalid).toStr(), (booked[bookingKey("c1", 200)] <> invalid).toStr(), (booked[bookingKey("c2", 100)] <> invalid).toStr(), booked.count().toStr()], ["true", "false", "true", "2"])
    same("recording playable", [recordingPlayable({itemId: "i", state: "recorded"}).toStr(), recordingPlayable({itemId: "i", state: "scheduled"}).toStr(), recordingPlayable({state: "recorded"}).toStr()], ["true", "false", "false"])
    same("recording state", [recordingState({state: "scheduled"}), recordingState({state: "scheduled", conflict: true}), recordingState({state: "recording"}), recordingState({state: "recorded"})], ["", "Tuner conflict", "Recording now", ""])
    same("day word", [dayWord(0, "November 18"), dayWord(1, "November 19"), dayWord(-1, "November 17"), dayWord(5, "November 23")], ["Today", "Tomorrow", "Yesterday", "November 23"])
    same("size", [recordingSizeText(500), recordingSizeText(640000000), recordingSizeText(1200000000), recordingSizeText(3000000000)], ["under 1 MB", "640 MB", "1.2 GB", "3 GB"])

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
