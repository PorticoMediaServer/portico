sub init()
    c = colours()
    f = frames()
    m.gutter = f.gutter
    ' the strip's rule runs across the page, its marker under the section shown
    rule = m.top.findNode("rule")
    rule.color = c.line
    rule.width = f.rightEdge - f.gutter
    rule.translation = [f.gutter, 52]
    m.top.findNode("mark").color = c.accent
    ' Details: About in the page's column, the facts in the frame's second column
    m.top.findNode("aboutHead").translation = [f.gutter, 0]
    m.top.findNode("about").translation = [f.gutter, 56]
    m.top.findNode("facts").translation = [f.title.factsX, 4]
    m.tabs = {lead: m.top.findNode("tabLead"), related: m.top.findNode("tabRelated"), details: m.top.findNode("tabDetails")}
    m.mark = m.top.findNode("mark")
    m.related = m.top.findNode("related")
    m.details = m.top.findNode("details")
    m.cast = m.top.findNode("cast")
    for each name in m.tabs
        setType(m.tabs[name], "section")
    end for
    setType(m.top.findNode("aboutHead"), "heading", c.text)
    setType(m.top.findNode("about"), "about", c.textSecondary)
    setType(m.top.findNode("tagline"), "body", c.textTertiary)
    m.related.observeField("selectedIndex", "onRelatedSelected")
    m.related.observeField("focusedIndex", "onRelatedFocused")
    m.cast.observeField("selectedIndex", "onCastSelected")
    m.people = []
    m.relatedItems = []
    m.readFor = ""
end sub

sub onItem()
    item = m.top.item
    c = colours()
    if item.id <> m.readFor
        m.readFor = item.id
        api("GET", "/v1/items/" + item.id + "/related", invalid, "onRelated", {id: item.id})
    end if
    about = ""
    if item.summary <> invalid then about = item.summary
    m.top.findNode("about").text = oneParagraph(about)
    m.top.findNode("aboutHead").visible = (about <> "")
    tag = m.top.findNode("tagline")
    tag.text = ""
    if item.tagline <> invalid then tag.text = item.tagline
    tag.translation = [m.gutter, 56 + m.top.findNode("about").boundingRect().height + 18]
    facts = m.top.findNode("facts")
    facts.removeChildrenIndex(facts.getChildCount(), 0)
    rows = [["Released", dateText(item.releaseDate)], ["Directed by", creditNames(item.credits, "director", 2)], ["Written by", creditNames(item.credits, "writer", 3)], ["Genres", tagNames(item, "genre", 3)], ["Studio", tagNames(item, "studio", 2)], ["Added", dateText(item.addedAt)]]
    y = 0
    for each row in rows
        if row[1] <> ""
            name = facts.createChild("Label")
            name.text = row[0]
            name.translation = [0, y]
            setType(name, "factLabel", c.textTertiary)
            value = facts.createChild("Label")
            value.text = row[1]
            value.width = 520
            value.wrap = true
            value.maxLines = 2
            value.translation = [194, y]
            setType(value, "factLabel", c.text)
            if value.boundingRect().height > 40 then y = y + 30
            y = y + 42
        end if
    end for
    people = createObject("roSGNode", "ContentNode")
    m.people = []
    if item.credits <> invalid
        ' cast, then crew, each in the server's order
        for each wantCast in [true, false]
            for each credit in item.credits
                if (credit.role = "actor") = wantCast
                    card = personCard(credit)
                    m.people.push({kind: "person", id: credit.personId, title: credit.name})
                    card.art = imageUrl(card.art, 150, 150)
                    people.createChild("ContentNode").addFields(card)
                end if
            end for
        end for
    end if
    m.cast.cards = people
    m.cast.visible = (people.getChildCount() > 0)
    place()
end sub

sub onRelated(event as object)
    result = event.getData()
    if not result.ok or event.getRoSGNode().ctx.id <> m.readFor then return
    items = []
    seen = {}
    seen[m.readFor] = true
    for each group in result.data.groups
        for each item in withoutReading(group.items)
            if seen[item.id] <> true
                seen[item.id] = true
                items.push(item)
            end if
        end for
    end for
    m.relatedItems = items
    cards = createObject("roSGNode", "ContentNode")
    for each item in items
        card = cardFor(item, "poster")
        card.art = imageUrl(card.art, 214, 320)
        cards.createChild("ContentNode").addFields(card)
    end for
    m.related.cards = cards
    m.top.hasRelated = (items.count() > 0)
    place()
end sub

sub place()
    c = colours()
    section = m.top.section
    shown = {lead: m.top.lead <> "", related: m.top.hasRelated, details: true}
    if not shown[section] then section = "details"
    m.tabs.lead.text = m.top.lead
    x = m.gutter
    for each name in ["lead", "related", "details"]
        label = m.tabs[name]
        label.visible = shown[name]
        if shown[name]
            label.translation = [x, 0]
            w = label.boundingRect().width
            if name = section
                label.color = c.text
                m.mark.translation = [x, 50]
                m.mark.width = w
            else
                label.color = c.textTertiary
            end if
            x = x + w + 44
        end if
    end for
    ' On a page's opening view the next section's row shows under the strip before focus reaches it.
    peeking = m.top.peek and shown.related and section = "lead"
    m.related.visible = peeking or (shown.related and section = "related")
    m.details.visible = (section = "details")
    ' On the opening view the posters sit on the frame's first row line, straight under the strip;
    ' folded up, the row has its heading ("More like Dune: Part Two") on the frame's heading line,
    ' or, peeking under a show's episodes, on the frame's peek line.
    f = frames().title
    gap = shelfMetrics("poster").headingGap
    if peeking and m.top.item <> invalid
        m.related.heading = "More like " + m.top.item.title
        m.related.translation = [0, f.peekY - f.foldedTabsY]
    else if m.top.folded and m.top.item <> invalid
        m.related.heading = "More like " + m.top.item.title
        m.related.translation = [0, f.foldedHeadingY - f.foldedTabsY]
    else
        m.related.heading = ""
        m.related.translation = [0, f.rowY - f.tabsY - gap]
    end if
end sub

sub onActive()
    if not m.top.active then return
    if m.related.visible
        m.related.active = true
    else if m.details.visible and m.cast.visible
        m.cast.active = true
    else
        m.top.setFocus(true)
    end if
end sub

sub onCastSelected(event as object)
    at = event.getData()
    if at >= 0 and at < m.people.count() then m.top.chosen = m.people[at]
end sub

sub onRelatedFocused(event as object)
    at = event.getData()
    if at >= 0 and at < m.relatedItems.count() then m.top.focusedRelated = m.relatedItems[at]
end sub

sub onRelatedSelected(event as object)
    at = event.getData()
    if at >= 0 and at < m.relatedItems.count() then m.top.chosen = m.relatedItems[at]
end sub
