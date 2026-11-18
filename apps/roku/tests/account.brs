' The Portico Account pages' own code (components/screens/AccountPages.brs) run in the brs
' interpreter with stand-ins for their nodes and for api(): which service or server each request
' goes to, and what happens to an answer that arrives after the viewer has moved on. Nothing here
' calls Portico Services or a server. Run with tools/test.sh.

sub main()
    m.failed = 0
    m.passed = 0
    s = "https://web.getportico.tv"
    home = "https://home.example"
    away = "https://away.example"

    ' the code comes from Services, with a digest of a secret only this TV keeps
    fresh()
    start()
    first = lastSent()
    check("a code is asked of Portico Services", first.origin = s and first.method = "POST" and first.path = "/v1/tv-codes")
    check("with the digest of the TV's secret, never the secret", len(first.body.secretHash) = 43 and first.body.secretHash <> m.secret and len(m.secret) = 43)
    answer(first, {ok: true, status: 200, data: {id: "tv1", code: "ABCD-EFGH", verifyUrl: "https://web.getportico.tv/device", verifyUrlWithCode: "https://web.getportico.tv/device?code=ABCD-EFGH", pollAfterMs: 5000}})
    check("the code and its QR address show", m.nodes.card.code = "ABCD-EFGH" and m.nodes.card.link = "https://web.getportico.tv/device?code=ABCD-EFGH")
    onPoll()
    redeem = lastSent()
    check("the code is collected from Services with the secret", redeem.origin = s and redeem.path = "/v1/tv-codes/tv1/redeem" and redeem.body.secret = m.secret)

    ' a late answer for a code that was replaced is dropped
    newCode()
    answer(redeem, {ok: true, status: 200, data: {state: "approved", tokens: {accessToken: "old"}}})
    check("an approval for a replaced code is not taken", m.tokens = invalid)

    ' Sign in on this TV: the account typed with the remote, then its two-step code, at Services
    fresh()
    start()
    m.codeNote.text = ""
    onButton(joinEvent("onTV"))
    m.dialog.text = "nana_r"
    onAnswered()
    m.dialog.text = "words"
    onAnswered()
    check("the account is signed in at Services", lastSent().origin = s and lastSent().path = "/v1/sessions" and lastSent().body.login = "nana_r" and lastSent().body.password = "words")
    answer(lastSent(), {ok: true, status: 200, data: {twoStep: {challengeId: "ch1"}}})
    m.dialog.text = "123456"
    onAnswered()
    check("then its two-step code", lastSent().path = "/v1/sessions/two-step" and lastSent().body.challengeId = "ch1" and lastSent().body.code = "123456")
    answer(lastSent(), {ok: true, status: 200, data: {tokens: {accessToken: "typed"}}})
    check("and the account is read with the new sign-in", lastSent().path = "/v1/account" and lastSent().token = "typed")

    ' approved: the account, then its servers, both from Services with its own token
    signedInToServices()
    check("the servers are asked of Services with the account's token", lastSent().origin = s and lastSent().path = "/v1/servers?signature=rsa" and lastSent().token = "acct")

    ' each server is checked at its own addresses; online first
    listOf([{serverId: "srv_home", name: "Home", addresses: [home]}, {serverId: "srv_away", name: "Away", addresses: [away]}])
    checks = sentTo("/v1/system")
    check("each server is checked at its own address", checks.count() = 2 and checks[0].origin = home and checks[1].origin = away)
    answer(checks[1], {ok: true, status: 200, data: {id: "srv_away"}})
    answer(checks[0], {ok: true, status: 200, data: {id: "someone_else"}})
    check("a server answering as another is not online, and online comes first", m.servers[0].id = "srv_away" and m.servers[1].state = "offline")

    ' opening: the challenge and the exchange go to the address that answered as that server
    m.focus = 0
    openServer()
    answer(lastSent(), {ok: true, status: 200, data: {id: "srv_away"}})
    check("the challenge goes to the chosen server", lastSent().origin = away and lastSent().path = "/v1/auth/portico/challenge")
    answer(lastSent(), {ok: true, status: 200, data: {serverId: "srv_away", challenge: "c1"}})
    check("the note is asked of Services for that server", lastSent().origin = s and lastSent().path = "/v1/servers/srv_away/identity" and lastSent().body.challenge = "c1")
    answer(lastSent(), {ok: true, status: 200, data: {identity: {payload: "x"}}})
    exchange = lastSent()
    check("the note goes back to the same server", exchange.origin = away and exchange.path = "/v1/auth/portico/exchange" and exchange.body.challenge = "c1")
    answer(exchange, {ok: true, status: 200, data: {session: {accessToken: "from-away"}, profiles: []}})
    check("the sign-in is kept under that server's address", m.top.signedIn <> invalid and m.top.signedIn.origin = away and m.top.signedIn.session.accessToken = "from-away")
    check("it names the server and carries the account's sign-in for Switch server", m.top.signedIn.serverId = "srv_away" and m.top.signedIn.account.username = "sam")

    ' Switch server: the kept account sign-in is renewed at Services, and the wanted server opens
    fresh()
    m.store = {porticorefresh: "kept-refresh", porticouser: "sam"}
    resume("srv_away")
    check("the kept sign-in is renewed at Services", lastSent().origin = s and lastSent().path = "/v1/sessions/refresh" and lastSent().body.refreshToken = "kept-refresh")
    answer(lastSent(), {ok: true, status: 200, data: {accessToken: "acct2", refreshToken: "new-refresh"}})
    check("a renewed refresh token is kept", m.store.porticorefresh = "new-refresh")
    check("then the account's servers are read with the new token", lastSent().path = "/v1/servers?signature=rsa" and lastSent().token = "acct2")
    listOf([{serverId: "srv_home", name: "Home", addresses: [home]}, {serverId: "srv_away", name: "Away", addresses: [away]}])
    checks = sentTo("/v1/system")
    answer(checks[0], {ok: true, status: 200, data: {id: "srv_home"}})
    answer(checks[1], {ok: true, status: 200, data: {id: "srv_away"}})
    check("the wanted server opens by itself", m.stage = "opening" and m.server.id = "srv_away")

    ' a challenge from a server that answers as another is refused before anything goes to Services
    fresh()
    signedInToServices()
    listOf([{serverId: "srv_home", name: "Home", addresses: [home]}])
    answer(lastSent(), {ok: true, status: 200, data: {id: "srv_home"}})
    m.focus = 0
    openServer()
    answer(lastSent(), {ok: true, status: 200, data: {id: "srv_home"}})
    answer(lastSent(), {ok: true, status: 200, data: {serverId: "srv_other", challenge: "c2"}})
    check("a challenge from another server sends nothing to Services", sentTo("/v1/servers/srv_home/identity").count() = 0 and m.stage = "cant")

    ' the viewer chooses another server while the exchange is out: its answer is dropped
    fresh()
    signedInToServices()
    listOf([{serverId: "srv_home", name: "Home", addresses: [home]}])
    answer(lastSent(), {ok: true, status: 200, data: {id: "srv_home"}})
    m.focus = 0
    openServer()
    answer(lastSent(), {ok: true, status: 200, data: {id: "srv_home"}})
    answer(lastSent(), {ok: true, status: 200, data: {serverId: "srv_home", challenge: "c3"}})
    answer(lastSent(), {ok: true, status: 200, data: {identity: {payload: "x"}}})
    pending = lastSent()
    onKeyEvent("back", true)
    answer(pending, {ok: true, status: 200, data: {session: {accessToken: "late"}, profiles: []}})
    check("a late exchange answer after Back signs nothing in", m.top.signedIn = invalid)

    ' a server that no longer has this account says so
    fresh()
    signedInToServices()
    listOf([{serverId: "srv_home", name: "Home", addresses: [home]}])
    answer(lastSent(), {ok: true, status: 200, data: {id: "srv_home"}})
    m.focus = 0
    openServer()
    answer(lastSent(), {ok: true, status: 200, data: {id: "srv_home"}})
    answer(lastSent(), {ok: true, status: 200, data: {serverId: "srv_home", challenge: "c4"}})
    answer(lastSent(), {ok: true, status: 200, data: {identity: {payload: "x"}}})
    answer(lastSent(), {ok: false, status: 403, error: {code: "left_server", message: "You left this server."}})
    check("removed from its only server, the account gets the start page with what happened", m.stage = "start" and m.nodes.joinPages.notice.title = "You no longer have access to Home.")

    ' with other servers on the account, the no-longer-allowed page instead
    fresh()
    signedInToServices()
    listOf([{serverId: "srv_home", name: "Home", addresses: [home]}, {serverId: "srv_away", name: "Away", addresses: [away]}])
    checks = sentTo("/v1/system")
    answer(checks[0], {ok: true, status: 200, data: {id: "srv_home"}})
    answer(checks[1], {ok: true, status: 200, data: {id: "srv_away"}})
    m.focus = 0
    openServer()
    answer(lastSent(), {ok: true, status: 200, data: {id: m.server.id}})
    answer(lastSent(), {ok: true, status: 200, data: {serverId: m.server.id, challenge: "c6"}})
    answer(lastSent(), {ok: true, status: 200, data: {identity: {payload: "x"}}})
    answer(lastSent(), {ok: false, status: 403, error: {code: "left_server", message: "You left this server."}})
    check("with other servers, a removed account gets the no-longer-allowed page", m.stage = "refused")

    ' an account with no server: the start page; joining with a code at a Portico Account server
    fresh()
    signedInToServices()
    listOf([])
    check("an account with no server gets the start page", m.stage = "start")
    onJoinSubmitted(joinEvent({address: "home.example", code: "K7QP-2MXD-9HRT-4WNC"}))
    check("the typed address is asked who it is", lastSent().path = "/v1/system" and instr(1, lastSent().origin, "home.example") > 0)
    first = lastSent()
    answer(first, {ok: true, status: 200, data: {id: "srv_new", name: "New", signIn: "portico"}})
    answer(lastSent(), {ok: true, status: 200, data: {serverId: "srv_new", challenge: "c7"}})
    answer(lastSent(), {ok: true, status: 200, data: {identity: {payload: "x"}}})
    check("the exchange carries the invitation, at the address that answered", lastSent().path = "/v1/auth/portico/exchange" and lastSent().body.invitation = "K7QP-2MXD-9HRT-4WNC" and lastSent().origin = first.origin)
    answer(lastSent(), {ok: false, status: 404, error: {code: "invitation_not_found", message: "This invitation is no longer valid."}})
    check("a refused code goes back to the invitation's page with the reason", m.stage = "join" and m.nodes.joinPages.note = "This invitation is no longer valid.")

    ' joining a local server: the code is looked at, then a new username and password are accepted there
    fresh()
    signedInToServices()
    listOf([])
    onJoinSubmitted(joinEvent({address: "https://nas.example", code: "K7QP-2MXD-9HRT-4WNC"}))
    answer(lastSent(), {ok: true, status: 200, data: {id: "srv_nas", name: "NAS", signIn: "local"}})
    check("a local server's invitation is looked at first", lastSent().path = "/v1/auth/invitations/K7QP-2MXD-9HRT-4WNC" and lastSent().origin = "https://nas.example")
    answer(lastSent(), {ok: true, status: 200, data: {serverName: "NAS", role: "member"}})
    m.dialog.text = "sam"
    onAnswered()
    m.dialog.text = "secret-words"
    onAnswered()
    accepted = lastSent()
    check("then accepted with the new username and password", accepted.path = "/v1/auth/invitations/K7QP-2MXD-9HRT-4WNC/accept" and accepted.body.username = "sam" and accepted.body.password = "secret-words")
    onKeyEvent("back", true)
    answer(accepted, {ok: true, status: 200, data: {session: {accessToken: "late"}, profiles: []}})
    check("an acceptance after Back signs nothing in", m.top.signedIn = invalid)

    ' Services out of reach while asking for the note: the Portico Accounts page, not a failure
    fresh()
    signedInToServices()
    listOf([{serverId: "srv_home", name: "Home", addresses: [home]}])
    answer(lastSent(), {ok: true, status: 200, data: {id: "srv_home"}})
    m.focus = 0
    openServer()
    answer(lastSent(), {ok: true, status: 200, data: {id: "srv_home"}})
    answer(lastSent(), {ok: true, status: 200, data: {serverId: "srv_home", challenge: "c5"}})
    answer(lastSent(), {ok: false, status: 0, error: {code: "unreachable", message: ""}})
    check("Services away while opening shows Waiting for Portico Accounts", m.stage = "accounts")

    ? "PASSED "; m.passed; " FAILED "; m.failed
