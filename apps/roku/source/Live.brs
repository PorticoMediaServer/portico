' Live TV's pure rules: the guide's time grid, and which toolbar entries and filter chips show.
' tests/rules.brs checks them against what the other apps do.

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
' one to choose from, the day, and Now.
function liveToolbar(sourceCount as integer) as object
    out = []
    if sourceCount > 1 then out.push("source")
    out.push("day")
    out.push("now")
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
