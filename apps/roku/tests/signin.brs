' The sign-in screen's own code (components/screens/SignInScreen.brs) run in the brs interpreter
' with stand-ins for the screen's nodes and for api(): what it does with a server's answer that
' arrives after the viewer has moved on. Run with tools/test.sh; nothing here runs on a Roku.

sub main()
    m.failed = 0
    m.passed = 0
    a = "https://a.example"
    b = "https://b.example"

    ' a password answer in time signs in, under the address the password was sent to
    fresh()
    choose(a)
    m.username = "sam"
    signInWithPassword("pw")
    check("the password goes to the server being signed in to", lastSent().origin = a and lastSent().path = "/v1/auth/sign-in")
    answer(lastSent(), {ok: true, status: 200, data: {session: {accessToken: "from-a"}}})
    check("an answer in time signs in under its own server", m.top.signedIn <> invalid and m.top.signedIn.origin = a and m.top.signedIn.session.accessToken = "from-a")
    check("the sign-in names the server's id, from the same server", m.top.signedIn.serverId = "srv")

    ' the viewer goes back and chooses another server before the first one answers
    fresh()
    choose(a)
    m.username = "sam"
    signInWithPassword("pw")
    fromA = lastSent()
    onKeyEvent("back", true)
    choose(b)
    answer(fromA, {ok: true, status: 200, data: {session: {accessToken: "from-a"}}})
    check("a late password answer from another server is dropped", m.top.signedIn = invalid)
    check("and the page stays on the new server's code", m.stage = "code" and m.origin = b)

    ' the same for the two-step code
    fresh()
    choose(a)
    m.username = "sam"
    signInWithPassword("pw")
    answer(lastSent(), {ok: true, status: 200, data: {challenge: {token: "challenge-a"}}})
    check("a two-step challenge opens the pad", m.stage = "factor")
    onFactor({getData: function() as string : return "123456" : end function})
    fromA = lastSent()
    check("the two-step code goes to the server that asked for it", fromA.origin = a and fromA.body.challenge = "challenge-a")
    onKeyEvent("back", true)
    onKeyEvent("back", true)
    choose(b)
    answer(fromA, {ok: true, status: 200, data: {session: {accessToken: "from-a"}}})
    check("a late two-step answer from another server is dropped", m.top.signedIn = invalid)

    ' a late code from the first server is not shown, or polled, at the second
    fresh()
    choose(a)
    codeFromA = lastSent()
    onKeyEvent("back", true)
    choose(b)
    answer(codeFromA, {ok: true, status: 200, data: {code: "AAAA-AAAA", deviceSecret: "secret-a"}})
    check("a late code from another server is not shown", m.pending = invalid and m.nodes.card.code = "")

    ' a two-step sign-in that ran out is not a wrong code: back to the code page with the server's reason
    fresh()
    choose(a)
    m.username = "sam"
    signInWithPassword("pw")
    answer(lastSent(), {ok: true, status: 200, data: {challenge: {token: "challenge-a"}}})
    onFactor({getData: function() as string : return "123456" : end function})
    answer(lastSent(), {ok: false, status: 403, error: {code: "two_factor_invalid", message: "That code is not right."}})
    check("a wrong two-step code keeps the pad up", m.stage = "factor" and m.nodes.factorPad.wrong = true)
    onFactor({getData: function() as string : return "123456" : end function})
    answer(lastSent(), {ok: false, status: 401, error: {code: "unauthorized", message: "This sign-in took too long. Sign in again."}})
    check("a two-step sign-in that ran out goes back with the reason", m.stage = "code" and m.nodes.codeNote.text = "This sign-in took too long. Sign in again.")

    ' an account that must still set up two-step sign-in is told so, and is not let through
    fresh()
    choose(a)
    m.username = "sam"
    signInWithPassword("pw")
    answer(lastSent(), {ok: true, status: 200, data: {session: {accessToken: "t", twoStepSetupRequired: true}}})
    check("a sign-in that still needs two-step set-up is not let through", m.top.signedIn = invalid and m.stage = "code" and instr(1, m.nodes.codeNote.text, "Set up two-step sign-in") = 1)

    ' servers found on this network: listed under their own name only when they answer as the server
    ' that announced itself, and opened held to that id
    fresh()
    go("local")
    m.discovery = {isSameNode: function(other as object) as boolean : return true : end function}
    found = {data: [{id: "srv_home", fp: "f", ip: "192.168.1.20", port: 32500}, {id: "srv_fake", fp: "f", ip: "192.168.1.30", port: 32500}],
        getData: function() as object : return m.data : end function, getRoSGNode: function() as object : return m.node : end function, node: m.discovery}
    onFound(found)
    checks = []
    for each request in m.sent
        if request.callback = "onNearbySystem" then checks.push(request)
    end for
    check("each found server is asked at its own address", checks.count() = 2 and checks[0].origin = "http://192.168.1.20:32500" and checks[1].origin = "http://192.168.1.30:32500")
    answer(checks[0], {ok: true, status: 200, data: {id: "srv_home", name: "Home"}})
    answer(checks[1], {ok: true, status: 200, data: {id: "srv_other", name: "Impostor"}})
    check("only the one that answers as itself is listed", m.listed.count() = 1 and m.listed[0].name = "Home")
    openNearby(m.listed[0])
    opening = lastSent()
    check("opening it goes the typed address's way, to its own address", opening.callback = "onSystem" and opening.origin = "http://192.168.1.20:32500")
    answer(opening, {ok: true, status: 200, data: {id: "srv_changed", name: "Home", signInMethods: ["password"]}})
    check("and stops if it then answers as another server", m.stage = "local")

    ' a late check of a found server after Back is dropped
    fresh()
    go("local")
    m.discovery = {isSameNode: function(other as object) as boolean : return true : end function}
    onFound({data: [{id: "srv_home", fp: "f", ip: "192.168.1.20", port: 32500}], getData: function() as object : return m.data : end function, getRoSGNode: function() as object : return {isSameNode: function(o as object) as boolean : return true : end function} : end function})
    pending = lastSent()
    onKeyEvent("back", true)
    answer(pending, {ok: true, status: 200, data: {id: "srv_home", name: "Home"}})
    check("a found server's late answer after Back lists nothing", m.listed = invalid or m.listed.count() = 0)

    ? "PASSED "; m.passed; " FAILED "; m.failed