end sub

sub signedInToServices()
    start()
    answer(lastSent(), {ok: true, status: 200, data: {id: "tv9", code: "C", verifyUrl: "https://web.getportico.tv/device", verifyUrlWithCode: "q", pollAfterMs: 5000}})
    onPoll()
    answer(lastSent(), {ok: true, status: 200, data: {state: "approved", tokens: {accessToken: "acct"}}})
    answer(lastSent(), {ok: true, status: 200, data: {username: "sam"}})
end sub

' Services answers the server list: its payload is base64url JSON.
sub listOf(servers as object)
    bytes = createObject("roByteArray")
    bytes.fromAsciiString(formatJson({kind: "portico.server-list", servers: servers}))
    answer(lastSent(), {ok: true, status: 200, data: {list: {payload: urlSafe(bytes.toBase64String())}}})
end sub

' A new set of pages, as the sign-in screen makes one.
sub fresh()
    m.sent = []
    m.nodes = {}
    m.top = {signedIn: invalid, leave: "", active: false, nodes: m.nodes, setFocus: function(on as boolean) as boolean : return true : end function,
        getScene: function() as object : return {dialog: invalid} : end function,
        findNode: function(id as string) as object
            if m.nodes[id] = invalid then m.nodes[id] = standIn(id)
            return m.nodes[id]
        end function}
    m.tokens = invalid
    init()
