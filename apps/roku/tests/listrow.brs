' kit/ListRow's own code (components/kit/ListRow.brs) run in the brs interpreter with stand-ins for
' its nodes: a row's model may leave out any flag it does not use (Settings search's matches and
' Saved's History rows have no `danger`), and on a Roku an IF over the missing value stops the app.
' Run with tools/test.sh; nothing here runs on a Roku.

sub main()
    m.failed = 0
    m.passed = 0
    ' the brs interpreter lets an IF over a missing value pass, so the guard's own answers are checked
    check("a missing flag, or a word, is not on", not isTrue(invalid) and not isTrue("follow") and isTrue(true) and not isTrue(false))
    fresh({label: "Pause history", help: "Nothing new is added", scope: "", value: "", control: "", switchOn: false, icon: ""})
    layout()
    check("a row without danger lays out, its label in the text colour", m.nodes.label.color = "text" and m.top.rowHeight >= 84)
    fresh({label: "Sign out", help: "", scope: "", value: "", control: "", switchOn: false, icon: "signOut", danger: true})
    layout()
    check("a row with danger has a red label", m.nodes.label.color = "danger")
    fresh({label: "Autoplay", help: "", scope: "This device", value: "", control: "switch", switchOn: true, icon: ""})
    layout()
    check("a switch row lays out with its switch on", m.nodes.switch.uri = "pkg:/images/switch-on.png")
    print "PASSED " + m.passed.toStr() + " FAILED " + m.failed.toStr()
end sub

sub check(name as string, ok as boolean)
    if ok
        m.passed = m.passed + 1
    else
        m.failed = m.failed + 1
        print "FAILED: " + name
    end if
end sub

' A row with stand-in nodes, one made for whatever id the row asks for (so a node the row gains
' later needs nothing here): each is an object whose words measure 12 px a letter, 30 px a line.
sub fresh(model as object)
    m.nodes = {}
    m.top = {model: model, rowWidth: 1064, rowHeight: 0, on: false, separator: false, nodes: m.nodes, findNode: function(id as string) as object
        if m.nodes[id] = invalid
            m.nodes[id] = {text: "", width: 0, boundingRect: function() as object
                if m.text = "" then return {width: 0, height: 0}
                return {width: len(m.text) * 12, height: 30}
            end function}
        end if
        return m.nodes[id]
    end function}
end sub

' --- stand-ins for what the row's other scripts give it -------------------------------------------

function colours() as object
    return {text: "text", danger: "danger", textSecondary: "textSecondary", textTertiary: "textTertiary", raisedBright: "raisedBright", separator: "separator"}
end function

function frames() as object
    return {settings: {row: 84}}
end function

function metrics() as object
    return {focusSpread: 30}
end function

sub setType(node as object, role as string, colour = "" as string)
end sub
