' Three stops down the page: the buttons, More like this, Details. Down and Up move between them.
sub init()
    c = colours()
    m.top_ = m.top.findNode("top")
    m.hero = m.top.findNode("hero")
    m.credits = m.top.findNode("credits")
    m.buttons = m.top.findNode("buttons")
    m.smallTitle = m.top.findNode("smallTitle")
    m.sections = m.top.findNode("sections")
    setType(m.credits, "caption", c.textSecondary)
    setType(m.smallTitle, "pageTitle", c.text)
    m.buttons.observeField("pressed", "onPressed")
    m.sections.observeField("chosen", "onRelatedChosen")
    m.sections.observeField("hasRelated", "place")
    m.stop = 0            ' 0 the buttons, 1 More like this, 2 Details
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
    model.progress = -1
    if item.kind = "movie" and item.durationMs <> invalid
        if model.facts <> "" then model.facts = durationText(item.durationMs) + "      " + model.facts else model.facts = durationText(item.durationMs)
    end if
    if item.contentRating <> invalid then model.tag = item.contentRating else model.tag = ""
    m.hero.model = model
    m.top.art = model.art
    m.smallTitle.text = model.title

    line = ""
    director = creditNames(item.credits, "director", 2)
    stars = creditNames(item.credits, "actor", 3)
    if director <> "" then line = "Directed by " + director
    if stars <> ""
        if line <> "" then line = line + "      "
        line = line + "Starring " + stars
    end if
    m.credits.text = line

    buttons = []
    p = progressOf(item)
    if p >= 0
        buttons.push({id: "play", text: "Resume", icon: "play", progress: p, detail: timeLeftText(item)})
        buttons.push({id: "startover", text: "Start over", icon: "refresh"})
    else
        buttons.push({id: "play", text: "Play", icon: "play"})
    end if
    if item.state.inList then buttons.push({id: "list", text: "My List", icon: "check"}) else buttons.push({id: "list", text: "My List", icon: "plus"})
    if item.state.watched then buttons.push({id: "watched", text: "Played", icon: "check"}) else buttons.push({id: "watched", text: "Mark as played", icon: ""})
    m.buttons.buttons = buttons
    m.sections.item = item
    place()
end sub

' Lay the page out for the stop focus is on.
sub place()
    atTop = (m.stop = 0)
    m.top.quiet = not atTop
    m.top_.visible = atTop
    m.smallTitle.visible = not atTop
    if atTop then m.sections.translation = [0, 700] else m.sections.translation = [0, 164]
    if m.stop = 2 then m.sections.section = "details" else m.sections.section = "related"
end sub

sub focusStop()
    if m.stop = 0 then m.buttons.active = true else m.sections.active = true
end sub

sub onActive()
    if m.top.active then focusStop()
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
        item.state.inList = not item.state.inList
        save(item, {inList: item.state.inList})
    else if which = "watched"
        item.state.watched = not item.state.watched
        if item.state.watched then item.state.positionMs = invalid
        save(item, {watched: item.state.watched})
    end if
end sub

' The change shows at once; the server is told, and the page takes the state it answers with.
sub save(item as object, change as object)
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
    hasRelated = m.sections.hasRelated
    if key = "down"
        if m.stop = 0 and hasRelated
            moveTo(1)
        else if m.stop < 2
            moveTo(2)
        end if
        return true
    else if key = "up"
        if m.stop = 2 and hasRelated
            moveTo(1)
        else if m.stop > 0
            moveTo(0)
        end if
        return true
    end if
    return false
end function