end sub

function standIn(id as string) as object
    return {id: id, text: "", control: "", translation: [0, 0], visible: true, children: [], rowHeight: 84,
        observeField: function(field as string, callback as string) as boolean : return true : end function,
        setFocus: function(on as boolean) as boolean : return true : end function,
        removeChildrenIndex: function(count as integer, at as integer) as boolean : m.children = [] : return true : end function,
        createChild: function(kind as string) as object
            child = {translation: [0, 0], rowHeight: 84, model: invalid, on: false, separator: false}
            m.children.push(child)
            return child
        end function,
        getChildCount: function() as integer : return m.children.count() : end function,
        getChild: function(i as integer) as object : return m.children[i] : end function}
end function

function lastSent() as object
    return m.sent[m.sent.count() - 1]
end function

function sentTo(path as string) as object
    found = []
    for each request in m.sent
        if request.path = path then found.push(request)
    end for
    return found
end function

sub answer(request as object, result as object)
    event = {result: result, ctx: request.ctx, getData: function() as object : return m.result : end function, getRoSGNode: function() as object : return m : end function}
    if request.callback = "onCode" then onCode(event)
    if request.callback = "onRenewed" then onRenewed(event)
    if request.callback = "onSignedInHere" then onSignedInHere(event)
    if request.callback = "onJoinSystem" then onJoinSystem(event)
    if request.callback = "onInvitation" then onInvitation(event)
    if request.callback = "onAccepted" then onAccepted(event)
    if request.callback = "onRedeem" then onRedeem(event)
    if request.callback = "onAccount" then onAccount(event)
    if request.callback = "onServers" then onServers(event)
    if request.callback = "onSystem" then onSystem(event)
    if request.callback = "onChallenge" then onChallenge(event)
    if request.callback = "onIdentity" then onIdentity(event)
    if request.callback = "onExchange" then onExchange(event)