end sub

' A new sign-in screen, as the app makes one.
sub fresh()
    m.sent = []
    m.nodes = {}
    m.top = {signedIn: invalid, active: false, nodes: m.nodes, setFocus: function(on as boolean) as boolean : return true : end function, findNode: function(id as string) as object
        if m.nodes[id] = invalid then m.nodes[id] = {id: id, text: "", control: "", children: [], observeField: function(field as string, callback as string) as boolean : return true : end function,
            removeChildrenIndex: function(count as integer, at as integer) as boolean : m.children = [] : return true : end function,
            createChild: function(kind as string) as object
                child = {translation: [0, 0], rowHeight: 84, model: invalid, on: false, separator: false}
                m.children.push(child)
                return child
            end function,
            getChildCount: function() as integer : return m.children.count() : end function,
            getChild: function(i as integer) as object : return m.children[i] : end function}
        return m.nodes[id]
    end function}
    init()
    m.pending = invalid
end sub

' Type an address and have that server answer as a Portico server: its code page is then showing.
sub choose(origin as string)
    tryAddress(origin)
    answer(lastSent(), {ok: true, status: 200, data: {id: "srv", name: "Server", signInMethods: ["password"]}})
end sub

function lastSent() as object
    return m.sent[m.sent.count() - 1]
end function

sub answer(request as object, result as object)
    event = {result: result, ctx: request.ctx, getData: function() as object : return m.result : end function, getRoSGNode: function() as object : return m : end function}
    if request.callback = "onSystem" then onSystem(event)
    if request.callback = "onNearbySystem" then onNearbySystem(event)
    if request.callback = "onCode" then onCode(event)
    if request.callback = "onPasswordAnswer" then onPasswordAnswer(event)
    if request.callback = "onFactorAnswer" then onFactorAnswer(event)
end sub

sub check(name as string, ok as boolean)
    if ok
        m.passed = m.passed + 1
    else
        m.failed = m.failed + 1
        ? "FAIL "; name
    end if
end sub

' --- stand-ins for what the screen's other scripts give it -------------------------------------

function api(method as string, path as string, body as dynamic, callback as string, ctx = invalid as dynamic, isPublic = false as boolean, key = "" as string, origin = "" as string) as object
    m.sent.push({method: method, path: path, body: body, callback: callback, ctx: ctx, origin: origin})
    return {}
end function

function colours() as object
    return {text: "", accentStrong: "", danger: "", textTertiary: "", textSecondary: ""}
end function

function frames() as object
    return {gutter: 0, signIn: {markX: 0, markY: 0, doorTitleY: 0, doorButtonsY: 0, doorNoteY: 0, localTitleY: 0, localBodyY: 0, nearbyLabelY: 0, nearbyRowsY: 0, elsewhereLabelY: 0, localButtonsY: 0, localNoteY: 0, pageX: 0, eyebrowY: 0, titleY: 0, bodyY: 0,
        buttonsY: 0, noteY: 0, pressY: 0, factorTitleY: 0, factorBodyY: 0, factorBoxesY: 0, cardX: 0, cardY: 0, keysX: 0, keysY: 0}}
end function

function typeRoles() as object
    return {authBody: [400, 26, 38]}
end function

sub setType(node as object, role as string, colour as string)
end sub

function installationId() as string
    return "installation"
end function
