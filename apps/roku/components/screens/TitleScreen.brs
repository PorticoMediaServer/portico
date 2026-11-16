sub init()
    c = colours()
    m.top_ = m.top.findNode("top")
    m.hero = m.top.findNode("hero")
    m.credits = m.top.findNode("credits")
    m.buttons = m.top.findNode("buttons")
    m.smallTitle = m.top.findNode("smallTitle")
    m.strip = m.top.findNode("strip")
    m.tabRelated = m.top.findNode("tabRelated")
    m.tabDetails = m.top.findNode("tabDetails")
    m.stripMark = m.top.findNode("stripMark")
    m.related = m.top.findNode("related")
    m.details = m.top.findNode("details")
    m.cast = m.top.findNode("cast")
    setType(m.credits, "caption", c.textSecondary)
    setType(m.smallTitle, "pageTitle", c.text)
    setType(m.tabRelated, "section")
    setType(m.tabDetails, "section")
    setType(m.top.findNode("aboutHead"), "heading", c.text)
    setType(m.top.findNode("about"), "about", c.textSecondary)
    setType(m.top.findNode("tagline"), "body", c.textTertiary)
    m.buttons.observeField("pressed", "onPressed")
    m.related.observeField("selectedIndex", "onRelatedSelected")
    m.stop = 0            ' 0 the buttons, 1 More like this, 2 Details
    m.relatedItems = []
    m.full = invalid
end sub

sub onItem()
    item = m.top.item
    m.full = item
    draw()
    api("GET", "/v1/items/" + item.id, invalid, "onDetail", {id: item.id})
    api("GET", "/v1/items/" + item.id + "/related", invalid, "onRelated", {id: item.id})
end sub

sub onDetail(event as object)
    result = event.getData()
    if not result.ok or event.getRoSGNode().ctx.id <> m.top.item.id then return
    m.full = result.data
    draw()
end sub

sub onRelated(event as object)
    result = event.getData()
    if not result.ok or event.getRoSGNode().ctx.id <> m.top.item.id then return
    items = []
    for each group in result.data.groups
        items.append(withoutReading(group.items))
    end for
    m.relatedItems = items
    cards = createObject("roSGNode", "ContentNode")
    for each item in items
        card = cardFor(item, "poster")
        card.art = imageUrl(card.art, 214, 320)
        cards.createChild("ContentNode").addFields(card)
    end for
    m.related.cards = cards
    place()
end sub

sub draw()
    item = m.full
    c = colours()
    model = heroFor(item)
    model.left = ""
    model.progress = -1
    if item.kind = "movie" and item.durationMs <> invalid then model.facts = joinFacts(durationText(item.durationMs), model.facts)
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

    ' Details
    about = ""
    if item.summary <> invalid then about = item.summary
    m.top.findNode("about").text = about
    tag = m.top.findNode("tagline")
    tag.text = ""
    if item.tagline <> invalid then tag.text = item.tagline
    tag.translation = [200, 56 + m.top.findNode("about").boundingRect().height + 18]
    facts = m.top.findNode("facts")
    facts.removeChildrenIndex(facts.getChildCount(), 0)
    rows = [["Released", dateText(item.releaseDate)], ["Directed by", director], ["Written by", creditNames(item.credits, "writer", 3)], ["Genres", tagNames(item, "genre", 3)], ["Studio", tagNames(item, "studio", 2)], ["Added", dateText(item.addedAt)]]
    y = 0
    for each row in rows
        if row[1] <> ""
            name = facts.createChild("Label")
            name.text = row[0]
            name.translation = [0, y]
            setType(name, "factLabel", c.textTertiary)
            value = facts.createChild("Label")
            value.text = row[1]
            value.width = 570
            value.translation = [194, y]
            setType(value, "factLabel", c.text)
            y = y + 42
        end if
    end for
    people = createObject("roSGNode", "ContentNode")
    if item.credits <> invalid
        for each credit in item.credits
            card = personCard(credit)
            card.art = imageUrl(card.art, 150, 150)
            people.createChild("ContentNode").addFields(card)
        end for
    end if
    m.cast.cards = people
    m.cast.visible = (people.getChildCount() > 0)
    place()
end sub

function joinFacts(a as string, b as string) as string
    if a = "" then return b
    if b = "" then return a
    return a + "      " + b
end function

' Lay the page out for the stop focus is on.
sub place()
    c = colours()
    hasRelated = (m.relatedItems.count() > 0)
    atTop = (m.stop = 0)
    m.top_.visible = atTop
    m.smallTitle.visible = not atTop
    m.credits.translation = [200, 428]
    m.buttons.translation = [200, 484]
    stripY = 700
    if not atTop then stripY = 164
    m.strip.translation = [0, stripY]
    m.tabRelated.visible = hasRelated
    x = 200
    if hasRelated
        m.tabRelated.translation = [x, 0]
        x = x + m.tabRelated.boundingRect().width + 44
    end if
    m.tabDetails.translation = [x, 0]
    onDetails = (m.stop = 2 or not hasRelated)
    if onDetails
        m.tabDetails.color = c.text
        m.tabRelated.color = c.textTertiary
        m.stripMark.translation = [x, 50]
        m.stripMark.width = m.tabDetails.boundingRect().width
    else
        m.tabRelated.color = c.text
        m.tabDetails.color = c.textTertiary
        m.stripMark.translation = [200, 50]
        m.stripMark.width = m.tabRelated.boundingRect().width
    end if
    m.related.visible = hasRelated and m.stop < 2
    m.related.translation = [0, stripY + 24]
    m.details.visible = (m.stop = 2) or (not hasRelated)
    m.details.translation = [0, stripY + 90]
end sub

sub focusStop()
    if m.stop = 1
        m.related.active = true
    else if m.stop = 2
        if m.cast.visible then m.cast.active = true else m.top.setFocus(true)
    else
        m.buttons.active = true
    end if
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
        save({inList: item.state.inList})
    else if which = "watched"
        item.state.watched = not item.state.watched
        if item.state.watched then item.state.positionMs = invalid
        save({watched: item.state.watched})
    end if
end sub

' The change shows at once; the server is told, and the page takes the state it answers with.
sub save(change as object)
    m.full = m.full
    draw()
    change.madeAgoMs = 0
    api("PUT", "/v1/items/" + m.full.id + "/state", change, "onSaved", {id: m.full.id})
end sub

sub onSaved(event as object)
    result = event.getData()
    if event.getRoSGNode().ctx.id <> m.full.id then return
    if result.ok
        m.full.state = result.data
        draw()
    else
        api("GET", "/v1/items/" + m.full.id, invalid, "onDetail", {id: m.full.id})   ' put the true state back
    end if
end sub

sub onRelatedSelected(event as object)
    at = event.getData()
    if at >= 0 and at < m.relatedItems.count() then m.top.open = {item: m.relatedItems[at], play: false}
end sub

function onKeyEvent(key as string, press as boolean) as boolean
    if not press then return false
    hasRelated = (m.relatedItems.count() > 0)
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
