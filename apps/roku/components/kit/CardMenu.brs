' The one entry menu for a title (tv-focus 04, tv-more frame 2, tv-detail frame 7): * on any card,
' and the "..." button on a title's page, give the same menu for the same title everywhere.
'
' A screen that uses it includes this file, sets m.top.menu = cardMenu(item, extras, skip), passes
' every menuChosen to cardMenuChosen first (it returns false for the screen's own rows), and defines
' `sub cardMenuChanged(item as object)`, called once the server has taken a change to the title's
' state (item.state is then the server's). `extras` are rows only that screen has ({id, text, icon},
' `last: true` to go at the foot, such as See all); `skip` names rows the screen leaves out ("details"
' on a film's own page, "show" on the show's). The menu's own rows have ids starting "card:", so they never meet a
' screen's own menu (a Sort menu, say).

function cardMenu(item as object, extras as object, skip as object) as object
    m.cardItem = item
    state = item.state
    if state = invalid then state = {}
    video = (item.kind = "movie" or item.kind = "episode")
    ' what the scene plays: video, in the player
    playable = video
    rows = []
    if playable
        if progressOf(item) >= 0
            rows.push({id: "card:play", text: "Resume", icon: "play"})
            if video then rows.push({id: "card:beginning", text: "Play from the beginning", icon: "restart"})
        else
            rows.push({id: "card:play", text: "Play", icon: "play"})
        end if
    end if
    ' films and shows are watched (the mock-ups' word); music is played
    word = "played"
    if video or item.kind = "show" or item.kind = "season" then word = "watched"
    if state.watched = true then rows.push({id: "card:unwatched", text: "Mark as un" + word, icon: "refresh"}) else rows.push({id: "card:watched", text: "Mark as " + word, icon: "check"})
    for each extra in extras
        if extra.last <> true then rows.push(extra)
    end for
    if state.inList = true then rows.push({id: "card:unlist", text: "Remove from My List", icon: "close"}) else rows.push({id: "card:list", text: "Add to My List", icon: "plus"})
    if state.favorite = true then rows.push({id: "card:unfavorite", text: "Remove from Favorites", icon: "heartFilled"}) else rows.push({id: "card:favorite", text: "Add to Favorites", icon: "heart"})
    rows.push({id: "card:playlist", text: "Add to playlist…", icon: "listPlus"})
    rows.push({id: "card:collection", text: "Add to collection…", icon: "collection"})
    gap = true
    if item.kind = "episode" and item.rootId <> invalid and not menuSkips(skip, "show")
        rows.push({id: "card:show", text: "Go to show", icon: "forward", gapBefore: gap})
        gap = false
    end if
    ' the show's page opened on the episode's season, at the episode (tv-detail frame 7)
    if item.kind = "episode" and item.rootId <> invalid and item.parentId <> invalid and not menuSkips(skip, "season")
        rows.push({id: "card:season", text: "Go to season", icon: "list", gapBefore: gap})
        gap = false
    end if
    if (item.kind = "movie" or item.kind = "show" or item.kind = "collection") and not menuSkips(skip, "details")
        ' the pages the app has: a film's, a show's and a collection's
        rows.push({id: "card:details", text: "Details", icon: "info", gapBefore: gap})
        gap = false
    end if
    for each extra in extras
        if extra.last = true
            row = {id: extra.id, text: extra.text, icon: extra.icon, gapBefore: gap}
            rows.push(row)
            gap = false
        end if
    end for
    return {eyebrow: cardMenuEyebrow(item), title: item.title, status: cardMenuStatus(item), rows: rows, hints: [{keys: ["Back"], text: "Close"}]}
end function

function menuSkips(skip as object, id as string) as boolean
    for each name in skip
        if name = id then return true
    end for
    return false
end function

