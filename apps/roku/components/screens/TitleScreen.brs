' Three stops down the page: the hero (its action row, and above it the collection link when the
' film has one), More like this, Details. Down and Up move between them.
sub init()
    c = colours()
    f = frames()
    m.tabsY = f.title.tabsY
    m.foldedTabsY = f.title.foldedTabsY
    m.hero = m.top.findNode("hero")
    m.hero.translation = [f.gutter, f.title.copyTop]
    m.sections = m.top.findNode("sections")
    m.hero.observeField("pressed", "onPressed")
    m.sections.observeField("chosen", "onRelatedChosen")
    m.sections.observeField("hasRelated", "place")
    m.stop = 0            ' 0 the hero, 1 More like this, 2 Details
    m.full = invalid
end sub

sub onItem()
    item = m.top.item
    m.full = item
    draw()
    api("GET", "/v1/items/" + item.id, invalid, "onDetail", {id: item.id})
end sub

sub onDetail(event as object)
    result = event.getData()
    if not result.ok or event.getRoSGNode().ctx.id <> m.top.item.id then return
    m.full = result.data
    draw()
end sub

sub draw()
    item = m.full
    model = heroFor(item)
    model.left = ""
    model.progress = -1         ' Resume carries the progress
    if item.durationMs <> invalid
        if model.facts <> "" then model.facts = durationText(item.durationMs) + "      " + model.facts else model.facts = durationText(item.durationMs)
    end if
    if item.contentRating <> invalid then model.tag = item.contentRating else model.tag = ""
    ' a film in a collection names it above the title, and Up from the buttons opens it
    group = collectionOf(item)
    if group <> invalid
        model.eyebrow = group.name
        if lcase(group.name).instr("collection") < 0 then model.eyebrow = group.name + " collection"
        model.eyebrowMore = countText(group.memberCount, "film", "films")
        model.link = true
    end if
    line = ""
    director = creditNames(item.credits, "director", 2)
    stars = creditNames(item.credits, "actor", 3)
    if director <> "" then line = "Directed by " + director
    if stars <> ""
        if line <> "" then line = line + "      "
        line = line + "Starring " + stars
    end if
    model.credits = line
    m.hero.model = model
    m.top.art = model.art

    state = item.state
    if state = invalid then state = {}
    buttons = []
    p = progressOf(item)
    if p >= 0
        buttons.push({id: "play", text: "Resume", icon: "play", progress: p, detail: timeLeftText(item)})
        buttons.push({id: "startover", text: "Start over", icon: "refresh"})
    else
        buttons.push({id: "play", text: "Play", icon: "play"})
    end if
    if state.inList = true then buttons.push({id: "list", text: "My List", icon: "check"}) else buttons.push({id: "list", text: "My List", icon: "plus"})
    ' the round buttons: played (the eye; a ticked circle once played), and the title's menu
    if state.watched = true then buttons.push({id: "watched", text: "", icon: "success"}) else buttons.push({id: "watched", text: "", icon: "watched"})
    buttons.push({id: "more", text: "", icon: "more"})
    m.hero.buttons = buttons
    m.sections.item = item
    place()
end sub

function collectionOf(item as object) as dynamic
    if item.collections = invalid or item.collections.count() = 0 then return invalid
    return item.collections[0]
end function

' Lay the page out for the stop focus is on.
sub place()
    atTop = (m.stop = 0)
    m.top.quiet = not atTop
    m.hero.folded = not atTop
    m.sections.folded = not atTop
    if atTop then m.sections.translation = [0, m.tabsY] else m.sections.translation = [0, m.foldedTabsY]
    if m.stop = 2 then m.sections.section = "details" else m.sections.section = "related"
end sub

sub focusStop()
    if m.stop = 0 then m.hero.active = true else m.sections.active = true
end sub

sub onActive()
    if not m.top.active then return
    ' back from the player (or a page opened from here): progress and played show at once
    if m.shownOnce = true and m.full <> invalid then api("GET", "/v1/items/" + m.full.id, invalid, "onDetail", {id: m.full.id})
    m.shownOnce = true
    focusStop()
end sub

sub moveTo(stopAt as integer)
    m.stop = stopAt
    place()
    focusStop()
end sub

sub onPressed(event as object)
    which = event.getData()
    item = m.full
    if which = "play"
        m.top.open = {item: item, play: true, startOver: false}
    else if which = "startover"
        m.top.open = {item: item, play: true, startOver: true}
    else if which = "list"
        save(item, {inList: not (item.state <> invalid and item.state.inList = true)})
    else if which = "watched"
        save(item, {watched: not (item.state <> invalid and item.state.watched = true)})
    else if which = "more"
        openMenu()
    else if which = "link"
        group = collectionOf(item)
        if group <> invalid then m.top.open = {item: {id: group.id, kind: "collection", title: group.name}, play: false}
    end if
end sub

' The "..." button and * open the title's menu; Details is left out, as this is its page. * on a
' More like this card opens that card's own menu, as on any other card.
sub openMenu()
    related = m.sections.focusedRelated
    if m.stop = 1 and related <> invalid and related.id <> invalid
        m.top.menu = cardMenu(related, [], [])
        return
    end if
    if m.full = invalid or m.full.state = invalid then return
    m.top.menu = cardMenu(m.full, [], ["details"])
end sub

sub onMenuChosen(event as object)
    cardMenuChosen(event.getData())
end sub

sub cardMenuChanged(item as object)
    if m.full = invalid or item.id <> m.full.id then return     ' a More like this card's change: the page is the same
    m.full = item
    draw()
end sub

' The change shows at once; the server is told, and the page takes the state it answers with.
sub save(item as object, change as object)
    if item.state = invalid then item.state = {}
    if change.inList <> invalid then item.state.inList = change.inList
    if change.watched <> invalid
        item.state.watched = change.watched
        if change.watched then item.state.positionMs = invalid
    end if
    m.full = item
    draw()
    change.madeAgoMs = 0
    api("PUT", "/v1/items/" + item.id + "/state", change, "onSaved", {id: item.id})
end sub

sub onSaved(event as object)
    result = event.getData()
    if event.getRoSGNode().ctx.id <> m.full.id then return
    if result.ok
        item = m.full
        item.state = result.data
        m.full = item
        draw()
    else
        api("GET", "/v1/items/" + m.full.id, invalid, "onDetail", {id: m.full.id})   ' put the true state back
    end if
end sub

sub onRelatedChosen(event as object)
    m.top.open = {item: event.getData(), play: false}
end sub

function onKeyEvent(key as string, press as boolean) as boolean
    if not press then return false
    if key = "options"
        openMenu()
        return true
    end if
    hasRelated = m.sections.hasRelated
    if key = "down"
        if m.stop = 0 and hasRelated
            moveTo(1)
        else if m.stop < 2
            moveTo(2)
        end if
        return true
    else if key = "up"
        if m.stop = 0 then return false      ' Up from the top goes on to the scene (the now-playing card)
        if m.stop = 2 and hasRelated
            moveTo(1)
        else
            moveTo(0)
        end if
        return true
    end if
    return false
end function