end sub

sub check(name as string, ok as boolean)
    if ok
        m.passed = m.passed + 1
    else
        m.failed = m.failed + 1
        ? "FAIL "; name
    end if
end sub

' --- stand-ins for what the pages' other scripts give them ------------------------------------

function api(method as string, path as string, body as dynamic, callback as string, ctx = invalid as dynamic, isPublic = false as boolean, key = "" as string, origin = "" as string, ifMatch = "" as string, token = "" as string) as object
    m.sent.push({method: method, path: path, body: body, callback: callback, ctx: ctx, origin: origin, token: token})
    return {}
end function

function colours() as object
    return {text: "0xEDF5FAFF", accentStrong: "", danger: "", textTertiary: "", textSecondary: "0xBDCCD6FF"}
end function

function frames() as object
    return {gutter: 200, signIn: {markX: 0, markY: 0, pageX: 0, noteY: 0, cardX: 0, cardY: 0, activateTitleBottom: 0, activateBodyY: 0, activateButtonsY: 0, termsY: 0,
        chooserEyebrowY: 0, chooserTitleY: 0, chooserBodyY: 0, rowsY: 0, rowsHeight: 360, chooserButtonsY: 0}}
end function

function typeRoles() as object
    return {authBody: [400, 26, 38]}
end function

sub setType(node as object, role as string, colour as string)
end sub

' Roku's digest is not in the interpreter: a stand-in secret and a stand-in digest of it
function secretText() as string
    return "S0123456789abcdefghijklmnopqrstuvwxyzABCDEF"
end function

function digestText(text as string) as string
    return "D0123456789abcdefghijklmnopqrstuvwxyzABCDEF"
end function

function urlSafe(base64 as string) as string
    return base64.replace("+", "-").replace("/", "_").replace("=", "")
end function

function joinEvent(data as object) as object
    return {data: data, getData: function() as object : return m.data : end function}
end function

' Roku's keyboard dialog: a stand-in the test answers by setting its text
function keyboardDialog(title as string, message as string, text as string, secret as boolean) as object
    return {title: title, text: text, buttonSelected: 0, close: false, observeField: function(field as string, callback as string) as boolean : return true : end function}
end function

function addressCandidates(typed as string) as object
    if left(typed, 8) = "https://" then return [typed]
    return ["https://" + typed]
end function

function storeRead(key as string) as string
    if m.store = invalid or m.store[lcase(key)] = invalid then return ""
    return m.store[lcase(key)]
end function

sub storeWrite(values as object)
    if m.store = invalid then m.store = {}
    for each key in values
        m.store[lcase(key)] = values[key]
    end for
end sub

function installationId() as string
    return "installation"
end function
