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
    same("live toolbar", [liveToolbar(1).join(","), liveToolbar(2).join(",")], ["day,now", "source,day,now"])
    ' the groups: each once, counted across the sources shown, the biggest first then by name; past
    ' the first few, behind More groups (never a More groups of one)
    sources = [{id: "a", groups: [{name: "News", count: 2}, {name: "Kids", count: 1}, {name: "Sports", count: 3}]}, {id: "b", groups: [{name: "Kids", count: 4}, {name: "Music", count: 1}]}]
    groups = liveGroups(sources, "", 8)
    same("live groups", [groups.shown.join(","), groups.more.count().toStr()], ["Kids,Sports,News,Music", "0"])
    same("live groups of one source", [liveGroups(sources, "a", 8).shown.join(",")], ["Sports,News,Kids"])
    groups = liveGroups(sources, "", 2)
    same("live groups capped", [groups.shown.join(","), groups.more.join(",")], ["Kids,Sports", "News,Music"])
    same("live groups one over", [liveGroups(sources, "", 3).shown.join(",")], ["Kids,Sports,News,Music"])

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