' Over the title: an episode's show and its code; anything else, its library and year.
function cardMenuEyebrow(item as object) as string
    if item.kind = "episode"
        show = ""
        if item.rootTitle <> invalid then show = item.rootTitle
        return (show + "   " + episodeCode(item)).trim()
    end if
    words = ""
    if item.libraryId <> invalid and m.global.libraryNames[item.libraryId] <> invalid then words = m.global.libraryNames[item.libraryId]
    if item.year <> invalid then words = (words + "   " + item.year.toStr()).trim()
    return words
end function

' Under the title: where the viewer is with it, then how long it runs.
function cardMenuStatus(item as object) as string
    where = ""
    if item.kind = "movie" or item.kind = "episode"
        where = "Not watched"
        if item.state <> invalid and item.state.watched = true then where = "Watched"
    end if
    if progressOf(item) >= 0 then where = "Resume, " + timeLeftText(item)
    length = durationText(item.durationMs)
    if where = "" then return length
    if length = "" then return where
    return where + "      " + length
end function

' Does what a menu row says, or returns false for a row that is the screen's own.
function cardMenuChosen(which as string) as boolean
    if left(which, 5) <> "card:" or m.cardItem = invalid then return false
    item = m.cardItem
    id = mid(which, 6)
    if left(id, 4) = "add:"
        ' a playlist or collection picked from the second sheet
        api("POST", "/v1/lists/" + mid(id, 5) + "/entries", {items: [item.id]}, "onCardMenuAdded", invalid, false, createObject("roDeviceInfo").getRandomUUID())
        return true
    end if
    changes = {watched: {watched: true}, unwatched: {watched: false}, list: {inList: true}, unlist: {inList: false}, favorite: {favorite: true}, unfavorite: {favorite: false}}
    if changes[id] <> invalid
        change = changes[id]
        change.madeAgoMs = 0
        api("PUT", "/v1/items/" + item.id + "/state", change, "onCardMenuSaved", {id: item.id})
    else if id = "play"
        m.top.open = {item: item, play: true, startOver: false}
    else if id = "beginning"
        m.top.open = {item: item, play: true, startOver: true}
    else if id = "show"
        m.top.open = {item: {id: item.rootId, kind: "show", title: item.rootTitle}, play: false}
    else if id = "season"
        m.top.open = {item: {id: item.rootId, kind: "show", title: item.rootTitle, seasonId: item.parentId, episodeId: item.id}, play: false}
    else if id = "details"
        m.top.open = {item: item, play: false}
    else if id = "playlist" or id = "collection"
        m.cardListKind = id
        api("GET", "/v1/lists?kind=" + id + "&limit=200", invalid, "onCardMenuLists")
    end if
    return true
end function

sub onCardMenuSaved(event as object)
    result = event.getData()
    if not result.ok or m.cardItem = invalid or m.cardItem.id <> event.getRoSGNode().ctx.id then return
    item = m.cardItem
    item.state = result.data
    cardMenuChanged(item)
end sub

' The profile's own playlists or collections, to add the title to; one it cannot change is left out.
sub onCardMenuLists(event as object)
    result = event.getData()
    if not result.ok or m.cardItem = invalid then return
    rows = []
    for each list in result.data.items
        if list.role = "owner" or list.role = "editor" then rows.push({id: "card:add:" + list.id, text: list.name, value: countText(list.count, "item", "items")})
    end for
    title = "Add to playlist"
    none = "No playlists yet. Make one on your phone or computer, then add to it here."
    if m.cardListKind = "collection"
        title = "Add to collection"
        none = "No collections yet. Make one on your phone or computer, then add to it here."
    end if
    status = ""
    if rows.count() = 0 then status = none
    m.top.menu = {eyebrow: m.cardItem.title, title: title, status: status, rows: rows, hints: [{keys: ["OK"], text: "Add"}, {keys: ["Back"], text: "Close"}]}
end sub

' Added: the list shows it the next time it is opened; nothing here changes.
sub onCardMenuAdded(event as object)
end sub
